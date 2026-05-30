# Current state (pre-alpha, as of M8)

An honest snapshot of what GoDB can and can't do right now, refreshed every milestone. This page exists so a reader doesn't have to scan commit history or trial-and-error their way into the API surface.

If you're new here, read [`README.md`](README.md) in this directory first. It frames the project status; this page goes one level deeper.

## What works

As of M8, `pkg/godb` exposes the engine through a stable public Go API. Internally, nine packages collaborate; all exercised by `make test` and `make race`. The [embedded-API tutorial](embedded-api.md) shows the public-facing path. The internal layers below are documented here for readers who want a map of how the engine fits together.

### `internal/storage` — the pager

Opens, creates, and reads/writes a single `.godb` file as a sequence of fixed 4 KB pages.

- `OpenPager(path, PagerOptions{CreateIfMissing: true})` opens or creates a database.
- `pager.AllocatePage(PageTypeTableLeaf)` appends a new page and returns it.
- `pager.ReadPage(id)` / `pager.WritePage(pg)` round-trip a page through disk.
- `pager.Sync()` fsyncs the file (so writes are durable on return).
- `pager.SetCatalogRoot(id)` writes the catalog/primary-tree root into the database header so it survives close/reopen.
- `pager.Header()` returns a copy of the header (catalog root, page count, format version, etc.).

The file format is documented in [ADR-0001](../adr/0001-single-file-fixed-pages.md) and the page header layout in [chapter 03](../book/03-milestone-1-pager.md).

### `internal/record` — typed values and row codec

Encodes and decodes typed rows as opaque bytes for storage in the tree's cells. Zero I/O.

- `record.Null() / record.Int(n) / record.Text(s) / record.Bool(b)` build values.
- `record.EncodeRow([]record.Value) []byte` produces the byte payload.
- `record.DecodeRow([]byte) ([]record.Value, int, error)` reads it back.
- `record.Schema{Columns: …}` defines a table shape; `schema.Validate(values)` enforces column count, nullability, and per-column type.

NULL and empty TEXT are distinct on-disk encodings ([ADR-0008](../adr/0008-null-and-empty-text-distinct.md)). `Kind` byte values are explicit and never reordered ([ADR-0007](../adr/0007-explicit-kind-byte-values.md)).

### `internal/btree` — slotted pages + B+tree

The whole storage-engine ladder above the pager:

- `btree.InitLeaf / InsertCell / GetCell / IterateCells / Validate` — slotted-page primitives on a single page.
- `btree.InitInternal / InsertInternalCell / FindChild / IterateInternalCells / RightmostChild` — the analogous primitives for internal pages.
- `btree.Create(pager) (*Tree, error)` — make a fresh B+tree (one empty leaf as root).
- `btree.Open(pager, rootID) *Tree` — wrap an existing tree by its root page id.
- `tree.Insert(key, payload)` — insert a (uvarint, []byte) cell. Descends to the right leaf, splits leaves/internal pages and grows the root as needed.
- `tree.Get(key)` — descend to the target leaf and return the payload.
- `tree.Scan(fn)` — visit every cell across every leaf in key order, via the leaf chain.
- `tree.Validate()` — full tree walk checking slotted-page invariants, separator/key-range consistency, and equal-leaf-depth.

Slotted layout: [ADR-0010](../adr/0010-slotted-page-layout.md). Cell formats and split policy are walked through in [chapter 05](../book/05-milestone-3-slotted-pages.md) and [chapter 07](../book/07-milestone-5-multi-page-btree.md). The dual-purpose `RightSibling` header field: [ADR-0013](../adr/0013-rightsibling-dual-semantics.md).

### `internal/catalog` — named tables and persistent metadata

The metadata layer above `btree`. Keeps the name → root-page-id + schema mapping for every table in the database, persisted via a B+tree of its own.

- `catalog.Open(pager) (*Catalog, error)` — bootstraps the catalog. On a fresh database it allocates the catalog's tree and writes the root id to `Header.CatalogRootPageID`. On an existing one it walks the tree and rebuilds the in-memory name index.
- `catalog.CreateTable(name, schema, sql) (*TableInfo, error)` — allocates a fresh B+tree for the new table, encodes its metadata, inserts it into the catalog tree, and returns a `TableInfo` with the new id + root page id.
- `catalog.LookupTable(name) (*TableInfo, error)` — O(1) cache hit.
- `catalog.ListTables() []*TableInfo` — snapshot of every registered table.
- `catalog.Sync() error` — persists the catalog tree's root id to the header and flushes the pager.

On-disk catalog rows are a custom binary format (not `record.EncodeRow`) starting with the two-byte magic+version prefix `0xCA 0x01` — the magic byte fences pre-M6 `.godb` files cleanly. Documented in [ADR-0014](../adr/0014-catalog-row-encoding.md) and walked through in [chapter 08](../book/08-milestone-6-catalog.md).

### `internal/sql` — lexer, parser, AST

The SQL frontend. Turns a string like `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL);` into a typed AST. No execution yet (M9).

- `sql.Parse(src) (Statement, error)` — parse exactly one statement. Trailing tokens after the statement are a syntax error (use `ParseAll` for multi-statement scripts).
- `sql.ParseAll(src) ([]Statement, error)` — parse zero or more statements separated by `;`.
- `sql.NewLexer(src) *Lexer` — tokenize directly if you need the lexer without the parser. `Lexer.Next` / `Lexer.Peek`.
- `sql.ColumnDefsToSchema(defs) record.Schema` — small bridge that converts a parsed `CREATE TABLE`'s `Columns` into the catalog's expected `record.Schema` shape.

Supported grammar: `CREATE TABLE`, `INSERT INTO`, `SELECT ... [WHERE column = expr]`. Three column types (`INTEGER`, `TEXT`, `BOOLEAN`). Two column constraints (`NOT NULL`, `PRIMARY KEY`). Anonymous `?` placeholders. Anything else — `JOIN`, `GROUP BY`, `ORDER BY`, `LIMIT`, `UPDATE`, `DELETE`, `ALTER`, `AND`/`OR` in WHERE, comparison operators other than `=` — is recognized and rejected with `ErrUnsupportedSQL` and a clear message naming the feature.

Documented in [ADR-0015](../adr/0015-sql-grammar-scope.md) and walked through in [chapter 09](../book/09-milestone-7-sql-parser.md).

### `internal/planner` — AST → executable plan

Five plan types (`CreateTablePlan`, `InsertPlan`, `TableScanPlan`, `PrimaryKeyLookupPlan`, `ProjectionPlan`) and a `Planner` that consults the catalog for schema validation:

- `planner.New(catalog) *Planner`.
- `planner.Plan(stmt) (Plan, error)` — dispatches on statement kind. Resolves table + column names; enforces v0.1 limitations (single INTEGER PK, WHERE only on the primary key); rejects unknown tables/columns up front so the executor never sees an invalid plan.

`SELECT *` produces a bare `TableScanPlan` (no wrapping projection). Named columns wrap with `ProjectionPlan`. WHERE on non-PK columns returns `ErrWhereOnlyPrimaryKey` — the parser is permissive, the planner narrows.

### `internal/exec` — runs plans against the catalog + btree

Two entry points:

- `executor.Run(plan, args, sqlSrc) Result` — for CreateTable + Insert. Returns `Result{RowsAffected, LastInsertID}`.
- `executor.RunQuery(plan, args) *Rows` — for TableScan, PKLookup, Projection. Returns a materialized `Rows{Columns, Values}`.

Parameter binding follows strict rules: `int/int32/int64 → KindInteger`, `string → KindText`, `bool → KindBoolean`, `nil → KindNull`. No implicit conversions. `bindArgs` consumes `?` placeholders in occurrence order.

After every INSERT, the executor compares `tree.RootPageID()` to the catalog's stored root; on drift, calls `catalog.SetTableRoot` which now (via [ADR-0018](../adr/0018-btree-update-cell-same-size.md)) actually persists. Rows materialization is documented in [ADR-0016](../adr/0016-rows-materialization.md); streaming arrives in v0.2.

### `pkg/godb` — the public Go API

The stable surface application code imports:

- `godb.Open(path, opts...) (*DB, error)`, `db.Close()`.
- `db.Exec(ctx, sql, args...) (Result, error)` — CREATE / INSERT.
- `db.Query(ctx, sql, args...) (*Rows, error)` — SELECT.
- `rows.Next() bool`, `rows.Scan(dest...) error`, `rows.Columns()`, `rows.Err()`, `rows.Close()`.
- `db.Begin(ctx) (*Tx, error)` — always returns `ErrTransactionsUnsupported` in v0.1 (see [ADR-0017](../adr/0017-no-transactions-in-v0-1.md)).
- 17 exported sentinel errors so callers dispatch via `errors.Is(err, godb.ErrXxx)`.

Walked through end-to-end in [chapter 10](../book/10-milestone-8-public-api.md). Full tutorial in [embedded-api.md](embedded-api.md).

### `internal/buffer`, `internal/tx`, `internal/engine`, `pkg/driver`

Empty placeholders. The buffer pool and transactions arrive in v0.2; the `database/sql/driver` wrapper at M9; `internal/engine` may be removed if it remains unused by M11.

## What is *not* yet usable

A short and honest list:

- **No transactions.** `db.Begin` returns `ErrTransactionsUnsupported`; writes are autocommit-only. v0.2 with the rollback journal closes this.
- **No `UPDATE` / `DELETE` / `ALTER TABLE` / `DROP TABLE`.** Parser + planner explicitly reject. v0.2+.
- **No `JOIN` / `GROUP BY` / `ORDER BY` / `LIMIT` / `HAVING`.** v0.3+.
- **No non-primary-key `WHERE`.** Planner returns `ErrWhereOnlyPrimaryKey`. v0.2 adds `TableScan + Filter`.
- **No compound `WHERE` with `AND` / `OR`.** Parser rejects.
- **No comparison operators other than `=`.** v0.2.
- **No CLI subcommands.** `./godb` prints a banner and exits; M10 adds `exec`, `query`, `inspect`, `check`, the interactive shell.
- **No `database/sql/driver` wrapper.** M9 adds it; until then, use `pkg/godb` directly.
- **No buffer pool.** Every read/write hits disk through the pager. v0.2.
- **No streaming Rows.** Result sets are materialized in memory. v0.2 with the buffer pool + btree cursor.
- **No prepared statements.** Every Exec/Query re-parses. v0.2 if there's a real need.
- **No implicit Scan conversions.** Strict types in v0.1; `database/sql`-style coercions could come in v0.2+.
- **No secondary indexes, no foreign keys, no `UNIQUE` / `CHECK` / `DEFAULT` / `REFERENCES`.** v0.2+ and later.

## How you actually use it: the public API

The right path is `pkg/godb`. The full tutorial — Open / Exec / Query / Scan, parameter binding rules, scan type rules, error handling, transactions — is in [`embedded-api.md`](embedded-api.md). The 40-line "create + insert + close + reopen + query" example at the bottom of that doc is the demo to run if you want to verify the engine works on your machine.

## For the curious: the internal layers, end-to-end

This snippet does the same thing as the embedded API tutorial but calls into `internal/` packages directly. **Not a recommendation for production use** — internals can change without warning between milestones — but a useful map for readers who want to see how the layers compose.

```go
package main

import (
    "errors"
    "fmt"
    "log"

    "github.com/felipegalante/godb/internal/btree"
    "github.com/felipegalante/godb/internal/catalog"
    "github.com/felipegalante/godb/internal/record"
    "github.com/felipegalante/godb/internal/sql"
    "github.com/felipegalante/godb/internal/storage"
)

func main() {
    // 1. Open (or create) the database file.
    pager, err := storage.OpenPager("demo.godb", storage.PagerOptions{CreateIfMissing: true})
    if err != nil {
        log.Fatal(err)
    }
    defer pager.Close()

    // 2. Open the catalog. On a fresh database this allocates the
    //    catalog's own B+tree and stashes its root id in the database
    //    header. On an existing one it rebuilds the in-memory name
    //    index from the on-disk catalog tree.
    cat, err := catalog.Open(pager)
    if err != nil {
        log.Fatal(err)
    }

    // 3. Parse a SQL CREATE TABLE into an AST, convert it to a
    //    record.Schema, and register the table. If the table already
    //    exists from a prior run, LookupTable finds it.
    const createSQL = "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, active BOOLEAN);"
    stmt, err := sql.Parse(createSQL)
    if err != nil {
        log.Fatal(err)
    }
    ct := stmt.(*sql.CreateTableStatement)
    schema := sql.ColumnDefsToSchema(ct.Columns)

    info, err := cat.LookupTable(ct.Name)
    if errors.Is(err, catalog.ErrTableNotFound) {
        info, err = cat.CreateTable(ct.Name, schema, createSQL)
    }
    if err != nil {
        log.Fatal(err)
    }

    // 4. Open the table's B+tree by the root page id the catalog
    //    handed back. Insert a few rows. Encode via record.EncodeRow;
    //    the cell's key is the INTEGER PRIMARY KEY (the row's id).
    tree := btree.Open(pager, info.RootPageID)
    rows := []struct {
        id     int64
        name   string
        active bool
    }{
        {1, "Felipe", true},
        {2, "MG", true},
        {3, "Jane", false},
    }
    for _, r := range rows {
        values := []record.Value{record.Int(r.id), record.Text(r.name), record.Bool(r.active)}
        if err := schema.Validate(values); err != nil {
            log.Fatal(err)
        }
        payload, _ := record.EncodeRow(values)
        if err := tree.Insert(uint64(r.id), payload); err != nil && !errors.Is(err, btree.ErrDuplicateKey) {
            log.Fatal(err)
        }
    }

    // 5. Sync the catalog (which also flushes the pager).
    if err := cat.Sync(); err != nil {
        log.Fatal(err)
    }

    // 6. Read back. Tree.Scan walks every leaf in key order.
    fmt.Println("id | name   | active")
    fmt.Println("---+--------+--------")
    _ = tree.Scan(func(k uint64, payload []byte) error {
        values, _, err := record.DecodeRow(payload)
        if err != nil {
            return err
        }
        fmt.Printf("%-2d | %-6s | %v\n", values[0].Int, values[1].Text, values[2].Bool)
        return nil
    })

    // 7. Validate the whole tree — slotted-page invariants, key
    //    ordering, equal leaf depth. Useful in tests and tools.
    if err := tree.Validate(); err != nil {
        log.Fatal(err)
    }
}
```

What this snippet shows:

- The seven internal layers (storage / record / btree / catalog / sql / planner / exec) compose without glue. The public `pkg/godb.DB.Exec`/`Query` does the same orchestration, just hidden behind a simpler surface.
- The `Tree.Scan` callback is invariant under splits: even if `Insert` had triggered ten splits between step 4 and step 6, `Scan` would still yield every row in key order via the leaf chain.
- Adding a `posts` table is one more parse + `cat.CreateTable("posts", ...)` call. Each table gets its own B+tree, all in the same `.godb` file, all surviving a close/reopen via the catalog.

## What just changed

The most recent milestone is **M8 — public Go API + planner + executor**. Chapter to read: [chapter 10](../book/10-milestone-8-public-api.md). The engine now exposes a stable Go API: `godb.Open` → `db.Exec`/`db.Query` → `Rows.Next`/`Scan`, end-to-end. The planner ([`internal/planner`](../../internal/planner/)) validates SQL against the catalog and produces typed plans; the executor ([`internal/exec`](../../internal/exec/)) runs them.

M8 also closed the M6 SetTableRoot persistence gap via a small `btree.UpdateCellSameSize` primitive ([ADR-0018](../adr/0018-btree-update-cell-same-size.md)). Table trees that grow new roots via splits now persist their new RootPageID — so a table you insert 10,000 rows into survives close/reopen correctly.

Three new ADRs landed alongside the chapter: [ADR-0016](../adr/0016-rows-materialization.md) (rows are materialized in v0.1), [ADR-0017](../adr/0017-no-transactions-in-v0-1.md) (Begin returns ErrTransactionsUnsupported), [ADR-0018](../adr/0018-btree-update-cell-same-size.md).

## What's next

**M9 — polish + `database/sql/driver` + integration tests.** Optional `sql.Open("godb", path)` wrapper so users can plug GoDB into the broader `database/sql` ecosystem; multi-table integration scenarios; better error context (wrap with source SQL + statement number for debugging). Sets up M10 (CLI) and M11 (v0.1 release).

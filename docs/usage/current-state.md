# Current state (pre-alpha, as of M5)

An honest snapshot of what GoDB can and can't do right now, refreshed every milestone. This page exists so a reader doesn't have to scan commit history or trial-and-error their way into the API surface.

If you're new here, read [`README.md`](README.md) in this directory first. It frames the project status; this page goes one level deeper.

## What works internally

Five internal packages do real work today. None of them are exposed via `pkg/godb` yet (M8). All of them are exercised by tests under `make test` and `make race`.

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

### `internal/buffer`, `internal/catalog`, `internal/sql`, `internal/planner`, `internal/exec`, `internal/tx`, `internal/engine`, `pkg/godb`, `pkg/driver`

All empty placeholders with `.gitkeep` files. They get filled in by future milestones in roughly that order.

## What is *not* yet usable

A short and honest list:

- **No public Go API.** `pkg/godb` is empty. Importing the engine requires forking or a `replace` directive against a local clone.
- **No SQL.** No lexer, no parser, no `CREATE TABLE` / `INSERT` / `SELECT`.
- **No CLI subcommands.** `./godb` prints a banner and exits.
- **No multi-table support.** A database has exactly one B+tree until M6's catalog.
- **No transactions, no rollback, no atomic splits.** A crash mid-Insert can leave the tree inconsistent. v0.2's rollback journal closes this.
- **No deletion, no update in place.** v0.2.
- **No buffer pool.** Every read/write hits disk through the pager directly. v0.2.
- **No secondary indexes, no foreign keys, no constraints beyond per-column type/nullability.** v0.2+ and later.

## Educational tour: an end-to-end loop, today

For someone reading the code who wants a tangible "the whole engine working" example, this is the smallest end-to-end snippet. **Not a recommendation to use GoDB this way in production** — internals can change between milestones — but a useful map of where each package fits.

```go
package main

import (
    "errors"
    "fmt"
    "log"

    "github.com/felipegalante/godb/internal/btree"
    "github.com/felipegalante/godb/internal/record"
    "github.com/felipegalante/godb/internal/storage"
)

func main() {
    // 1. Open (or create) the database file.
    pager, err := storage.OpenPager("demo.godb", storage.PagerOptions{CreateIfMissing: true})
    if err != nil {
        log.Fatal(err)
    }
    defer pager.Close()

    // 2. Schema for typed validation. The codec doesn't enforce this;
    //    we do, before encoding.
    schema := &record.Schema{Columns: []record.Column{
        {Name: "id", Kind: record.KindInteger, NotNull: true, PrimaryKey: true, Position: 0},
        {Name: "name", Kind: record.KindText, NotNull: true, Position: 1},
        {Name: "active", Kind: record.KindBoolean, Position: 2},
    }}

    // 3. Tree handle. Create() allocates a fresh empty leaf as the
    //    root. SetCatalogRoot persists the root id in the database
    //    header so a future reopen can find it.
    var tree *btree.Tree
    if root := pager.Header().CatalogRootPageID; root == 0 {
        tree, err = btree.Create(pager)
        if err != nil {
            log.Fatal(err)
        }
        if err := pager.SetCatalogRoot(tree.RootPageID()); err != nil {
            log.Fatal(err)
        }
    } else {
        tree = btree.Open(pager, root)
    }

    // 4. Insert a few rows. Encode via record.EncodeRow; the cell's
    //    key is the INTEGER PRIMARY KEY (the row's id).
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

    // 5. Persist the new root (in case splits changed it) and flush.
    pager.SetCatalogRoot(tree.RootPageID())
    pager.Sync()

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

- The four layers (storage / record / btree / your code) compose without help from any glue not in the engine.
- Persistence works: kill the program, re-run it, and step 3 finds the existing root via `CatalogRootPageID` and `btree.Open`s it.
- The `Tree.Scan` callback is invariant under splits: even if `Insert` had triggered ten splits between step 4 and step 6, `Scan` would still yield every row in key order via the leaf chain.

## What just changed

The most recent milestone is **M5 — multi-page B+tree (splits + descent + height growth)**. Chapter to read: [chapter 07](../book/07-milestone-5-multi-page-btree.md). What this means for users (well, future users) is that the engine now scales beyond ~149 rows — the M4 single-leaf ceiling — to essentially as many rows as fit on disk. Tree height grows automatically; the API doesn't change.

## What's next

**M6 — catalog.** A meta-B+tree mapping table names to root page ids + column schemas. This is the milestone where "one tree per database" stops being true. After M6 this page gets a new section showing `catalog.Create("users", schema)` and `catalog.Lookup("users")` and how those compose with `btree.Tree`.

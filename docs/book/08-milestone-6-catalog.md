# Chapter 08 — The Catalog: Many Named Tables (M6)

## Where we are

By the end of [Chapter 07](07-milestone-5-multi-page-btree.md) the engine could store ~10,000 rows in a single B+tree that survives a Close/reopen cycle with its full structural invariants intact. There was one tree per database. If you wanted "a users table *and* a posts table," you couldn't — the storage layer didn't know what a "table" was. It just knew about cells in pages and trees of pages.

M6 changes that. We introduce the **catalog**: a small metadata layer that knows the names, root page ids, schemas, and original `CREATE TABLE` statements of every table the database holds. The catalog is itself just another B+tree (yes, really — that's the whole trick). What makes it *the* catalog is one privileged slot: its root page id lives in the database header's `CatalogRootPageID` field, which has been reserved since M1 and waiting for this milestone.

After M6 lands, you can write code that says "create a `users` table with this schema, create a `posts` table with that one, close the database, reopen it, look up `users`, get its tree, read its rows" — and it works. The SQL layer (M7+) and the public Go API (M8) will sit on top of exactly this surface.

## Foundation

### What metadata is, and why every relational engine has it

A relational database stores two kinds of bytes on disk: **user data** (the rows the application cares about) and **metadata** (the schema, table names, index definitions, and other "data about the data"). Without metadata, the engine has no way to answer "what's this byte for?" — every read would be a guess. Postgres has its `pg_catalog` schema. MySQL has `information_schema`. SQLite has `sqlite_schema` (formerly `sqlite_master`). GoDB has `internal/catalog`.

Mechanically, metadata is just more rows. SQLite's schema table is literally a SQLite table, queryable like any other. Postgres's `pg_catalog.pg_class` is a regular Postgres relation. The recursive shape — "the catalog is itself a table" — is universal because once you have a working table-storage abstraction, why build a second one for metadata?

GoDB inherits this. The catalog stores one *object* per registered table, where an object is a small struct containing:

```go
type Object struct {
    Type       ObjectType    // = ObjectTypeTable (1) for M6
    Name       string        // e.g. "users"
    RootPageID storage.PageID // where this table's own B+tree lives
    SQL        string        // the CREATE TABLE statement, optional
    Schema     record.Schema // column list (reused from internal/record)
}
```

The catalog's job is to encode these objects into B+tree cells, look them up by name, and survive a Close/reopen.

### The bootstrap problem

Every B+tree the database holds going forward records its root page id *inside the catalog*. The catalog tree itself can't record its own root id inside itself — that's circular. So the catalog's root id needs a privileged home outside the catalog: somewhere the engine reads first, on every open, before anything else.

That home is the **database header**. Specifically, the 8 bytes at offset 20 of page 0, exposed via `pager.Header().CatalogRootPageID` (read) and `pager.SetCatalogRoot(id)` (write). This field has existed since M1 — reserved, sitting at 0 on every fresh database, with a doc comment that quietly noted "the catalog will use this someday." M6 is "someday."

On `catalog.Open(pager)`:

- If `Header.CatalogRootPageID == 0`: the database has never had a catalog. Allocate a fresh empty B+tree via `btree.Create(pager)`. Call `pager.SetCatalogRoot(tree.RootPageID())`. Initialize the in-memory cache to empty and `nextID` to 1.
- If `Header.CatalogRootPageID != 0`: open the existing tree via `btree.Open`, scan every cell, decode each into an `Object`, populate the in-memory cache, and set `nextID = max(seen object ids) + 1`.

Two pages worth of work on a fresh open; one tree-scan worth of work on subsequent opens. That's the entire bootstrap.

### Pre-M6 `.godb` files

Between M4 and M6, `Header.CatalogRootPageID` had a *placeholder* meaning — the M4 plan called it "the application's single primary tree root id," because M4 had no real catalog yet but did need a way to remember a Tree's root across opens. A pre-M6 `.godb` file therefore has a non-zero `CatalogRootPageID` pointing at a regular table leaf, not a catalog tree.

If M6 code blindly trusts the field, it tries to walk the regular leaf as if it were a catalog tree. The cells are `record.EncodeRow` payloads, not `EncodeObject` payloads — and decode goes off the rails.

The catalog's encoding starts with a **2-byte magic prefix** specifically to fence this case: `0xCA 0x01`. A `record`-encoded row starts with `0x01` (row version), which would slip past a single-byte version check and fail downstream with a confusing truncation error. The high-bit-set magic byte `0xCA` is distinct from any leading byte `record.EncodeRow` produces, so `DecodeObject` returns `ErrUnsupportedCatalogVersion` immediately on a pre-M6 file. See [ADR-0014](../adr/0014-catalog-row-encoding.md) and the test [`TestOpenRejectsPreM6FileWithRegularTreeAtCatalogRoot`](../../internal/catalog/catalog_test.go) for the worked example.

We deliberately do not write a migration path. GoDB is pre-alpha. The fence catches pre-M6 files cleanly with a clear error; users discard those files and start over.

### Why the catalog is just another B+tree

The catalog could have been a hand-rolled flat file, a JSON sidecar, a fixed allocation in the header, or any number of other things. It's a B+tree because:

- Every layer above storage already understands B+trees. No new index structure to build, test, or document.
- Lookups by object id (the catalog's natural key) become O(log n) for free, even though M6 in practice has a very small number of tables.
- Inserts (CreateTable) scale naturally as the catalog grows; the same split logic from M5 keeps the catalog tree balanced.
- "The catalog is recursive" is a strong design point. Once B+trees store sorted-key data, the catalog shows that *all* data — even metadata — can be stored in B+trees.

The only structural difference from a regular table tree is **what's in the cell payloads**: catalog rows (encoded `Object`s) instead of user rows. The btree layer doesn't know or care.

## Decisions

- **Custom binary encoding for catalog rows**, not `record.EncodeRow`. The variable-length column list doesn't fit a flat-row shape cleanly. See [ADR-0014](../adr/0014-catalog-row-encoding.md).
- **Two-byte magic+version prefix** (`0xCA 0x01`) fences against pre-M6 `.godb` files where `CatalogRootPageID` pointed at a regular leaf. Single-byte version was insufficient because `record.rowVersion = 0x01` collided.
- **In-memory `map[string]*TableInfo` cache** is the source of truth for name lookups. Mutations write through to both the tree and the cache. Opens rebuild the cache from the tree.
- **Object ids are monotonically increasing, never reused.** On open, `nextID = max(seen ids) + 1`. Keeps the door open for v0.2's deletion + journal story to ship without reusing ids some other pointer might cache.
- **No back-compat with pre-M6 files.** Pre-alpha state; the fence catches and rejects, users discard old files.
- **`SetTableRoot` is in-memory only in M6.** Persisting it would require a btree-level `UpdateCell` primitive (no such thing today), which is out of scope. The v0.2 journal + UpdateCell story closes this before M9 (the executor) starts mutating table trees and producing real root drift.
- **No `DropTable`, `RenameTable`, `AlterTable`, secondary indexes, or atomic catalog mutations** in M6. Each has a future milestone home.

## The code

The M6 surface is small. Two new packages, one new ADR, one new chapter.

- [`internal/catalog/catalog.go`](../../internal/catalog/catalog.go) — the `Catalog` type, `Open`, `CreateTable`, `LookupTable`, `ListTables`, `SetTableRoot`, `Sync`. ~250 lines.
- [`internal/catalog/codec.go`](../../internal/catalog/codec.go) — the row codec with the 2-byte magic+version prefix. ~200 lines.
- [`internal/catalog/errors.go`](../../internal/catalog/errors.go) — the typed errors.

### `Open` and the bootstrap

```go
func Open(pager *storage.Pager) (*Catalog, error) {
    rootID := pager.Header().CatalogRootPageID
    c := &Catalog{pager: pager, byName: map[string]*TableInfo{}, nextID: 1}
    if rootID == 0 {
        // Fresh: allocate the catalog's tree, stash root in header.
        tree, _ := btree.Create(pager)
        c.tree = tree
        pager.SetCatalogRoot(tree.RootPageID())
        return c, nil
    }
    // Existing: open the tree, walk it, rebuild cache.
    c.tree = btree.Open(pager, rootID)
    c.tree.Scan(func(key uint64, payload []byte) error {
        obj, err := DecodeObject(payload)
        if err != nil { return err }
        c.byName[obj.Name] = &TableInfo{
            ID: key, Name: obj.Name, RootPageID: obj.RootPageID,
            SQL: obj.SQL, Schema: obj.Schema,
        }
        if key >= c.nextID { c.nextID = key + 1 }
        return nil
    })
    return c, nil
}
```

(Real code has error checks and the nil-pager guard; this is the shape.) The whole story of "find the catalog" reduces to one header read.

### `CreateTable`

```go
func (c *Catalog) CreateTable(name string, schema record.Schema, sql string) (*TableInfo, error) {
    if err := validateName(name); err != nil { return nil, err }
    if _, exists := c.byName[name]; exists { return nil, ErrTableExists }

    tableTree, _ := btree.Create(c.pager)         // allocate the table's own tree
    obj := &Object{
        Type: ObjectTypeTable, Name: name,
        RootPageID: tableTree.RootPageID(),
        SQL: sql, Schema: schema,
    }
    payload, _ := EncodeObject(obj)
    id := c.nextID
    c.tree.Insert(id, payload)                     // insert into catalog tree
    c.pager.SetCatalogRoot(c.tree.RootPageID())    // re-persist root (may have grown)
    info := &TableInfo{ID: id, ...}                // populate
    c.byName[name] = info
    c.nextID++
    return info, nil
}
```

Two page allocations: one for the new table's empty leaf, one for the catalog row's cell (which may or may not split the catalog tree). The `SetCatalogRoot` call at the end is the safety net: if the catalog's own root grew via a root split during `c.tree.Insert`, the header now points at the new root.

### The catalog row codec

The encoding is documented in detail in [ADR-0014](../adr/0014-catalog-row-encoding.md). The decoder's first six bytes look like:

```
0xCA       <- catalog magic
0x01       <- catalog format version
0x01       <- ObjectTypeTable
0x05       <- uvarint name length = 5
'u' 's' 'e' 'r' 's'   <- name bytes
...
```

`DecodeObject` rejects mismatch on either prefix byte with `ErrUnsupportedCatalogVersion`. The magic byte (`0xCA`) is deliberately chosen to be distinct from `record.rowVersion = 0x01`, so pre-M6 `.godb` files (which have record-encoded rows where the catalog now lives) fail cleanly.

## Tests as proof

The 23 tests live in [`internal/catalog/catalog_test.go`](../../internal/catalog/catalog_test.go) and [`internal/catalog/codec_test.go`](../../internal/catalog/codec_test.go). A few worth pointing at specifically:

- **`TestPersistAcrossReopen`** creates three tables in a fresh database, captures their assigned IDs and root page ids, syncs, closes, reopens with a *fresh* pager, and asserts every table comes back identically — same ID, same root, same schema, same SQL. End-to-end M1+M2+M3+M4+M5+M6.
- **`TestOpenRejectsPreM6FileWithRegularTreeAtCatalogRoot`** is the magic-byte fence test. It manually constructs a pre-M6-style `.godb` file (a regular table leaf with a `record.EncodeRow` payload, with `CatalogRootPageID` pointing at it) and asserts `catalog.Open` returns `ErrUnsupportedCatalogVersion` rather than crashing or decoding garbage.
- **`TestDecodeRejectsBadMagic`** isolates the same fence at the codec level: a single byte set to `0x01` at offset 0 of an otherwise-valid catalog row triggers `ErrUnsupportedCatalogVersion`.
- **`TestCreateTableRejectsDuplicateName`** pins the uniqueness invariant: a second `CreateTable` with the same name returns `ErrTableExists` *and* leaves catalog state unchanged (same `ListTables` count, same original schema).
- **`TestNextIDSurvivesReopenAndExtends`** verifies that `nextID` is correctly recomputed on reopen and that the next `CreateTable` issues the expected id — no off-by-one, no id reuse.

## What this layer cannot do yet

- **No `DropTable`, `RenameTable`, `AlterTable`.** v0.2 territory. The catalog's API stays append-only in v0.1.
- **No secondary indexes.** `ObjectTypeIndex` is reserved in the enum but never written; v0.2.
- **No atomic catalog mutations.** A crash mid-`CreateTable` (between allocating the table's leaf and inserting the catalog row) orphans the table's leaf but leaves the name free. Acceptable in v0.1; v0.2's rollback journal closes the gap.
- **`SetTableRoot` is in-memory only.** Persisting it requires a btree-level `UpdateCell` primitive that doesn't exist yet. Real table-root drift only happens when M5's root-split triggers fire on an inserting table tree, which only happens once M9 (the executor) writes through to tables. The persistence story has to land before M9 — it's tracked.
- **No SQL.** M6 takes a `record.Schema` directly. M7's parser will produce schemas from `CREATE TABLE` strings and call `catalog.CreateTable` on the result; the SQL string handed to M6 today is just bookkeeping (a record of intent for future `inspect`/debugging).
- **No public Go API.** `catalog` stays under `internal/`. M8 wraps `catalog` + `btree.Tree` into the `pkg/godb` surface.
- **No CLI commands** for catalog inspection (`godb inspect tables`, `godb describe users`). M10.
- **No buffer pool.** Each catalog operation calls `Pager.ReadPage`/`WritePage` directly. v0.2.

## Further reading

- SQLite's [`sqlite_schema`](https://www.sqlite.org/schematab.html) — the same idea executed differently. Worth comparing field-by-field; the conceptual overlap is large.
- Postgres's [`pg_catalog`](https://www.postgresql.org/docs/current/catalogs.html) — a much richer catalog, dozens of tables, all queryable as regular relations. The "everything is a table including the metadata" sensibility taken to its logical extreme.
- Joe Celko's *SQL for Smarties* has a chapter on the information schema standard for the language-level take on what a catalog should expose.

## Where the next chapter picks up

You can now declare multiple named tables, retrieve their schemas, and persist the lot across a database close/reopen. What you cannot do is *talk to* the catalog using SQL.

M7 (the next milestone) builds the SQL lexer and parser. The deliverable is a recursive-descent parser that accepts a tiny grammar — `CREATE TABLE`, `INSERT`, `SELECT` with `WHERE id = ?` — and produces an AST. No execution yet (that's M9). The chapter will cover how the lexer tokenizes a stream of bytes, how the parser turns tokens into an AST, and why the supported grammar is deliberately small (see [ADR-0004](../adr/0004-no-sqlite-compatibility.md) — no SQLite dialect compatibility).

By the end of M7 the engine can parse SQL but not run it. By the end of M9 (executor) the loop closes: SQL string → AST → plan → catalog lookup → tree operation → results.

That's where the next chapter picks up.

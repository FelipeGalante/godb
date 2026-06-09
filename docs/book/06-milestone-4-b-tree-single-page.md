# Chapter 06 — The Smallest B+tree: One Leaf, One Root (M4)

## Where we are

By the end of [Chapter 05](05-milestone-3-slotted-pages.md) we can pack many typed rows into a single 4 KB page, find them by key in O(log n) via the cell directory, and tell the caller "the page is full" when no more cells fit. Everything we have so far operates on *one page*. A real database is not one page — it's many. A table that grows past ~50 rows blows past `ErrPageFull` and we have nowhere to put the next row.

The standard answer is a **B+tree**: a tree of pages where the leaves hold the data, the internal nodes hold separator keys and child pointers, and the search for a key walks from the root down to a single leaf in O(log n) total page reads. Established engines (SQLite, Postgres, MySQL, etc.) all use B+trees as their primary index structure. We are going to build one too.

But not all of one today. M4 builds the **smallest meaningful B+tree**: a tree whose root *is* its only leaf. Height zero. When the leaf fills, `Insert` returns `ErrPageFull` — we do not yet split. That's it.

This sounds underwhelming until you notice what it gives us:

- A `Tree` type with the *exact* API a multi-page tree will have: `Create`, `Open`, `Insert`, `Get`, `Scan`, `Validate`, `RootPageID`. M5 changes the implementation, not the surface.
- A persisted root page id (the first real use of the database header's `CatalogRootPageID` field), so a tree survives Close/reopen.
- An end-to-end "create a database file, put a typed row in, find it again after restarting" demo. M4 is the first milestone you can show a friend without footnotes.

By the end of M4 the engine looks, from the outside, like a tiny embedded database. Internally we know the index is one leaf wide and a single insert past capacity will fail loudly. M5 closes that gap.

## Foundation

### B-trees, in one section

A **B-tree** is a balanced ordered tree where each node holds many keys (not just one or two), each node points to many children (not just two), and the tree's height grows very slowly with the number of keys. The "B" is Boeing or Bayer or Big-or-balanced depending on which oral history you believe — what matters is the *shape*.

Picture a binary search tree: each node has one key, at most two children, and a tree holding *n* keys has depth around log₂(n). A B-tree generalizes this: each node holds up to *m-1* keys (for some branching factor *m*, typically in the hundreds for a real database), each node has up to *m* children, and the depth is log_m(n). For *m=400* and *n=1,000,000* rows, the depth is around 3 — three page reads to find any row in a million. That's the magic.

Why "hundreds"? Because each node *is one page on disk*. The fan-out *m* is roughly "how many separator keys + child pointers fit in a page." Cramming the fan-out high means the tree is shallow, which means lookups touch fewer pages, which means fewer disk reads.

A **B+tree** is a B-tree with one specialization: only the leaves hold data. The internal nodes hold separator keys and pointers, nothing else. This has two practical benefits:

- Internal nodes are smaller, so fan-out is even higher (more separators per page).
- All data lives at the leaf level, so a full range scan is a single linked-list walk across leaves (via sibling pointers) — you don't have to descend the tree for every row.

GoDB uses B+trees. So does SQLite, Postgres, MySQL's InnoDB, BoltDB, and roughly every other page-based engine. It's the workhorse data structure for ordered indexed lookup on disk.

### What "height zero" actually means

A B+tree's height is the number of levels above the leaves. A tree with one leaf has height zero: the root *is* the leaf. A tree with two leaves and a single internal node has height one. A million-row tree might have height three or four.

A height-zero tree is a degenerate B+tree — but it's still a B+tree. It supports the same API. It just stops being interesting (no descent, no splits) until the first overflow.

This matters for M4 because we get to build the whole tree-shaped abstraction *without writing any descent or split code yet*. Every operation reads exactly one page (the root, which is also the leaf), does the slotted-page work, and writes back. Total new logic: maybe 80 lines. The hard parts (descent, splits, rebalancing, root growth) all live in M5.

The API we ship in M4 — `Insert(key, payload) error`, `Get(key) ([]byte, bool, error)`, `Scan(fn) error` — does not change in M5. The implementation gets smarter. The caller doesn't notice.

### The page-full contract becomes a tree-level event

Recall from Chapter 05 that `InsertCell` has a precise contract: on `ErrPageFull`, the page is *unchanged*. We carry that contract up to the Tree:

> `Tree.Insert` on a full leaf returns `ErrPageFull` and leaves the tree unchanged. Every previously-inserted (key, payload) is still retrievable.

In M5 this error becomes the trigger for a leaf split. The Tree catches `ErrPageFull` internally, splits the leaf into two, picks a separator key, and inserts the new key on whichever side it belongs. From the caller's perspective, `Insert` either succeeds or fails for a *different* reason (e.g. disk full). The page-full case becomes the engine's internal mechanic for "the tree is growing."

In M4 we stop just short of that. The caller sees `ErrPageFull` and there's nowhere for it to go. This is fine for v0.1 testing (we can fit a few hundred small rows in one page), and it's a clean contract for M5 to plug into.

### Where does the root page id live?

A tree on disk is a graph of pages. To find any of them you have to start somewhere — and "somewhere" is the **root page id**. If we don't know the root, we can't open the tree.

So we need to persist the root id across opens. There are a few places it could go:

1. **In the database header.** A specific 8-byte field on page 0. Simple, reliable, but it commits us to "one tree per database" — which is exactly what M4 needs, and exactly what M6 will *not* need.
2. **In a dedicated 'roots' page.** A separate metadata page holding root ids for many trees. Flexible, but unnecessary in M4 (we only have one tree).
3. **In the catalog.** Each table's row in the catalog includes its tree's root id. The right long-term answer, but the catalog doesn't exist yet.

For M4 (and M5), option 1 wins by being trivial. The database header already has a `CatalogRootPageID` field — reserved in M1, sitting at 0 since then. We start writing to it. The field's *name* is forward-looking ("catalog root") because that's what it will be in M6, but right now it stores the application's single primary-tree root. Pre-M6 it's a slight misnomer; we accept that to avoid changing the header layout twice.

The Tree itself, though, stays agnostic. `Create` and `Open` take a pager and (for Open) a root id, and the Tree does not know or care where that id is persisted. Callers are responsible for handing it back on the next open. In M4 the test code and the smoke script do that explicitly via the new `Pager.SetCatalogRoot(id)` method. In M6 the catalog will own that bookkeeping.

This separation matters: it means the Tree implementation isn't entangled with header bookkeeping, so the M6 change (catalog owns root ids) doesn't have to rewrite the Tree.

## Decisions

- **The Tree wraps one root page id, owned externally.** `Create` allocates a page and returns a Tree. `Open` takes (pager, rootID) — no I/O on Open, validation happens lazily on the first operation. The Tree's `RootPageID()` accessor lets callers persist the id wherever makes sense.
- **`Pager.CatalogRootPageID` is dual-purpose pre-catalog.** A new `Pager.SetCatalogRoot(id)` method writes the field; the doc comment on the field explains that v0.1 uses it as the primary tree root and M6 will tighten the semantics. The on-disk layout doesn't change between M4 and M6 — only the meaning of the field does.
- **`ErrPageFull` surfaces directly from `Tree.Insert`.** No split, no recovery. M5 catches the error inside the Tree and splits. We intentionally leave the M4-shaped error contract in place so the M5 plan has something concrete to hook onto.
- **No Tree-level mutex.** The Pager already serializes its mutating methods. Each Tree operation calls one or two Pager methods. The single-writer guarantee is sufficient for v0.1.
- **`Open` does no I/O.** Lazy. A wrong rootID surfaces as `ErrNotLeaf` (the page's type byte isn't a leaf) or a storage error on the first real operation. This keeps `Open` cheap and matches the "constructor returns; first method call does work" pattern most Go users expect.

None of these are large enough to need an ADR. They're documented in code comments and in this chapter.

## The code

The whole M4 implementation is in two new files:

- [`internal/btree/tree.go`](../../internal/btree/tree.go) — the `Tree` type and its operations (~110 lines).
- [`internal/btree/tree_test.go`](../../internal/btree/tree_test.go) — 12 tests covering every public method and the persistence end-to-end (~330 lines).

Plus one small addition in the storage layer:

- [`internal/storage/pager.go`](../../internal/storage/pager.go) — the new `SetCatalogRoot` method (10 lines), and the updated doc comment on `Header.CatalogRootPageID`.

### `Tree` itself

```go
type Tree struct {
    pager  *storage.Pager
    rootID storage.PageID
}
```

Two fields. No state. No cache. Every operation reads the root page from the pager fresh, does the slotted-page work, writes back on mutation.

The constructors:

```go
func Create(pager *storage.Pager) (*Tree, error) {
    pg, _ := pager.AllocatePage(storage.PageTypeTableLeaf)
    InitLeaf(pg)
    pager.WritePage(pg)
    return &Tree{pager: pager, rootID: pg.ID}, nil
}

func Open(pager *storage.Pager, rootID storage.PageID) *Tree {
    return &Tree{pager: pager, rootID: rootID}
}
```

(Real code has error checks; this is the shape.) `Create` does I/O (allocate, init, write). `Open` does none.

### `Insert`, in full

```go
func (t *Tree) Insert(key uint64, payload []byte) error {
    pg, err := t.pager.ReadPage(t.rootID)
    if err != nil {
        return fmt.Errorf("btree.Tree.Insert: read root: %w", err)
    }
    if err := InsertCell(pg, key, payload); err != nil {
        return err
    }
    if err := t.pager.WritePage(pg); err != nil {
        return fmt.Errorf("btree.Tree.Insert: write root: %w", err)
    }
    return nil
}
```

Read the root. Try to insert into it (`btree.InsertCell` from M3 — the slotted-page work). If insertion succeeded, write the page back. If it failed for *any* reason (duplicate, page full, oversized), the page is unchanged (per M3's contract) so there's nothing to roll back; we just return the error.

This is exactly the shape M5's Insert will start with. The difference: M5 will read the root, *descend through internal pages* to find the right leaf, and try to insert into that leaf. On `ErrPageFull`, M5 will split the leaf and (recursively) propagate. M4's Insert is what you get if you remove the descent and the split and the propagation. The skeleton is the same.

### `Get`, `Scan`, `Validate`

Each is a similar 5-line wrapper:

- `Get` reads the root, calls `GetCell(pg, key)`, returns.
- `Scan` reads the root, calls `IterateCells(pg, fn)`, returns.
- `Validate` reads the root, calls `Validate(pg)` (the package-level slotted-page validator from M3), returns.

In M5, `Get` descends to the right leaf; `Scan` walks leaf siblings via `RightSibling`; `Validate` walks every level checking inter-page invariants. Again — same API surface, fancier implementation.

### `Pager.SetCatalogRoot`

```go
func (p *Pager) SetCatalogRoot(id PageID) error {
    p.mu.Lock()
    defer p.mu.Unlock()
    if p.closed {
        return ErrClosed
    }
    p.header.CatalogRootPageID = id
    return nil
}
```

It mutates the in-memory header. Persistence happens at the next `Sync()` or `Close()`, consistent with how `PageCount` already worked (the existing `syncLocked` writes the whole header back). No new flush path.

## Tests as proof

The Tree's 12 tests sit in [`internal/btree/tree_test.go`](../../internal/btree/tree_test.go). A few worth pointing at specifically:

- **`TestTreePersistAcrossReopen`** is the headline test for M4. It opens a fresh `.godb` file, creates a Tree, inserts three M2-encoded rows, calls `pager.SetCatalogRoot(tree.RootPageID())`, closes the pager. Then it opens the same file again with a *fresh* pager, reads `Header().CatalogRootPageID`, opens a new Tree on that root, scans it, decodes each payload back into typed values, and asserts the names come out matching `Felipe / MG / Jane`. End-to-end M1+M2+M3+M4 in one test.
- **`TestInsertReportsPageFullWhenLeafFull`** pins down the M5 contract. It inserts ~500-byte payloads until `Insert` returns `ErrPageFull`, then asserts the tree is still valid and every previously-inserted cell is still retrievable. This is exactly the invariant M5 will rely on when splitting: "the leaf is full, but it's still well-formed."
- **`TestValidateAfterRandomInserts`** is a property-style test: 150 random unique keys inserted in arbitrary order, `Validate` called after each. Anything off-by-one or wrong-sort-position would trip it. Mirrors the equivalent leaf-level test from M3.
- **`TestOpenWrongPageTypeReturnsErrNotLeaf`** verifies that `Open(pager, 0)` (page 0 is the database header, not a leaf) returns `ErrNotLeaf` on the first operation rather than crashing. The lazy validation is intentional.

The pager test for the new method:

- **`TestSetCatalogRootPersistsAcrossReopen`** in [`internal/storage/pager_test.go`](../../internal/storage/pager_test.go) closes the loop on the new pager API: set, close, reopen, value is what we set.

## What this layer cannot do yet

- **No splits.** When the leaf fills, `Insert` returns `ErrPageFull` and leaves you on your own. M5.
- **No internal pages.** The tree is height zero forever in M4. The internal-page cell format ([key: uvarint][child_id: u64] plus a rightmost child pointer) doesn't exist yet. M5.
- **No cross-leaf scans.** `Scan` walks one leaf. The `RightSibling` pointer in the page header is reserved but unused in M4 (one leaf has no siblings). M5 wires it up.
- **No multiple trees in one database.** M4 has *the* tree, singular. Multiple tables means multiple trees means a catalog. M6.
- **No catalog.** `Header.CatalogRootPageID` is doing double duty as the primary-tree root id until M6 brings the real catalog.
- **No deletion.** No `Tree.Delete` because M3 has no `DeleteCell`. v0.2.
- **No update in place.** Same. The pattern will eventually be delete-and-reinsert, and neither end exists yet.
- **No public Go API.** `Tree` is under `internal/btree`. The `godb.DB` wrapper is M8.
- **No SQL, no CLI.** M7 (parser) and M10 (CLI commands).
- **No buffer pool.** Each `Tree.Insert` does a `Pager.ReadPage` (hits disk) plus a `Pager.WritePage` (likewise). v0.2 closes this with a page cache.

Each of these has a milestone home. None of them needs to bleed into M4.

## Further reading

- The SQLite [B-Tree Pages](https://www.sqlite.org/fileformat.html#b_tree_pages) section, which describes a height-zero "table b-tree with a single page" almost exactly the way M4 implements it (their cell format is richer, but the shape is the same).
- Wikipedia's [B+ tree](https://en.wikipedia.org/wiki/B%2B_tree) article — short, decent diagrams of the multi-page case M5 will build.
- CMU 15-445 lecture on tree indexes (typically lecture 10 in the standard schedule). Andy Pavlo walks through insertion, split, and merge with whiteboard examples.
- *Database Internals* by Alex Petrov, chapters 2 and 4. The cleanest written treatment of B+trees in book form.

## Where the next chapter picks up

You can now store many typed rows in a tree, find them by primary key, scan them in order, and reopen the database file later to find them all again. The thing you cannot do is grow past one leaf.

M5 (the next chapter) fixes that. It gives the Tree the ability to **split**: when `InsertCell` returns `ErrPageFull` on the target leaf, the Tree splits the leaf into two leaves, picks a separator key, and inserts that separator (plus a pointer to the new right-hand leaf) into the parent internal page. If the parent overflows, it splits too, propagating up. If the *root* overflows, the Tree creates a new root with the old root and the new sibling as its two children — and the tree's height grows by one.

To do that M5 needs the internal-page cell format (different from the leaf format — it stores child pointers, not row payloads), a descent algorithm (`Insert`/`Get`/`Scan` all start at the root and walk down through internal pages to the right leaf), and a tree-walking `Validate` that checks invariants across levels.

The Tree's API doesn't change — same `Insert / Get / Scan / Validate`. The implementation gets meaningfully larger, but every line of it hangs off contracts you have already seen: `ErrPageFull` from M3 becomes the M5 split trigger; `RightSibling` from the M3 page header becomes the leaf-traversal link for `Scan`; the slotted-page directory becomes the internal node too (just with a different cell format).

That's where the next chapter picks up.

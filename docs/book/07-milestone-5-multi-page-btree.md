# Chapter 07 — The Multi-page B+tree: Splits, Descent, and Growth (M5)

## Where we are

By the end of [Chapter 06](06-milestone-4-b-tree-single-page.md) we had a `Tree` type that worked beautifully — for about 149 small rows. Insert one more and the single root-leaf hit `ErrPageFull`, returned the error to the caller, and gave up. It was a B+tree in the same way a single picture frame is a museum: the right shape, the wrong size.

This chapter is where the tree actually becomes a tree. We're going to add the ability to **split** an overflowing leaf into two, **introduce internal pages** to keep separator keys, **propagate** splits upward when a parent itself overflows, and **grow the root** when even the root has nowhere to push its overflow. The public API (`Insert`, `Get`, `Scan`, `Validate`, `Create`, `Open`, `RootPageID`) doesn't change at all. Every caller that worked at the end of M4 keeps working. What changes is everything beneath the surface.

After M5, the engine supports tables with essentially unbounded row count (limited by disk and the 64-bit `PageID` space). The next milestone (M6 — the catalog) finally builds something *on top* of a real index instead of around a 149-row toy.

## Foundation

### What a split actually is

A B+tree splits a page when it can't fit one more cell. The mechanic is surprisingly local: the overflowing page becomes two pages, each holding roughly half of what the original had, and a single key is handed to the parent as a "separator" that decides which of the two halves a future lookup should descend into.

The local move is obvious enough. The interesting part is what happens to the parent. The parent didn't expect a new child; now it has one extra. Its own cell count goes up by one. If the parent still has room, perfect — write it back. If the parent itself overflows, the parent splits, propagating a new separator one more level up. The chain continues until either a level has room or the *root* runs out of room and the tree's height grows by one. In a tree of a million rows that's *maybe* three or four levels of internal pages above the leaves — splits propagate, but they propagate quickly and stop fast.

What makes this work at the data-structure level is the B+tree's height invariant: every leaf is at the same depth. Splits preserve this invariant for free, because they always operate at one level at a time and growth happens at the top. We don't have to walk down and check; we have to walk *up* from the leaf where the new cell wanted to go and stop wherever the propagation no longer needs to continue.

### Leaf splits versus internal splits

The two split kinds look similar from a thousand feet but differ in one important detail: how the "median" key gets handled.

**Leaf split.** The leaf holds all data; we are not going to lose any of it. So when a leaf splits, the median key is *copied* — it stays in the leaf (specifically, the new right-hand leaf, since the leaf chain is sorted) and a copy is also pushed up to the parent as a separator. The parent now knows: "anything < median lives in old leaf; anything ≥ median lives in new leaf." The data is still all in leaves. ✓

**Internal split.** An internal page holds only separator keys + child pointers. When it splits, the median *separator* gets *pulled up* — it is no longer needed in the internal page itself, because the new parent's role is to be the dividing line between the two halves. The median's child stays with one half (as that half's rightmost child); the original rightmost child stays with the other half.

In code:
- Leaf split: `desired = existingCells + newCell`, sorted. `mid = len(desired) / 2`. `lowerHalf = desired[:mid]`, `upperHalf = desired[mid:]`. Separator sent up = `upperHalf[0].key`.
- Internal split: `cells = existingCells + newCell`, sorted by separator. `mid = len(cells) / 2`. `leftCells = cells[:mid]`, `rightCells = cells[mid+1:]`. Median separator = `cells[mid].separator` (sent up). Median's child becomes the left page's new rightmost-child.

This asymmetry is the whole reason a B+tree is "+": the asymmetric treatment of medians lets us guarantee that all data lives at the leaves and only separators live in internal pages, which keeps internal pages dense (high fan-out → shallow tree → fast lookup).

### The path stack and why we don't trust on-disk parent pointers

When a leaf splits, we have to insert the new separator into the parent. The parent isn't reachable from the leaf — pages on disk don't naturally point at their parents. (The page header has a `Parent` field, but it's debug-only and not trusted because keeping it consistent during splits is more work than the field is worth.)

The solution is a **path stack**: while descending from root to leaf during an Insert, we record each internal page we visit *and which slot we took* (so we know how to update that internal page if the child it pointed at splits). The path is a Go slice of `(pageID, slotIdx)` pairs. After the leaf split, we walk the path from the end (leaf's parent) backward toward the root, inserting and possibly re-splitting at each level.

Why is `slotIdx` necessary, not just `pageID`? Because the parent might have many children, and the split happened on a specific one — we need to know whether to modify a particular cell's separator, insert after a specific cell, or update the rightmost-child pointer. The slot index captures that. (When `slotIdx == CellCount`, that means "we took the rightmost child", a small special case the propagation code handles separately.)

### The leaf chain

In a B+tree, leaves form a singly linked list left-to-right via `RightSibling` pointers in their page headers. The chain lets `Scan` walk every cell in key order with a single descent to the leftmost leaf followed by a flat traversal — no descending the tree once per page.

When a leaf splits, the chain has to be kept consistent. The new right-hand leaf inherits the old leaf's `RightSibling`; the old leaf's `RightSibling` becomes the new leaf's id. If the chain looked like `A → C → 0` before the split (where `0` means "end"), and A splits into A + B, the chain becomes `A → B → C → 0`. Two pointer updates, no cascading.

(In M5 the leaf chain is the only sibling structure that exists. Internal pages don't form a sibling chain — they're navigated top-down through the tree. The `RightSibling` field's other use, "rightmost child of an internal page," is documented in [ADR-0013](../adr/0013-rightsibling-dual-semantics.md).)

### Atomic splits, and why we don't have them

A split touches multiple pages: the original leaf is rewritten, a new leaf is allocated, the parent is rewritten (with a new cell), and possibly the parent's parent, all the way up. If the process crashes between any two of those writes, the on-disk tree is in an inconsistent state. Concretely: the new right-hand leaf may exist as an orphaned page (allocated but not referenced by any parent), or the parent's separator may already point at a leaf that doesn't have the keys it's supposed to.

**M5 does not solve this.** It ships the split logic and accepts the gap. v0.2 will introduce a rollback journal: a separate file recording the original contents of every page about to be modified during a transaction, so that on crash recovery, the page contents can be restored to their pre-transaction state. With a journal, an in-progress split becomes "rolled back to the pre-split state" automatically.

Without a journal, the practical impact is: if you crash mid-split, you may have leaked pages (file grew, some pages unreachable) or worse, an internal page pointing at the wrong child. For v0.1, this is the kind of limitation the release should call out plainly: *we can construct a healthy tree from a healthy starting state, but we can't promise the tree is healthy after a crash*. Don't rely on M5 for crash safety; lean on it for everything else.

## Decisions

- **Median by cell count.** When splitting, the midpoint is chosen by *cell count*, not byte count. For variable-length payloads this can produce slightly unbalanced byte distribution, but cell count is simpler and works well enough. Future tuning could split by byte midpoint or bias splits toward the right for sequential inserts (InnoDB does this for autoincrement-heavy workloads). Documented as a code comment in `splitLeaf` / `splitInternal`; not promoted to an ADR.
- **Path stack instead of on-disk parent pointers.** See above. The `Parent` field stays debug-only.
- **`RightSibling` is dual-purpose.** Same field, two interpretations based on page type. Captured in [ADR-0013](../adr/0013-rightsibling-dual-semantics.md).
- **The Tree does not auto-update `Header.CatalogRootPageID` on root grow.** When `Tree.Insert` grows a new root, `Tree.RootPageID()` reflects the change immediately, but the catalog field is unchanged until the caller explicitly calls `pager.SetCatalogRoot(tree.RootPageID())`. This avoids conflict with M6's catalog-owned root tracking. The trade-off: a crash between an Insert that grew the root and the user's next `SetCatalogRoot` leaves the header pointing at a stale root. The test `TestRootSplitChangesRootID` pins this contract.
- **No atomic splits, deferred to v0.2.** Honest acknowledgement of the gap.

## The code

Three files do the M5 work:

- [`internal/btree/internal.go`](../../internal/btree/internal.go) — internal-page slotted primitives that landed in the prior commit (M5 commit 1). `InitInternal`, `InsertInternalCell`, `FindChild`, `IterateInternalCells`, `RightmostChild`, `SetRightmostChild`, `ValidateInternal`. Mirror the leaf primitives in shape, differ in cell format. Cells encode `[child_id: u64 BE][separator: uvarint]`.
- [`internal/btree/cell.go`](../../internal/btree/cell.go) — extended with `internalCellSize`, `writeInternalCell`, `readInternalCell`, `readInternalCellSeparator`. The leaf cell codec is unchanged.
- [`internal/btree/tree.go`](../../internal/btree/tree.go) — the multi-page Tree. The interesting functions:

### `Tree.Insert`

The whole function fits in ~30 lines. It loops downward from the root, recording a path stack at every internal page, until it hits a leaf. At the leaf it tries `InsertCell`; on success, writes and returns. On `ErrPageFull`, it calls `splitLeaf`, then `propagateSplit` to walk the path back up.

```go
for {
    pg, _ := t.pager.ReadPage(pageID)
    ptype := storage.PageType(pg.Data[0])
    if isLeafType(ptype) {
        err := InsertCell(pg, key, payload)
        if err == nil { return t.pager.WritePage(pg) }
        if !errors.Is(err, ErrPageFull) { return err }
        separator, newRightID, _ := t.splitLeaf(pg, key, payload)
        return t.propagateSplit(path, separator, newRightID)
    }
    slotIdx, childID, _ := findChildWithSlot(pg, key)
    path = append(path, pathEntry{pageID, slotIdx})
    pageID = childID
}
```

### `splitLeaf`

Materializes the desired sorted cell list (existing cells + the new one in its sorted position; payloads are copied because the page memory is about to be reset). Picks the midpoint. Allocates a new leaf for the upper half. Re-inits the original leaf and re-fills it with the lower half. Threads `RightSibling`: new leaf inherits old's right neighbor; old leaf points at new leaf. Returns `(upperHalf[0].key, new_leaf.ID)`.

### `propagateSplit`

Walks the path stack from end to beginning. At each parent, calls `buildParentDesired` to produce the desired `(cells, rightmost)` configuration, then checks `internalCellsFit`. If the parent fits, `rewriteInternalPage` rebuilds it; done. If it doesn't fit, `splitInternal` builds two pages and returns `(newSeparator, newRightInternalID)` — and the loop continues up. If the path empties, `growRoot` creates a new internal page above the previous root.

### `splitInternal`

Picks the median cell. The median's *separator* is what gets pulled up (returned to the caller); the median's *child* becomes the left page's new rightmost-child. The cells before the median go to the left page; the cells after go to a freshly-allocated right internal page. The original rightmost-child becomes the right page's rightmost-child.

### `growRoot`

Allocates a fresh `PageTypeTableInternal`, initializes it with `InitInternal(newRoot, oldRoot, separator, newRightID)` — one cell, two children. Updates `t.rootID`. The caller persists the new root id via `pager.SetCatalogRoot` when convenient.

### `Validate`

Walks the tree recursively, threading a `(lower, upper)` key-range bound down each subtree. At every leaf, asserts all keys are within the bound. At every internal page, derives child bounds from successive separators. Records the depth at which the first leaf is seen and rejects any later leaf at a different depth. Returns the first violation as a `*storage.CorruptionError`. Used by tests after every Insert (or periodically in property tests).

### `Scan`

Descends to the leftmost leaf via `cell[0].child` at each internal page, then walks the `RightSibling` chain to the end. No path stack needed; no per-leaf descent.

## Tests as proof

The M5 test suite roughly doubles the btree package's test count to ~25 tests. The interesting ones are:

- **[`TestInsertManyRandomOrder`](../../internal/btree/tree_test.go)** — inserts 1000 random unique keys, calls `Validate` every 100 inserts, then verifies every key with `Get` and `Scan` ordering. This is the property-style backbone — any latent bug in descent, split, or propagation surfaces quickly.
- **`TestRootSplitChangesRootID`** — explicitly confirms that the root grows (the tree's height increases by one) after enough inserts, and pins the contract that the caller is responsible for `SetCatalogRoot` after Insert.
- **`TestPersistAcrossReopenWithSplits`** — 300-key multi-leaf tree closes, reopens, scans, validates. The end-to-end "real" test of M5.
- **`TestInsertDuplicateAcrossLeavesStillRejected`** — inserts a key, splits the leaf so its home moves to a non-root leaf, tries to insert again — `ErrDuplicateKey`. Pins the global uniqueness invariant.
- **`TestScanStopsOnCallbackErrorAcrossLeaves`** — verifies the iterator's stop-on-error semantics work across the leaf chain.
- **`TestInsertManyInOrder` / `TestInsertManyReverseOrder`** — sequential inserts in both directions, stressing the right-side and left-side split paths respectively.

And the internal-page tests from M5 commit 1 (in [`internal_test.go`](../../internal/btree/internal_test.go)) pin the descent rule itself: `TestFindChildDescendsCorrectly` is a table-driven case for every key-versus-separator relationship.

## What this layer cannot do yet

- **Atomic splits.** A crash mid-split leaves the tree inconsistent. The rollback journal in v0.2 closes this.
- **Deletion / merges / rebalancing.** No `Tree.Delete`. v0.2.
- **Buffer pool.** Every page read/write hits the pager directly. v0.2.
- **Update in place.** No `Tree.Update`. v0.2.
- **Multiple trees per database.** M5 has *the* tree, singular. The catalog (M6) introduces multi-tree.
- **The catalog itself, schemas, table names, SQL.** M6 and beyond.
- **Range scans with an explicit `from / to` key.** `Scan` does a full traversal; key-range scans are an M5+ refinement that hasn't landed.
- **Public Go API.** `Tree` is still under `internal/`. M8.

Each of these has a milestone home. Reading them as "missing" misses the point — they're the next chapters.

## Further reading

- Bayer & McCreight, *Organization and Maintenance of Large Ordered Indexes* (1972). The original B-tree paper. Surprisingly readable; the B+ variant came later (Comer's 1979 survey is a good reference too).
- The SQLite [B-tree module](https://www.sqlite.org/btreemodule.html) overview. Their internal cell format is richer than GoDB's (variable-length keys, overflow chains), but the descent / split / propagation algorithms are the same.
- *Database Internals* (Alex Petrov), chapter 4. The clearest written treatment of split propagation, including the asymmetric leaf-vs-internal median handling.
- CMU 15-445 lecture on tree indexes. Andy Pavlo walks through insertion + split with whiteboard examples for both cases.

## Where the next chapter picks up

The Tree works. The Tree is also one B+tree, singular, that happens to live at whatever root page id the application remembers in `Header.CatalogRootPageID`. That's the same dual-purpose hack M4 introduced — fine for one tree, completely insufficient for many.

M6 (next chapter) introduces the **catalog**: a meta-B+tree that maps table names to their tree root ids, column schemas, and creation SQL. `Header.CatalogRootPageID` finally gets to point at a real catalog (not a placeholder primary-tree root), and the catalog itself is just another B+tree built with the same primitives this chapter shipped — recursion, in the nicest possible way.

The catalog also has a small bootstrap puzzle that's fun to think about: if the catalog stores root ids of all tables, how does the catalog find its own root? The answer is in the header, which is the only thing that exists before any tree. M6 walks that out in detail. See you there.

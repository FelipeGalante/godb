# ADR-0018: `btree.UpdateCellSameSize` — same-size in-place cell update

- Status: Accepted
- Date: 2026-05-30
- Tags: btree, catalog, persistence

## Context

The catalog (M6) stores one row per table, encoded as a binary object inside a B+tree cell. One of those object fields is `RootPageID` — the page id where that table's B+tree's root lives. When the M8 executor inserts rows into a table and the table's tree root grows via a root-split (M5), `RootPageID` changes in memory but needs to be persisted in the on-disk catalog row so the next reopen finds the post-split root.

In M6 we left `Catalog.SetTableRoot` as in-memory-only because the btree had no in-place cell update primitive. The M6 plan documented this as a "known gap to close before M9" — the executor (M8) is the first caller that produces real root drift via INSERT, so M8 is the milestone that has to fix it.

The full general answer ("update a cell whose new payload is a different size") requires either growing the cell in place (rarely fits given variable-length packing) or delete-then-reinsert (no `DeleteCell` exists in v0.1; v0.2's rollback journal makes deletion atomic). Neither is appropriate for M8's scope.

But: the catalog's specific use case has a useful property. The catalog row's encoded layout puts `RootPageID` as a fixed-width 8-byte field. Re-encoding the same object with only `RootPageID` changed produces a byte slice of identical length to the original. So the catalog only needs a **same-size** update primitive — which is much cheaper and safer to add.

## Decision

Add two thin primitives to `internal/btree`:

```go
// At the leaf-page level:
func UpdateCellSameSize(pg *storage.Page, key uint64, newPayload []byte) error

// At the Tree level (descends to the right leaf):
func (t *Tree) UpdateCellSameSize(key uint64, newPayload []byte) error
```

Behavior:

- Find the cell with `key`. Return `ErrKeyNotFound` if absent.
- Compare `len(newPayload)` to the existing cell's payload length. If different, return `ErrSizeChanged` (and leave the page unchanged).
- Otherwise overwrite the payload bytes in place. The key-and-payload-length prefix stays put because their encoded sizes are unchanged.

`Catalog.SetTableRoot` then re-encodes the catalog row with the new `RootPageID` and calls `tree.UpdateCellSameSize(info.ID, newPayload)`. Same-size invariant holds by construction.

## Consequences

**Enables.** Closes the M6 SetTableRoot persistence gap. Any future caller whose update happens to be same-size (e.g. updating a fixed-width integer in a row, when the executor learns about UPDATE later) can use this primitive.

**Constrains.** The same-size constraint is real. Updates that change the payload's encoded length still aren't supported. Callers who need that have to wait for v0.2's deletion + reinsert story (which arrives with the rollback journal).

**Reversibility.** The primitive is additive; removing it would be backward-incompatible only with the catalog (the only caller in v0.1). Easy to extend later (relax the constraint when delete-then-reinsert is available).

## Alternatives considered

**Full `UpdateCell` with arbitrary new size.** Requires growing the cell in place (rarely fits in the available slot) or deleting and reinserting. Delete isn't available in v0.1; even when it is (v0.2), atomic delete-reinsert needs the rollback journal to be crash-safe.

**Stable-id indirection page.** The catalog could store a fixed page id per table that holds a single 8-byte `RootPageID` (updated in place). One extra page per table, one extra read on every catalog lookup. The data lives at a page-level offset rather than inside a cell, so updates don't need the btree's help. Rejected: an extra page per table is wasteful; the indirection adds complexity to catalog lookups; the constraint-relaxation that same-size updates need is already minimal.

**Skip persistence entirely.** What M6 did. Rejected for M8: the executor needs this to work for any table that root-splits.

## Related

- Code: [`internal/btree/leaf.go`](../../internal/btree/leaf.go) — `UpdateCellSameSize`; [`internal/btree/tree.go`](../../internal/btree/tree.go) — `Tree.UpdateCellSameSize`; [`internal/btree/errors.go`](../../internal/btree/errors.go) — `ErrSizeChanged`, `ErrKeyNotFound`; [`internal/catalog/catalog.go`](../../internal/catalog/catalog.go) — `SetTableRoot` is the consumer.
- Book: [Chapter 10 — Public Go API + Planner + Executor (M8)](../book/10-milestone-8-public-api.md).
- See also: [ADR-0014 (catalog row encoding)](0014-catalog-row-encoding.md) — places `RootPageID` at a fixed offset, which is what makes same-size updates work.
- See also: [ADR-0010 (slotted page layout)](0010-slotted-page-layout.md) — the underlying invariant the primitive respects (payloads are pointed to by directory entries; same-size payload means same directory entry).

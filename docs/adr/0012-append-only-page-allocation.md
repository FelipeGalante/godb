# ADR-0012: Append-only page allocation in v0.1 (no freelist)

- Status: Accepted
- Date: 2026-05-30
- Tags: storage, allocation, scope

## Context

A database that supports deletion will eventually have pages that are no longer used. Those pages can either be:

- **Reclaimed and reused** via a freelist: a linked list of freed page IDs that subsequent allocations draw from.
- **Left in place** as "dead pages" — the file size doesn't shrink, but the pager skips them on allocation.

A real freelist is a small but non-trivial structure: it lives on its own pages (typically a chain), needs atomic updates alongside the page count, and has to survive crash recovery in v0.2+ (a freed page that gets reused mid-crash can corrupt data if the rollback journal doesn't account for it).

In v0.1, GoDB has no deletion at all — the B+tree (when M4+ arrives) only inserts and reads. There are no freed pages because nothing has been freed. A freelist would have nothing to do.

## Decision

GoDB v0.1's `Pager.AllocatePage` is **append-only**: each call increments the database's page count, writes the type byte at offset 0 of the new page, persists the updated page count to the header, and returns the new page. There is no freelist; the `FreelistHeadPage` field in the database header exists and is reserved, but always reads as 0 in v0.1.

When deletion lands (v0.2 per spec §3.2), the freelist will be implemented and `AllocatePage` will check it first before extending the file.

## Consequences

**Enables.** A trivial allocator: increment a counter, extend the file by one page-write, persist the counter. The whole thing is ~10 lines of code. No corruption modes from freelist mis-updates. No coupling between deletion (which doesn't exist) and allocation.

The database file grows monotonically across the lifetime of a single GoDB version. For workloads that only insert, this matches what a freelist allocator would do anyway. For workloads that delete a lot — none yet exist — the file would grow without bound. Acceptable in v0.1 because deletion doesn't exist.

**Constrains.** When deletion lands in v0.2, callers of `AllocatePage` won't see any contract change, but the implementation gets more complex. The header's `FreelistHeadPage` field is already reserved, so the file format itself doesn't change.

**Reversibility.** Adding the freelist is additive: existing `.godb` files have `FreelistHeadPage = 0` (meaning "no freelist yet, append to extend") and continue to work. New writes start populating it. The on-disk format reads correctly under both regimes.

## Alternatives considered

**Implement the freelist now, even with no deletion.** Rejected: code with no consumer is dead weight, and dead code accumulates bit rot. We'd be testing freelist behavior against scenarios we couldn't actually construct (we can't delete anything to free a page).

**Stub the API and panic.** `AllocatePage` could pretend to consult a freelist and panic in the "freed" branch. Rejected: pointless complexity. Better to write a trivial allocator that's obviously correct than a fake one with a "TODO" panic.

**Skip the `FreelistHeadPage` header field for now, add it in v0.2.** Tempting for "don't reserve what you don't use." Rejected: adding the field later would be a header-layout change, requiring a file-format major version bump. The cost of reserving 8 bytes now is zero; the cost of bumping the format major version later is real.

## Related

- Code: [internal/storage/pager.go](../../internal/storage/pager.go) — `Pager.AllocatePage`; [internal/storage/header.go](../../internal/storage/header.go) — `Header.FreelistHeadPage`
- Book: [Chapter 03 — Pages, Files, and Durability](../book/03-milestone-1-pager.md)
- See also: ADR-0001 (single file, fixed pages) — append-only relies on page-aligned offsets.
- Spec §8 (storage layer responsibilities and v0.2 freelist behavior).

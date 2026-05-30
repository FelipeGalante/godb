# ADR-0006: No buffer pool in v0.1 — direct pager I/O

- Status: Accepted
- Date: 2026-05-30
- Tags: performance, scope, storage

## Context

A buffer pool sits between the pager and everything above it. Its job is to cache pages in memory so that repeated reads or writes to the same page don't hit disk every time. Production engines (SQLite, Postgres, MySQL, RocksDB, BoltDB) all have one. They typically include:

- A frame table (page-id → in-memory page).
- A pin/unpin protocol so callers can hold a page across operations.
- A dirty bit so the pool knows which pages need flushing.
- An eviction policy (Clock, LRU, ARC, …) for when the pool is full.

All of this is well-trodden territory, but it's also a non-trivial amount of code with subtle correctness concerns: what happens if a caller forgets to unpin? What happens if eviction tries to evict a dirty pinned page? What happens if a flush errors mid-eviction?

For v0.1, GoDB's working set is tiny (a few pages per test) and durability is autocommit-flush after each write ([ADR-0014](#) — coming later). The buffer pool would add complexity without changing what the engine can do.

## Decision

GoDB v0.1 has **no buffer pool**. The pager returns fresh `*storage.Page` values from `ReadPage` and the caller hands the same `*storage.Page` back to `WritePage`. All I/O is direct `pread`/`pwrite` against the underlying file. The buffer pool lands in v0.2 (per spec §9.3), with frames, pin/unpin, dirty tracking, and a clock-or-LRU eviction policy.

The `internal/buffer/` package directory is reserved (with a `.gitkeep`) so import paths don't have to move when the buffer pool arrives.

## Consequences

**Enables.** A pager that fits in one file (`internal/storage/pager.go`) with clear `ReadPage`/`WritePage`/`AllocatePage`/`Sync`/`Close` semantics. M1 can be written and tested in a single sitting. Bug surface is minimal — there is no caching layer to invalidate, no eviction to race against, no pin/unpin protocol for callers to get wrong.

**Constrains.** Repeated reads of the same page hit disk every time. For v0.1 workloads (single-process, small data, integration tests) this is irrelevant. For anything resembling a benchmark or a realistic workload, it would be a serious performance bug.

This decision also defers an architectural question: when the buffer pool arrives, the existing `Pager.ReadPage` signature (returning a freshly-allocated `*storage.Page`) will likely change to return a pinned reference. That's a breaking change to every caller. Mitigated by the fact that every caller currently lives behind `internal/` and the rewrite happens with the v0.2 buffer-pool work as one coordinated change.

**Reversibility.** Adding the buffer pool is the v0.2 plan. Removing it later is not.

## Alternatives considered

**Build the buffer pool first, in M1.** Rejected: doubles the M1 surface area, introduces pin/unpin and dirty-tracking concerns before any layer above storage exists to exercise them. The buffer pool is more usefully built against a real consumer (the B+tree, in v0.2) than against tests of its own behavior.

**A trivial map-based cache (no eviction, no pin/unpin).** "Page cache lite." Rejected: ends up being either a footgun (callers think they have caching but only get it sometimes) or a fake (only correct for tests where the working set is small). Better to have nothing than something misleading.

**Memory-map the file and let the OS handle caching.** Rejected for the same reasons as in [ADR-0001](0001-single-file-fixed-pages.md): mmap's failure modes are hard to reason about, and writing a real pager is part of the project's educational point.

## Related

- Code: [internal/storage/pager.go](../../internal/storage/pager.go), [internal/buffer/.gitkeep](../../internal/buffer/.gitkeep)
- Book: [Chapter 03 — Pages, Files, and Durability](../book/03-milestone-1-pager.md)
- Spec §9 (buffer pool design, including v0.1 and v0.2 decisions).
- See also: ADR-0001 (fixed page size) — makes direct page I/O simple.

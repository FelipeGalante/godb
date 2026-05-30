# ADR-0001: Single `.godb` file with fixed 4 KB pages

- Status: Accepted
- Date: 2026-05-30
- Tags: storage, file-format, on-disk

## Context

A database engine has to pick a unit of I/O. Reading or writing one row at a time (variable-length, arbitrary offset) is operationally painful: the filesystem cache, the disk hardware, and any future page-replacement policy all want to think in fixed-size chunks. SQLite and most engines settle on a fixed page size that becomes the atom of I/O for the lifetime of the file.

GoDB also has to decide whether the database lives in one file or many. Multi-file layouts (data file + index file + WAL + journal + ...) make for clean separation of concerns, but they multiply the work the engine has to do to keep the set consistent — every file format change becomes "and what about the other files?"

This decision is locked in early because the page size is encoded into the file header (so reading an existing file works), and because every layer above storage — record encoding, slotted page, B+tree — sizes itself relative to the page.

## Decision

GoDB v0.1 stores each database in a **single `.godb` file** with a **fixed 4096-byte page size**. The page size is encoded in the database header for forward compatibility but is not configurable at create time in v0.1.

```go
// internal/storage/page.go
const PageSize = 4096
```

Page 0 is always the database header. Page 1 is reserved for the catalog root (allocated by M6). Pages 2+ are for tables, indexes, overflow, or freelist use.

## Consequences

**Enables.** A simple `pread`/`pwrite` storage layer with no scatter/gather. A trivial bound for "how much data is a B+tree node" — exactly one page. Easy to reason about durability: one page is one atomic write attempt from the application's perspective (the filesystem may or may not honor that, but the API contract is clear). Easy to inspect: `xxd -s $((PAGE*4096)) -l 4096 file.godb` dumps any page.

**Constrains.** Cells can never exceed one page minus the header. Once we have B+tree leaves, we will need overflow pages (later) for any payload larger than ~4 KB. Workloads with very small rows under-utilize the page (lots of internal fragmentation); workloads with very large rows hit the overflow case more often. Both are accepted for v0.1 — performance is not the priority.

**Reversibility.** The page size constant can be made configurable in a later version by reading it from the header at open time (the field already exists). The file format itself doesn't have to change for this — only the constant has to become a variable. The choice of *4096* specifically (as opposed to 8192 or 16384) is reversible too, but only for newly-created databases.

## Alternatives considered

**Multi-file layout (one file per table + a manifest).** Cleaner separation between tables, easier to ship a single table around. Rejected: SQLite proves a single-file design works well at this size, and the operational simplicity of "one file to copy / open / lock / inspect" is the whole point of an embedded database. Multi-file would also push GoDB into the "you need to handle five files atomically" territory immediately, which is too much for v0.1.

**Variable-size pages.** Some engines allow per-table page sizes. Rejected: every algorithm we'd write — B+tree, slotted page, free space tracking — would have to take page size as a parameter, which doubles the cognitive load for no v0.1 benefit. Future versions can revisit by storing per-table page sizes in the catalog if there's a real use case.

**Memory-mapped (mmap) storage.** mmap simplifies code by letting the OS manage paging. Rejected for v0.1: mmap's failure modes (SIGBUS on disk-full, asynchronous page faults inside critical sections, OS-dependent flush semantics) are harder to reason about than explicit `pread`/`pwrite`, and the educational value of writing a real pager is the whole point of M1.

## Related

- Book: [Chapter 03 — Pages, Files, and Durability](../book/03-milestone-1-pager.md)
- Code: [internal/storage/page.go](../../internal/storage/page.go), [internal/storage/pager.go](../../internal/storage/pager.go)
- See also: ADR-0006 (no buffer pool in v0.1) — direct page I/O is viable precisely because the page size is fixed.
- See also: ADR-0012 (append-only allocation) — page-aligned offsets make this trivial.

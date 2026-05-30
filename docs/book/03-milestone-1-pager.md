# Chapter 03 — Pages, Files, and Durability (M1)

## Where we are

You finished [Chapter 02](02-milestone-0-skeleton.md) and the project compiles. There is a Go module, a directory tree, a Makefile, a CI workflow, and a CLI binary that prints a banner. That's it. There is no storage. There is nowhere to put data.

This chapter changes that. By the end of M1 you can create a `.godb` file, allocate pages in it, write bytes into those pages, close the process, reopen the file, and read the same bytes back out. Everything else in this book — records, slotted pages, B+trees, SQL — leans on the abstraction this chapter builds: the **pager**.

## Foundation

A database file is not a magic format. It is, at the level the operating system sees, a flat sequence of bytes. The engine has to impose structure on those bytes. That structure has three jobs:

1. **Discoverability.** Given just a file, the engine must be able to open it, recognize it as a valid `.godb`, and find its metadata (page count, root pointers, version).
2. **Efficient I/O.** Reading and writing happen in chunks the OS, the disk, and the engine all like. Variable-length record-at-a-time I/O is operationally painful.
3. **Crash safety.** When `Close()` returns, the data should be on disk. When the process dies mid-write, the file should still be openable (even if the in-flight write is lost).

Every database engine in the SQLite/Postgres family solves these by adopting the same set of conventions:

- A **fixed-size page**, typically 4 KB or 8 KB.
- A **database header** that lives in the first page and tells the engine everything it needs to open the rest of the file.
- A **pager** layer that owns all read/write/allocate operations on the file and knows nothing about what's *inside* the pages.

We'll cover each of these in turn, then walk through the code.

### Why fixed-size pages

A page is a fixed-size chunk of the database file. For GoDB, 4096 bytes ([ADR-0001](../adr/0001-single-file-fixed-pages.md)). The file is logically an array of pages: page 0 lives at byte offset 0, page 1 at offset 4096, page 2 at 8192, and so on. Page number times page size gives you the byte offset.

The reasons for fixed-size pages compound:

- **Predictable I/O.** Reading "page 5" is always a single `pread(fd, buf, 4096, 5*4096)`. No length to look up, no chasing variable-length pointers.
- **OS cache alignment.** Linux's filesystem cache is page-based (typically 4 KB). Writing in 4 KB chunks aligned to 4 KB offsets means each write lines up with exactly one OS page — no read-modify-write under the hood.
- **Disk friendliness.** Modern SSDs erase in much larger units (16 KB+), but they still address in 4 KB sectors. Writing in 4 KB chunks aligned to 4 KB is the floor of what hardware likes.
- **Bounded units of work for higher layers.** The B+tree's interior nodes will be exactly one page. The slotted page's cell directory will live in exactly one page. The buffer pool (later) will cache and evict in units of one page. Every higher layer benefits from "one page = one unit."

The cost: a single value larger than ~4 KB doesn't fit in a single cell, so the engine eventually needs *overflow pages* to handle them. That's a later milestone's problem.

### Why a file header

The header is a special page (page 0) that the pager always knows is there. It carries the metadata the engine needs to open a file it has never seen before:

- **Magic bytes** so a non-`.godb` file is rejected before we trust any of its contents.
- **Version numbers** so old binaries can refuse to open files written by future binaries.
- **Page size**, in case it ever becomes configurable (it isn't in v0.1, but the field is reserved).
- **Page count**, so the pager knows how many pages exist without statting the file.
- **Root page pointers** for the catalog (later) and the freelist (later).
- **Counters** (change counter, last transaction id) used by clients to detect concurrent modification (later).
- **Reserved space** for future additions without bumping the format version.

GoDB's header layout is in [ADR-0002](../adr/0002-big-endian-on-disk.md) (for endianness) and the spec; the exact byte layout is encoded in [`internal/storage/header.go`](../../internal/storage/header.go). It is 60 bytes of meaningful content followed by ~4 KB of reserved zeros — there's plenty of room to add fields without changing the format.

### `pread`, `pwrite`, and why `Read` + `Seek` is wrong

If you've written file-handling code in Go before, you've probably reached for the pattern:

```go
file.Seek(offset, io.SeekStart)
file.Read(buf)
```

This is fine for sequential, single-threaded reads. It is a footgun for a database. `Seek` moves the file pointer; subsequent reads in other goroutines (or even the same goroutine, if you forget) interact with the same shared cursor. The right primitive is **positional I/O**:

```go
file.ReadAt(buf, offset)
file.WriteAt(buf, offset)
```

These don't touch any cursor; each call is self-contained and safe to issue concurrently. GoDB uses `ReadAt` / `WriteAt` exclusively. (Under the hood on Linux, they call `pread(2)` and `pwrite(2)`.)

### `fsync`, and what "the write happened" really means

When you call `file.WriteAt(buf, offset)` and it returns success, the bytes are *not necessarily on the physical disk*. They are in a kernel buffer somewhere. The OS will probably flush them out within a few seconds, but a power loss right now would lose them.

To force a flush, you call `file.Sync()` (Go's name for `fsync(2)`). `Sync` blocks until the OS reports the bytes have been written to the underlying storage device. It is, on modern SSDs, slow — milliseconds — because it pushes through every level of cache.

The contract every database manages is: **a write is not durable until `Sync` returns successfully**. If you write 1000 pages and then crash before calling `Sync`, you have written zero pages, from a durability standpoint. If you call `Sync` after the first 500 and then crash, you have written 500 pages and the rest are lost.

In v0.1, GoDB takes the simplest possible approach: `Pager.Close()` calls `Sync()`, and the public `Sync()` method does the same. The buffer pool, group commit, and write-ahead log are all later optimizations that change *when* Sync happens but not *what* it means.

### What "the pager" is

The pager is the layer that owns the database file. Specifically, it owns:

- The `*os.File` handle.
- The in-memory copy of the database header.
- The page count.
- A mutex so concurrent callers don't race each other on the header.

It exposes a small surface:

- `OpenPager(path, opts)` — open an existing `.godb` or create a new one.
- `ReadPage(id)` — read page `id` into a fresh `Page` value.
- `WritePage(pg)` — write the bytes of `pg` back to disk at its ID's offset.
- `AllocatePage(type)` — extend the file by one page, tag it with a type byte, persist the new page count.
- `Sync()` — flush the file (and any header changes) to disk.
- `Close()` — sync, then close the file.
- `PageCount()`, `Header()` — accessors.

The pager is **not** responsible for:

- The contents of any page beyond the type byte at offset 0 (the slotted page layer owns that).
- Records, tables, rows, SQL — nothing higher up.
- Caching, eviction, dirty tracking — that's the buffer pool, deferred to v0.2 ([ADR-0006](../adr/0006-no-buffer-pool-in-v0-1.md)).
- Crash recovery beyond "validate the header on open" — that's the journal, deferred to v0.2.

This is a deliberately tiny contract. Tiny contracts are testable, replaceable, and don't accidentally encode assumptions that bite later.

## Decisions

| Decision | Why | Where |
|---|---|---|
| 4 KB fixed page size | Aligns with OS / hardware; bounded units of work | [ADR-0001](../adr/0001-single-file-fixed-pages.md) |
| Big-endian on disk | Readable in hex dumps, matches SQLite & network order | [ADR-0002](../adr/0002-big-endian-on-disk.md) |
| Magic bytes `GODB` at offset 0 | Distinct from SQLite's "SQLite format 3" | [`header.go:11`](../../internal/storage/header.go) |
| Major/minor format version | Old binaries can refuse future-format files | [`header.go:17-19`](../../internal/storage/header.go) |
| `PageType` enum has all v0.1 + v0.2 values reserved | On-disk type bytes can't reorder later | [`page.go:19-30`](../../internal/storage/page.go), [ADR-0007](../adr/0007-explicit-kind-byte-values.md) |
| `AllocatePage` writes only the type byte; rest is zero | Higher layers own the full body layout | [`pager.go`](../../internal/storage/pager.go), see "deferred" comment |
| No buffer pool in v0.1 | Defer complexity until there's a real workload | [ADR-0006](../adr/0006-no-buffer-pool-in-v0-1.md) |
| Append-only allocation in v0.1 | No deletes yet, so no freelist either | [ADR-0012](../adr/0012-append-only-page-allocation.md) |

## The code

The storage layer is four files. Read them in this order:

### [`internal/storage/page.go`](../../internal/storage/page.go)

42 lines. Defines:

- `const PageSize = 4096` — the load-bearing constant.
- `type PageID uint64` — every page is identified by its ordinal (0-based).
- `type PageType uint8` with explicit values for `PageTypeInvalid`, `PageTypeHeader`, `PageTypeCatalogLeaf`, `PageTypeCatalogInternal`, `PageTypeTableLeaf`, `PageTypeTableInternal`, `PageTypeIndexLeaf`, `PageTypeIndexInternal`, `PageTypeOverflow`, `PageTypeFree`. Most of these aren't used in M1; they're reserved so on-disk values stay stable as later milestones land.
- `type Page struct { ID PageID; Data [PageSize]byte; Dirty bool }` — the in-memory view of a single page. `Data` is an inline `[4096]byte` array (not a slice), so a `Page` value is exactly one page plus a few bytes of overhead.

The `Page` type is deliberately small. There's no buffer-pool frame, no pin count, no reference back to the pager. In M1, the pager hands you a fresh `Page` from `ReadPage`; you mutate it; you hand it back to `WritePage`. That's it.

### [`internal/storage/header.go`](../../internal/storage/header.go)

Defines the on-disk layout of page 0. The key pieces:

- `var Magic = [4]byte{'G', 'O', 'D', 'B'}` — the magic bytes. Different from SQLite's, on purpose.
- `const FormatMajor uint16 = 0` and `const FormatMinor uint16 = 1` — version numbers.
- `type Header struct { ... }` — the fields, with a doc comment that has the exact byte layout.
- `func (h *Header) Encode(buf []byte) error` — write the header into a 4 KB buffer (zero-fills the reserved bytes).
- `func DecodeHeader(buf []byte) (*Header, error)` — read a header, validate the magic, the page size, and the major version. Returns typed errors (`ErrInvalidMagic`, `ErrUnsupportedVersion`, `ErrPageSizeMismatch`) so callers can distinguish "this isn't a `.godb` file" from "this is, but it's an incompatible version."

The header is encoded as big-endian everywhere — see [ADR-0002](../adr/0002-big-endian-on-disk.md) for why. Notice the explicit byte offsets in the doc comment: this is the file format, written down once, in a place where future-you can find it.

### [`internal/storage/errors.go`](../../internal/storage/errors.go)

The typed errors the package returns. Most are sentinel `errors.New` values (`ErrInvalidMagic`, `ErrUnsupportedVersion`, `ErrPageSizeMismatch`, `ErrTruncatedFile`, `ErrPageOutOfRange`, `ErrClosed`). One is a struct, `*CorruptionError`, used when the pager detects structural damage at a specific page ID — the caller can `errors.As(err, &corruption)` to get the page ID and reason.

The point of typed errors over `errors.New("storage error")` everywhere: callers can match against them with `errors.Is` and react appropriately. A test that opens a deliberately corrupted file checks `errors.Is(err, storage.ErrInvalidMagic)`, not the error string. The string can change without breaking the test; the sentinel can't.

### [`internal/storage/pager.go`](../../internal/storage/pager.go)

The main event. ~230 lines. Walk through:

- `type PagerOptions struct { CreateIfMissing bool }` — controls whether a missing file is an error or an instruction to create. Narrow on purpose; more fields land when they're needed.
- `type Pager struct { mu sync.Mutex; file *os.File; header Header; closed bool }` — the pager's state. The mutex serializes header mutations and allocation; reads of existing pages don't strictly need it but take it anyway for v0.1 simplicity.
- `func OpenPager(path string, opts PagerOptions) (*Pager, error)` — branches on whether the file exists. If yes, `openExisting` reads page 0 and validates the header. If no and `CreateIfMissing`, `createNew` writes a fresh header. If no and not, returns a wrapped `os.ErrNotExist`. Atomicity matters here: if header decoding fails, we close the file before returning so the caller doesn't get a half-open handle.
- `func (p *Pager) ReadPage(id PageID) (*Page, error)` — bounds-checks `id` against the in-memory `PageCount`, allocates a fresh `Page`, and `ReadAt`s 4 KB at `id * PageSize`.
- `func (p *Pager) WritePage(page *Page) error` — bounds-checks, `WriteAt`s the page bytes, marks `page.Dirty = false`.
- `func (p *Pager) AllocatePage(pageType PageType) (*Page, error)` — the most subtle function in the file. Bumps `PageCount`, writes a zeroed page (with the type byte set at offset 0) at the new offset, then persists the updated `PageCount` to the header. Rolls back the in-memory `PageCount` on a header-write failure so we don't leak a half-allocated page.
- `func (p *Pager) Sync() error` — writes the header (it may have changed via `AllocatePage`) and calls `file.Sync()`.
- `func (p *Pager) Close() error` — Sync, then close. Idempotent: a second call is a no-op (returns nil) rather than an error, because `defer p.Close()` patterns shouldn't blow up if the caller has already closed.

Two things worth noticing in `AllocatePage`:

1. **The full page header layout is deferred.** Per the comment in the function and [ADR-0010](../adr/0010-slotted-page-layout.md), `AllocatePage` only writes the *type byte* at offset 0. The 28-byte general page header (cell count, free space offset, sibling/parent pointers, checksum) is owned by the M3 slotted-page layer. M1 deliberately doesn't write fields that M3 will overwrite anyway.

2. **The new page is extended by `WriteAt`.** Linux extends a file implicitly when you write past its end. We rely on this: there's no separate "grow the file" syscall before the write. The cost is that "the file has N pages" is only true after the corresponding write completes — but since `AllocatePage` is the only place that grows the file, and it's mutex-protected, there's no concurrent observer that can see a half-grown file.

## Tests as proof

The test suite is in [`internal/storage/pager_test.go`](../../internal/storage/pager_test.go), 14 tests. Read them — they document the contract more precisely than the prose:

- `TestOpenCreatesDatabaseFile` — `CreateIfMissing: true` produces a 4 KB file with a valid header and `PageCount == 1`.
- `TestOpenRejectsMissingFileWithoutCreate` — without `CreateIfMissing`, missing-file returns wrapped `os.ErrNotExist`.
- `TestOpenRejectsInvalidMagic` — random garbage at offset 0 → `ErrInvalidMagic`.
- `TestOpenRejectsTruncatedFile` — file shorter than `PageSize` → `ErrTruncatedFile`.
- `TestOpenRejectsUnsupportedVersion` — valid magic with a bumped major version → `ErrUnsupportedVersion`.
- `TestReadWritePageRoundTrip` — alloc, write a marker, close, reopen, read — bytes match.
- `TestAllocatePageIncrementsPageCount` — N allocations increment `PageCount` correctly and the increment survives reopen.
- `TestHeaderSurvivesReopen` — full header round-trips across close/reopen.
- `TestReadPageOutOfRangeReturnsError` and `TestWritePageOutOfRangeReturnsError` — bounds checks fire as expected.
- `TestSyncDurabilityBasic` — write, Sync, simulate-restart, reopen, read — bytes are there.
- `TestCloseIsIdempotent` — a second Close returns nil, not an error.
- `TestOpsAfterCloseReturnError` — ReadPage/AllocatePage/Sync after Close all return `ErrClosed`.
- `TestHeaderEncodeDecodeRoundTrip` — pure encoding test, no I/O.

All tests use `t.TempDir()` so they're hermetic. The pager touches no shared state outside the file it owns.

The race detector (`go test -race ./...`) is clean. There are no concurrent writes in v0.1, but the mutex-protected reads are exercised, and we want the test suite to stay clean as concurrency grows.

## What this layer cannot do yet

A real list, from honest:

- **Variable-length data.** A page is 4 KB of bytes. There's no notion of "a row" or "a value" yet. That's M2.
- **Multiple records per page.** Each page is opaque. To pack many cells into one page with a directory, you need slotted-page logic. That's M3.
- **Indexed lookup.** O(log n) lookup needs a B+tree. That's M4 (single-page) and M5 (multi-page).
- **Tables and schemas.** No catalog yet. That's M6.
- **SQL.** No parser, no planner, no executor. That's M7+.
- **Crash safety beyond `Sync`.** A crash mid-`AllocatePage` (after extending the file but before persisting the header) leaves the file in a state where `PageCount` is too small. v0.1 accepts this: on reopen, the file size doesn't matter as long as it's at least `PageCount * PageSize`. v0.2's rollback journal will close this gap.
- **Concurrent writers.** Two `Pager` instances on the same file would race each other at the OS level. v0.1 documents single-process use only; cross-process file locking is a much later concern.
- **Page checksums.** The header has a `ChecksumAlgo` field, but it's always 0 in v0.1. Detecting corruption-via-bit-rot is v0.2.
- **Freelist reuse.** Even when M2/M3/M4 lets you delete rows, you can't actually delete pages until v0.2 ([ADR-0012](../adr/0012-append-only-page-allocation.md)). The file only grows in v0.1.
- **A buffer pool.** Every `ReadPage` hits disk. Fine for v0.1; not for any realistic workload. v0.2 closes this ([ADR-0006](../adr/0006-no-buffer-pool-in-v0-1.md)).

Each of these is a milestone hook for later chapters.

## Further reading

- The SQLite [Database File Format](https://www.sqlite.org/fileformat.html) page, especially sections 1.2 (page sizes) and 1.3 (the database header). GoDB's header is much simpler, but the structure is recognizable.
- Linux man page for `pread(2)` and `pwrite(2)` — the syscalls behind `ReadAt`/`WriteAt`. Knowing what they actually do is worth ten minutes.
- "Files are Hard" (Dan Luu, 2017) — a sobering tour of how easy it is to get file durability wrong. GoDB's v0.1 approach (call `Sync` on every commit) is deliberately the simplest possible thing precisely because of issues like the ones described in that post.
- Andrew Kalmans' [pager.c walkthrough](https://www.sqlite.org/src/file?name=src/pager.c) in SQLite source — far more sophisticated than GoDB's v0.1 pager, but the same primitives are recognizable.

## Where the next chapter picks up

Chapter 04 takes the next step up the layered model: from "a page is 4 KB of bytes" to "a row is a sequence of typed values." Variable-length data, type tags, NULL vs empty TEXT, schema validation. The record layer has no I/O of its own — it produces bytes that M3 will store inside cells, which M4 will index. By the end of M2 you'll be able to encode `[1, "Felipe", true]` into a byte slice, validate it against a schema, decode it back. The bytes will still be in memory; persistence comes in M3 when those bytes start landing in slotted-page cells.

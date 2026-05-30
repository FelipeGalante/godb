# Chapter 01 — Foundations: what a database engine actually is

## Where we are

You have read the [introduction](00-introduction.md) and know what kind of book this is. There is no code yet. Before we look at any code, we need a shared model of what a database engine *is* — what its layers do, how they hand off to each other, and why GoDB (and SQLite, and roughly every other engine of its kind) is structured the way it is. This chapter is the conceptual scaffold the rest of the book builds on. After this chapter, everything else is concrete: real files, real code, real bytes on disk.

## Foundation

### "Database engine" is not a single thing

When you say "database" out loud, you're using one word to describe roughly seven different things. The Postgres binary you connect to via `psql`? That's a database engine. The SQLite library a desktop app embeds? Also a database engine. A spreadsheet stored as a CSV? Arguably one. They all share a job — *give me a way to put data somewhere and get it back later, reliably* — but they implement that job at wildly different levels of complexity.

A useful first cut: a database engine is the layer of software between *your data* and *the bytes on disk*. It owns three responsibilities the application layer doesn't want to think about:

1. **Durability.** When you say "save this," it survives a power loss.
2. **Indexed lookup.** Finding one row out of a million doesn't take a million-row scan.
3. **Schema and types.** "Felipe's age" is an integer, not a string, and the engine enforces that even if your application code is sloppy.

Any engine that does those three things — Postgres, SQLite, BoltDB, BadgerDB, RocksDB, GoDB — is in the same family. The differences between them are mostly about *which other things* they also do (network protocols, replication, full SQL, MVCC, etc.) and *how big a working set* they can handle.

### Embedded vs. client-server

The biggest fork in the road is whether the engine runs *inside* your application's process or *outside* it.

A **client-server** database (Postgres, MySQL, MongoDB, almost every "database as a service") runs as its own process — usually on its own machine — and your application talks to it over a network socket. This gives you:

- Multiple application processes, even on multiple machines, sharing one logical database.
- Independent scaling and failure of the database vs. the application.
- A built-in authentication boundary.
- All the operational cost of running another piece of infrastructure.

An **embedded** database (SQLite, BoltDB, LevelDB) is a library your application imports. There's no separate process, no network socket, no authentication. The database file is just a file in your application's directory. Open it, use it, close it. This gives you:

- Zero operational cost.
- Speed: no network round-trip, often no IPC at all.
- Single-process (or carefully-locked multi-process) scope.
- No built-in multi-user model.

**GoDB is embedded.** It's a Go package you `import`. There is no server. There is no network protocol. The "database" is a single `.godb` file in your application's filesystem. This is the same shape as SQLite — which is why we say GoDB is SQLite-inspired — and it's the shape that fits a solo developer building a CLI tool, a desktop app, or a small local-first service.

### What "SQLite-inspired" actually means

"SQLite-inspired" is doing a lot of work in this project's framing, so let's pin it down.

SQLite is a textbook example of a small, well-designed embedded database. It chose:

- A single file on disk.
- Fixed-size pages (typically 4 KB).
- B-trees as the only index structure.
- A small SQL subset.
- A single-writer concurrency model (with WAL mode added later for better read concurrency).
- A focus on correctness and portability over raw performance.

GoDB borrows that *shape* — single file, fixed pages, B-trees, a small SQL subset, single writer, correctness-first. It does **not** borrow:

- SQLite's file format (the magic bytes differ; the page header differs; the cell format differs).
- SQLite's SQL dialect (no type affinity; no implicit conversions; almost no functions in v0.1).
- SQLite's varint encoding (GoDB uses LEB128 from `encoding/binary`).
- SQLite's twenty years of edge-case handling.

See [ADR-0004](../adr/0004-no-sqlite-compatibility.md) for the explicit non-compatibility decision. The short version: matching SQLite's bits would mean either cloning SQLite (no point) or supporting an incompatible-but-pretending-to-be-similar dialect (a footgun). Neither serves the project's goals.

### The layered architecture

This is the most important picture in the book. Every milestone slots into one of these layers; every chapter explains a layer or the boundary between two.

```
+-----------------------------------------+
|  Application code                       |   (your Go program)
+-----------------------------------------+
              ↓  godb.Open / Exec / Query
+-----------------------------------------+
|  Public API (pkg/godb)                  |   ← stable, ergonomic surface
+-----------------------------------------+
              ↓
+-----------------------------------------+
|  SQL                                    |   ← lexer → parser → AST
|  (internal/sql)                         |
+-----------------------------------------+
              ↓
+-----------------------------------------+
|  Planner                                |   ← AST → query plan
|  (internal/planner)                     |
+-----------------------------------------+
              ↓
+-----------------------------------------+
|  Executor                               |   ← plan → table+row operations
|  (internal/exec)                        |
+-----------------------------------------+
              ↓
+-----------------------------------------+
|  Catalog                                |   ← table metadata, schemas
|  (internal/catalog)                     |
+-----------------------------------------+
              ↓
+-----------------------------------------+
|  B+tree                                 |   ← indexed lookup over pages
|  (internal/btree)                       |
+-----------------------------------------+
              ↓
+-----------------------------------------+
|  Slotted page                           |   ← many cells in one page
|  (internal/btree, lower layer)          |
+-----------------------------------------+
              ↓
+-----------------------------------------+
|  Records                                |   ← typed values, encoded rows
|  (internal/record)                      |
+-----------------------------------------+
              ↓
+-----------------------------------------+
|  Buffer pool (v0.2)                     |   ← page cache with eviction
|  (internal/buffer)                      |
+-----------------------------------------+
              ↓
+-----------------------------------------+
|  Pager / storage                        |   ← fixed-size pages, file I/O
|  (internal/storage)                     |
+-----------------------------------------+
              ↓
+-----------------------------------------+
|  Operating system                       |   ← read(2), write(2), fsync(2)
+-----------------------------------------+
              ↓
+-----------------------------------------+
|  Disk                                   |   ← spinning rust or NAND flash
+-----------------------------------------+
```

A few things to notice in this diagram:

- **Each layer only talks to the layer immediately below it.** The SQL layer doesn't know about pages. The B+tree doesn't know about SQL. The pager doesn't know about types. This is what makes the engine tractable — each layer has a tightly-scoped responsibility.
- **Reads and writes flow up and down through the same stack.** When you `INSERT` a row, the SQL layer parses it, the planner picks an insert plan, the executor calls into the catalog and B+tree, the B+tree picks a leaf page, the slotted page packs the cell, the pager flushes the page to disk. When you `SELECT` it back, the path is the same in reverse.
- **The layers don't all exist yet.** As of the milestone this chapter is being written for, GoDB has the bottom four layers (pager, records, slotted page; B+tree is M4-next). Everything above that is reserved-but-empty package directories. This is deliberate — see the next subsection.

### Why bottom-up

There's a strong temptation, when building any system, to start at the top — write a SQL parser, get pretty error messages, have something to show. GoDB explicitly does the opposite: storage first, SQL last. The reasoning is captured in [ADR-0005](../adr/0005-bottom-up-build-order.md), but in summary:

- **Bottom-up means every milestone produces something demonstrably correct on its own.** M1 (the pager) can be tested in isolation against a temp file. By the time SQL arrives, the storage stack has been hammered by thousands of test runs.
- **Top-down means months of unrunnable code.** A parser without a planner, a planner without an executor, an executor without storage — none of them do anything by themselves. You can write a lot of code that doesn't quite work yet, which is exactly how hobby database projects get abandoned.
- **Bugs get bounded by layer.** A bug in the executor cannot be a storage bug if storage has been stable for six milestones. Debugging is mostly "diff since last green."

The cost: there's nothing flashy to show for the first chunk of the project. The first impressive demo (insert a typed row, read it back across a restart) doesn't arrive until M3/M4. Past Felipe was OK with that trade. Future Felipe (and you, the reader) gets a stack that's stable from the bottom up.

### What a "page" is, in two paragraphs

We will spend an entire chapter on pages (chapter 03). But because every later chapter assumes you know what a page is, here's the two-paragraph version.

A **page** is a fixed-size chunk of the database file — in GoDB, 4096 bytes ([ADR-0001](../adr/0001-single-file-fixed-pages.md)). The whole file is logically a sequence of pages: page 0 is the database header, page 1 is reserved for the catalog root, pages 2+ are for table data, index data, overflow, or the freelist. The page number times the page size gives you the byte offset of that page in the file. To read page 5, you `pread` 4096 bytes at offset 5*4096 = 20480. To write it, `pwrite`.

The reason for fixed-size pages — instead of, say, variable-length records packed end-to-end — is *everything*. Every higher layer assumes "one page = one unit of work." The B-tree node is exactly one page. The slotted-page cell directory addresses cells within one page. The buffer pool (when it lands) caches and evicts in units of one page. The OS-level filesystem cache and the disk hardware itself also like to think in fixed-size pages. Picking a page size is one of the most consequential decisions in a database engine, and once picked it's effectively permanent for the file format.

### Durability, in one paragraph

When your code calls `WritePage(pg)` and the function returns, *the page is not necessarily on disk*. The OS has accepted the bytes, but they're probably sitting in a kernel buffer. A power loss right now would lose them. To force the OS to actually flush — to push the bytes past every level of cache to the physical storage device — you call `fsync(fd)` (in Go, `file.Sync()`). Until you call `Sync`, you have not committed anything; you've only enqueued it. This is the durability contract every database has to manage. In v0.1, GoDB takes the simplest possible approach: it `Sync`s after every batch of writes, accepting some performance loss in exchange for a clear "if Sync returned, the data is durable" guarantee. Later versions will be smarter (write-ahead log, group commit) but the contract is the same.

### What an "index" actually buys you

The application-level view of an index is "make `WHERE x = 5` fast." The mechanical view is: an index is a *separate data structure* — usually a tree, sometimes a hash, sometimes a bitmap — that maps from a key to "the row(s) with that key." Without an index, finding `x = 5` requires scanning every row in the table (O(n)). With a B-tree on `x`, finding `x = 5` is O(log n) — you walk the tree from root to leaf, two or three pages of I/O instead of a million-row scan.

The primary index of a typical table is a B+tree keyed on the primary key. For GoDB, that's the `INTEGER PRIMARY KEY` rowid. The tree's leaves hold the row data; the tree's interior nodes hold sort keys and pointers to children. We'll get to the details in M4/M5. For now, just know that "B+tree" means "a data structure that makes ordered lookups fast across many pages." That's the whole point.

## Decisions

This chapter is conceptual, so the decisions live in ADRs and in later chapters that walk through the code. The biggest ones that shape everything below:

- [ADR-0001](../adr/0001-single-file-fixed-pages.md): single file, fixed 4 KB pages.
- [ADR-0004](../adr/0004-no-sqlite-compatibility.md): SQLite-inspired but not compatible.
- [ADR-0005](../adr/0005-bottom-up-build-order.md): bottom-up build order.

## The code

There is no code for this chapter. The repo, at the point this chapter was written, has:

```
go-database/
  cmd/godb/         ← CLI binary (banner only, so far)
  internal/
    storage/        ← pager (chapter 03)
    record/         ← records (chapter 04)
    btree/          ← slotted page (chapter 05), B+tree to come
    buffer/         ← reserved for v0.2 buffer pool
    catalog/        ← reserved
    sql/            ← reserved
    planner/        ← reserved
    exec/           ← reserved
    tx/             ← reserved
    engine/         ← reserved
  pkg/
    godb/           ← reserved (public API)
    driver/         ← reserved (database/sql driver)
  docs/             ← the docs you're reading
  testdata/         ← reserved (golden SQL etc)
```

Most of those directories are empty (with `.gitkeep` files) because the layers they'll hold are later milestones. Chapter 02 walks through why we set the project up that way before there's any code to put in most of these dirs.

## Tests as proof

No tests for this chapter — it has no code. Tests for the layers we've shipped live alongside the code: [`internal/storage/pager_test.go`](../../internal/storage/pager_test.go), [`internal/record/codec_test.go`](../../internal/record/codec_test.go), [`internal/record/schema_test.go`](../../internal/record/schema_test.go), [`internal/btree/leaf_test.go`](../../internal/btree/leaf_test.go).

## What this layer cannot do yet

This chapter sets up the model but doesn't build anything. Specifically, after reading it you cannot yet:

- Open a database file (chapter 03 covers that).
- Encode a row (chapter 04).
- Pack multiple rows into a page (chapter 05).
- Look up a row by primary key efficiently (chapter for M4, coming next).
- Run any SQL (chapter for M7+, much later).

Each of these is what a chapter is for.

## Further reading

- *Database Internals*, Alex Petrov (O'Reilly, 2019). The best single book on this topic. Chapters 1–4 cover everything in this chapter at proper depth.
- The SQLite documentation, specifically the [File Format](https://www.sqlite.org/fileformat.html) and [Architecture](https://www.sqlite.org/arch.html) pages. SQLite-inspired, remember.
- CMU 15-445 / 15-721 lecture videos, on YouTube. Free, rigorous, deeper than this book is going to get.
- *Designing Data-Intensive Applications*, Martin Kleppmann. Higher-altitude than this book but the chapter on storage engines (chapter 3) is essential reading.

## Where the next chapter picks up

The next chapter is M0 — the project skeleton. It is, deliberately, a small chapter. The goal of M0 is to set up the package layout, the Makefile, the CI workflow, and a CLI binary that prints a banner. It is the least flashy commit in the project; it's also the one that makes every subsequent milestone tractable. After M0, we earn the right to start writing storage code in M1.

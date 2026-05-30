# Chapter 00 — Introduction

## What this book is

This is a book about building a database engine. Not "using" one — *building* one. From the bottom up, in Go, in a single file on disk, with no dependencies beyond the standard library.

The engine being built is GoDB, the codebase you're sitting in. The book grows alongside the code: every milestone in the project gets a chapter. Reading the book in order, you'll see how a real (if small) embedded relational database comes together — first the file format, then the page abstraction, then variable-length records, then a packed-cell layout, then a B-tree on top of that layout, then a catalog, then a SQL parser, then a planner, then an executor, then a public API. Every layer leans on the one beneath it. Skip a layer and the layer above stops making sense.

The book is not a SQLite walkthrough, not a survey of database internals, not a research paper. It's an honest first-person record of building a specific engine and explaining each layer well enough that you could build your own version. The code is unapologetically Go-flavored — typed errors, package boundaries, table-driven tests, `internal/` for everything implementation-shaped. But the *concepts* (pages, slotted layouts, B-trees, write-ahead logs, query plans) are universal. If you ever write a storage engine in a different language, what you learn here will transfer.

## Who this book is for

The intended reader:

- Knows how to read and write Go. You don't need to be an expert — if you've written a CLI tool or a small web service, you have enough.
- Understands what a file, a process, an integer, and a byte are. We will spend time on what `pread` and `fsync` do, but we won't define "file."
- Is **new** to database internals. You may have used SQLite or Postgres or Redis. You may have written SQL. You almost certainly have *not* implemented a B-tree from scratch or thought about what "page" means at the storage layer. Both are fine — that's what this book is for.
- Reads code as a primary skill. The book points at files; the files are where the truth lives.

If you're already a storage-engine expert, this book will move too slowly for you. Read the [PRD](../prd.md) for what's being built, skim the [ADRs](../adr/) for the load-bearing decisions, and then go straight to the code.

## How this book maps to the codebase

The project is organized in milestones (M0 through M11). Each milestone closes one layer of the engine: M1 is the pager, M2 is records, M3 is slotted pages, and so on. Every milestone is one or more commits in `git log`, and every milestone has a chapter in this book once that milestone has landed.

So the reader-facing index is:

| Layer | Milestone | Chapter | Code lives in |
|-------|-----------|---------|---------------|
| Project skeleton | M0 | [02](02-milestone-0-skeleton.md) | repo root + empty package dirs |
| Pager | M1 | [03](03-milestone-1-pager.md) | [`internal/storage/`](../../internal/storage/) |
| Records | M2 | [04](04-milestone-2-records.md) | [`internal/record/`](../../internal/record/) |
| Slotted page | M3 | [05](05-milestone-3-slotted-pages.md) | [`internal/btree/`](../../internal/btree/) |
| Single-page B+tree | M4 | (next) | (next) |
| Multi-page B+tree | M5 | (later) | (later) |
| Catalog | M6 | (later) | (later) |
| SQL parser | M7 | (later) | (later) |
| Executor | M9 | (later) | (later) |
| Public API | M8 | (later) | (later) |
| CLI | M10 | (later) | (later) |

When you finish chapter 05, you'll have read about everything that exists today. The rest of the book grows with the code.

## How chapters are structured

Every milestone chapter follows the same pattern, so you can build a reading rhythm:

1. **Where we are.** One paragraph: what the previous chapters left us with, what we're about to add.
2. **Foundation.** The database-internals concepts you need before the code makes sense. This is where pages, B-trees, slotted layouts, etc. are explained from first principles. If you only read one section of a chapter, read this one.
3. **Decisions.** The concrete choices made in this milestone, each with rationale. Each decision links to its [ADR](../adr/) if one exists.
4. **The code.** A file-by-file walkthrough with [`file.go:line`](../../README.md) links. Not a paste of the code — a guided tour that explains why each piece exists. Open the files alongside the chapter.
5. **Tests as proof.** Which tests exist, what invariants they pin down, what they don't try to prove. Reading tests is a great way to understand a codebase; we'll point you at the right ones.
6. **What this layer cannot do yet.** An honest list of gaps. Each gap names the milestone that fills it.
7. **Further reading.** (Optional.) Pointers to SQLite docs, papers, or other sources for going deeper.
8. **Where the next chapter picks up.** One paragraph that hands off to the next milestone.

## Conventions

A few things to know up front so they don't trip you up later:

- **"You" is the reader. "We" is the project** (Felipe and any future contributors). The book uses second person for instructions and explanations, first person plural when describing decisions GoDB has made.
- **Code blocks are illustrative.** They show the shape of an idea or a key 5-line snippet — never the entire file. If you want the full implementation, open the file at the linked path. The file is the truth.
- **ADRs are the decision record.** When a chapter says "GoDB does X" without justifying it in full, there's usually an [ADR](../adr/) that explains why. Links are inline.
- **Spec references** (e.g. "spec §10") point at Felipe's private project spec. You don't need access to it to follow the book — anything load-bearing is also explained in the chapter — but the references exist for traceability.
- **Tests are first-class.** This is a project that takes test discipline seriously. When a chapter says "see [`pager_test.go`](../../internal/storage/pager_test.go)," go read the tests. They pin down behaviors that prose can't.

## What this book deliberately doesn't try to do

- It doesn't try to teach you Go. If you don't know what a `*os.File` is or what `defer` does, find a Go tutorial first.
- It doesn't try to be a complete database-internals textbook. For that, read *Designing Data-Intensive Applications* (Kleppmann), *Database Internals* (Petrov), or the CMU Database Systems course videos. This book is narrower and code-specific.
- It doesn't try to compare GoDB to other engines exhaustively. We compare to SQLite (and occasionally Postgres or BoltDB) when the comparison clarifies a decision, and not otherwise.
- It doesn't try to be exhaustively rigorous on every edge case. The code is rigorous (tests cover the edges); the prose summarizes.

## A note on humility

This is a small, educational engine. The team that built SQLite spent twenty years tuning what they shipped; the team that built Postgres has been at it for three decades. GoDB isn't trying to be either of those things. The goal is to build something *real* — a database you could genuinely use for a single-user CLI tool — while keeping it small enough that a reader can hold the whole thing in their head.

Every chapter ends with a "What this layer cannot do yet" section that's honest about the gaps. There are many. That's the point: each milestone closes a few of them, and the book grows to match.

## Where to start

If you're reading the book for the first time, the next chapter — [Chapter 01: Foundations](01-foundations-database-engines.md) — is the layered model that the rest of the book builds on. It has no code in it. After that, [Chapter 02: The Skeleton](02-milestone-0-skeleton.md) introduces the project layout, and then chapters 03–05 walk through M1, M2, and M3, in order.

If you'd rather jump straight to code: open [`internal/storage/pager.go`](../../internal/storage/pager.go) and start reading there. The book will still be here when you want context.

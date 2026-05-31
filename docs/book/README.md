# Building a Database Engine in Go from Scratch

A chapter-per-milestone narrative for the GoDB project. Read it alongside the commit history: each milestone-chapter explains the concepts behind a layer, then walks through the code that implements it.

## How to read this book

- **In order.** Each chapter assumes the previous one. Skipping the foundations chapter to jump to "the B+tree" will hurt.
- **Paired with the code.** Chapters reference files like [`internal/storage/pager.go`](../../internal/storage/pager.go). Open the file as you read.
- **Paired with the commits.** `git log` is the long-form changelog; each milestone chapter corresponds to one or more commits. New milestone, new commit, new chapter.
- **At your own depth.** The "Foundation" section of each chapter is the conceptual minimum to understand what comes next. The "Code" section is the guided tour. The "Further reading" pointers are for going deeper than this book chooses to.

## Table of contents

| #  | Chapter | What it covers |
|----|---------|----------------|
| 00 | [Introduction](00-introduction.md) | What this book is, who it's for, how it maps to the codebase, conventions |
| 01 | [Foundations: what a database engine actually is](01-foundations-database-engines.md) | The layered architecture, why we build bottom-up, what "embedded SQLite-inspired" really means |
| 02 | [The Skeleton (M0)](02-milestone-0-skeleton.md) | Project layout, package boundaries, CI as a forcing function |
| 03 | [Pages, Files, and Durability (M1)](03-milestone-1-pager.md) | Fixed-size pages, file headers, `pread`/`pwrite`, the pager pattern, what `fsync` really does |
| 04 | [Records: Variable-Length Typed Values (M2)](04-milestone-2-records.md) | Tagged unions, value/row encoding, NULL semantics, schema as a contract |
| 05 | [Slotted Pages: Many Records on One Page (M3)](05-milestone-3-slotted-pages.md) | The variable-length packing problem, slotted layout, cell directories, the "page full" contract |
| 06 | [The Smallest B+tree: One Leaf, One Root (M4)](06-milestone-4-b-tree-single-page.md) | B-trees and B+trees from first principles, height-zero trees, the Tree API that survives into M5+, where the root id lives |
| 07 | [The Multi-page B+tree: Splits, Descent, and Growth (M5)](07-milestone-5-multi-page-btree.md) | Leaf and internal splits, the path stack, root growth, the leaf chain, and the atomic-split limitation deferred to v0.2 |
| 08 | [The Catalog: Many Named Tables (M6)](08-milestone-6-catalog.md) | What metadata is, the bootstrap problem and the privileged header slot, why the catalog is itself just another B+tree, the magic-byte fence |
| 09 | [The SQL Frontend (M7)](09-milestone-7-sql-parser.md) | Why a lexer and parser are separate phases, recursive descent in practice, the deliberately small grammar, the "recognize and refuse" rejection pattern |
| 10 | [The Loop Closes: Public API + Planner + Executor (M8)](10-milestone-8-public-api.md) | Three-layer dispatch (parse/plan/execute); materialization vs streaming; strict bind/scan types; the same-size cell update that finally persists table root drift |
| 11 | [Polish and the database/sql Driver (M9)](11-milestone-9-polish-and-driver.md) | The adapter pattern (driver wraps native); database/sql value-type mapping; what "polish" looks like at this point in a database's life |

Chapters for milestones 10 and 11 land as those milestones land.

## Conventions

- **Code references** look like [`internal/storage/pager.go`](../../internal/storage/pager.go) — clickable from GitHub or any markdown reader that resolves relative paths.
- **ADR references** look like [ADR-0001](../adr/0001-single-file-fixed-pages.md) — they go to the decision record for the choice being discussed.
- **Spec references** point to sections of Felipe's private project spec (not in the repo). When a chapter says "spec §10", that's where the source design lives — but every load-bearing detail is also explained in the chapter itself.
- **Code blocks** are illustrative, not exhaustive. They show the shape of an idea or quote a key 5-line snippet — never paste an entire file. To see the full file, open it.
- **"You" is the reader. "We" is the project (Felipe + future contributors).**

## Status

This book is a living document. Each milestone adds at least one chapter. The introduction and chapters 01–11 cover M0 through M9 as of 2026-05-30.

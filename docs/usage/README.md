# Using GoDB

This is the entry point for *using* GoDB. The [book](../book/) tells you how GoDB is built; the [PRD](../prd.md) explains what GoDB is meant to be; the [ADRs](../adr/) record why specific design choices were made. None of those answer the practical question: *how do I run GoDB, and what can it do for me right now?* That's what this directory is for.

As of M8 the public Go API is real: `import "github.com/felipegalante/godb/pkg/godb"` and run SQL. The CLI binary still prints a banner and exits (full commands land at M10). The pages here describe what's usable today, what's coming, and where to read next.

## Where we are

| Milestone | Status | What it gives the user |
|-----------|--------|------------------------|
| M0 — Skeleton | ✅ | A Go module that builds; a CLI binary that prints a banner; `make test`, `make race`, `make vet`. |
| M1 — Pager | ✅ | A durable `.godb` file format with 4 KB pages and a validated header. Internal. |
| M2 — Records | ✅ | Typed values (`NULL`/`INTEGER`/`TEXT`/`BOOLEAN`) and row encoding. Internal. |
| M3 — Slotted page | ✅ | Many cells in one page, sorted by key, with O(log n) lookup. Internal. |
| M4 — Single-page B+tree | ✅ | A `Tree` type that wraps a single leaf and persists across reopens. Internal. |
| M5 — Multi-page B+tree | ✅ | Splits, descent, root grow. ~10,000-row trees survive close/reopen. Internal. |
| M6 — Catalog | ✅ | Multiple named tables in one database, each with its own B+tree. Metadata persists across close/reopen via the database header. Internal. |
| M7 — SQL lexer + parser | ✅ | `CREATE TABLE`, `INSERT`, `SELECT` parsed into a typed AST; unsupported features explicitly rejected with `ErrUnsupportedSQL`. Still internal. |
| **M8 — Public Go API + Planner + Executor** | ✅ | **`godb.Open`, `db.Exec`, `db.Query`, `Rows.Next`/`Scan`.** End-to-end: parse → plan → execute → typed results. Multi-table tables of arbitrary size survive close/reopen. Begin returns `ErrTransactionsUnsupported` until v0.2. See [`embedded-api.md`](embedded-api.md) for the tutorial. |
| M9 — polish + `database/sql/driver` | next | Optional `sql.Open("godb", path)` wrapper, multi-table integration tests, better error context. |
| M9 — Executor | | End-to-end SQL execution. |
| M10 — CLI | | Interactive shell, `exec`, `query`, `inspect`, `check`. |
| M11 — v0.1 release | | Tagged release; install + use from another Go project. |
| v0.2 | | Transactions, deletion, buffer pool, secondary indexes. |

## What you can do today

### 1. Use the embedded API

```bash
go get github.com/felipegalante/godb/pkg/godb
```

…then read the [**embedded-API tutorial**](embedded-api.md) for the full Open → Exec → Query → Scan loop. The supported SQL surface, parameter binding rules, scan type rules, and error contracts are all there.

### 2. Read the [development book](../book/)

The book walks the engine from the first commit forward, one chapter per milestone. It's written for someone who knows Go and wants to learn how a database engine is put together. Start at the [introduction](../book/00-introduction.md) and follow chapters in order; by the end of [chapter 10](../book/10-milestone-8-public-api.md) you've read everything the engine knows how to do today.

### 3. Build the CLI binary

```bash
make build
./godb
```

Prints `godb: SQLite-inspired database engine in Go` and exits. The CLI subcommands (`exec`, `query`, `inspect`, `check`, the interactive shell) all land at M10.

### 4. Internal-packages sandbox (optional)

If you want to call the engine's internal layers directly (for learning, or to extend the engine itself), see the [snapshot in `current-state.md`](current-state.md). Internal APIs can change without warning between milestones; the public `pkg/godb` API is the stable surface.

## When to read what

- **Just want to use it?** [`embedded-api.md`](embedded-api.md). Then `pkg/godb` Godoc when published.
- **Want to learn how databases work?** Start with the [book introduction](../book/00-introduction.md). It assumes Go knowledge and zero database-internals knowledge.
- **Want to understand a specific decision?** Browse the [ADR index](../adr/).
- **Want to know what's deliberately *not* being built?** Read the [PRD](../prd.md), specifically the Vision/Non-vision and Out-of-scope sections.

## What lands here next

As features land, new pages join this directory:

- M9: a `database/sql/driver` quickstart once the wrapper exists.
- M10: `cli.md` — the interactive shell, all the `inspect` subcommands, the `check` validator.
- v0.2: `transactions.md`, `migrations.md`.

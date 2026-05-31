# Using GoDB

This is the entry point for *using* GoDB. The [book](../book/) tells you how GoDB is built; the [PRD](../prd.md) explains what GoDB is meant to be; the [ADRs](../adr/) record why specific design choices were made. None of those answer the practical question: *how do I run GoDB, and what can it do for me right now?* That's what this directory is for.

As of M9 GoDB has both a native Go API (`pkg/godb`) and a `database/sql/driver` wrapper (`pkg/driver`). You can `import "github.com/felipegalante/godb/pkg/godb"` for the Go-native shape, or `import _ "github.com/felipegalante/godb/pkg/driver"` + `sql.Open("godb", path)` to plug into the `database/sql` ecosystem. The CLI binary still prints a banner (full commands land at M10). The pages here describe what's usable today, what's coming, and where to read next.

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
| M8 — Public Go API + Planner + Executor | ✅ | `godb.Open`, `db.Exec`, `db.Query`, `Rows.Next`/`Scan`. End-to-end: parse → plan → execute → typed results. Multi-table tables of arbitrary size survive close/reopen. Begin returns `ErrTransactionsUnsupported` until v0.2. See [`embedded-api.md`](embedded-api.md) for the tutorial. |
| **M9 — polish + `database/sql/driver`** | ✅ | **`sql.Open("godb", path)` works.** `pkg/driver` registers as `"godb"`; full `database/sql` API: `db.Prepare`, `sql.NullString`, `ExecContext`/`QueryContext`, the connection pool. Plus `godb.SQLError` (type alias — no internal imports needed), `godb.StatementError` (carries source SQL), `DB.Sync` (mid-life flush). See [`database-sql.md`](database-sql.md) for the tutorial. |
| M10 — CLI | next | Interactive shell, `exec`, `query`, `inspect`, `check`. |
| M11 — v0.1 release | | Tagged release; install + use from another Go project. |
| v0.2 | | Transactions, deletion, buffer pool, secondary indexes. |

## What you can do today

### 1. Use the embedded API (`pkg/godb`)

```bash
go get github.com/felipegalante/godb/pkg/godb
```

…then read the [**embedded-API tutorial**](embedded-api.md) for the full Open → Exec → Query → Scan loop. The supported SQL surface, parameter binding rules, scan type rules, and error contracts are all there.

### 2. Use it via `database/sql` (`pkg/driver`)

```bash
go get github.com/felipegalante/godb/pkg/driver
```

…then read the [**`database/sql` tutorial**](database-sql.md) for `sql.Open("godb", path)` + the standard library's API. Same SQL surface, same error sentinels (`errors.Is(err, godb.ErrXxx)` works through the wrapper), plus standard `sql.NullString`/`sql.NullInt64`, `Stmt.Exec`, the connection pool, `ExecContext`/`QueryContext`.

### 3. Read the [development book](../book/)

The book walks the engine from the first commit forward, one chapter per milestone. It's written for someone who knows Go and wants to learn how a database engine is put together. Start at the [introduction](../book/00-introduction.md) and follow chapters in order; by the end of [chapter 11](../book/11-milestone-9-polish-and-driver.md) you've read everything the engine knows how to do today.

### 4. Build the CLI binary

```bash
make build
./godb
```

Prints `godb: SQLite-inspired database engine in Go` and exits. The CLI subcommands (`exec`, `query`, `inspect`, `check`, the interactive shell) all land at M10.

### 5. Internal-packages sandbox (optional)

If you want to call the engine's internal layers directly (for learning, or to extend the engine itself), see the [snapshot in `current-state.md`](current-state.md). Internal APIs can change without warning between milestones; the public `pkg/godb` API is the stable surface.

## When to read what

- **Just want to use it?** [`embedded-api.md`](embedded-api.md). Then `pkg/godb` Godoc when published.
- **Want to learn how databases work?** Start with the [book introduction](../book/00-introduction.md). It assumes Go knowledge and zero database-internals knowledge.
- **Want to understand a specific decision?** Browse the [ADR index](../adr/).
- **Want to know what's deliberately *not* being built?** Read the [PRD](../prd.md), specifically the Vision/Non-vision and Out-of-scope sections.

## What lands here next

As features land, new pages join this directory:

- M10: `cli.md` — the interactive shell, all the `inspect` subcommands, the `check` validator.
- v0.2: `transactions.md`, `migrations.md`.

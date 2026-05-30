# Chapter 10 — The Loop Closes: Public API + Planner + Executor (M8)

## Where we are

By the end of [Chapter 09](09-milestone-7-sql-parser.md) the engine could *read* SQL: `CREATE TABLE`, `INSERT`, `SELECT [WHERE id = ?]` all parsed into a typed AST. What it couldn't do was *execute* any of that. Every layer below — storage (M1), records (M2), slotted pages (M3), B+tree (M4–M5), catalog (M6) — already worked in isolation; the SQL frontend (M7) sat on top of them with no bridge. The bridge is M8.

M8 ships three things in one milestone:

1. **A planner** ([`internal/planner/`](../../internal/planner/)) — turns a parser AST into an executable plan.
2. **An executor** ([`internal/exec/`](../../internal/exec/)) — runs the plan against the catalog and pager.
3. **A public Go API** ([`pkg/godb/`](../../pkg/godb/)) — what callers actually import.

After M8 lands, a Go program does this and it works:

```go
db, _ := godb.Open("app.godb")
defer db.Close()

db.Exec(ctx, "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL)")
db.Exec(ctx, "INSERT INTO users VALUES (?, ?)", 1, "Felipe")

rows, _ := db.Query(ctx, "SELECT * FROM users WHERE id = ?", 1)
defer rows.Close()
for rows.Next() {
    var id int64
    var name string
    rows.Scan(&id, &name)
    fmt.Println(id, name) // 1 Felipe
}
```

That `1 Felipe` print is the loop closing. Every layer this book has covered — page I/O, fixed-size pages, slotted layouts, B+tree splits, catalog lookups, SQL parsing — runs to produce it. The chapter you're reading is the chapter where GoDB stops being "a stack of layers that work in isolation" and becomes "an embedded database."

M8 also closes one outstanding gap from M6: `Catalog.SetTableRoot` was in-memory-only because the B+tree had no in-place cell update primitive. M8 adds `btree.UpdateCellSameSize` (a deliberately constrained update that only works when the new payload's encoded length matches the old) and wires `SetTableRoot` to persist root drift through it. Without this fix, M8's executor would silently lose data the moment a table tree's root split.

## Foundation

### Three layers, one round trip

A SQL string goes through three internal transformations on its way to a result:

```
"SELECT * FROM users WHERE id = ?"   (raw bytes)
            ↓ sql.Parse
*sql.SelectStatement                  (AST — what the user said)
            ↓ planner.Plan
*planner.PrimaryKeyLookupPlan         (Plan — what the executor will do)
            ↓ executor.RunQuery
*exec.Rows                            (Result — the data, materialized)
            ↓ pkg/godb.Rows
caller's *int64, *string, ...         (typed scan into the user's vars)
```

Each transformation is a separate package. The split is deliberate:

- **Parser** doesn't know about tables, columns, or types. It just produces an AST.
- **Planner** doesn't do I/O. It consults the catalog (read-only) to validate references, then produces a plan tree the executor walks.
- **Executor** doesn't parse or plan. It takes a plan plus arguments and dispatches to btree / catalog operations.
- **Public API** doesn't do any of the above directly. It orchestrates parse → plan → execute and adapts the result to a Go-friendly `Rows`/`Result` interface.

This is the textbook database split: parsing, planning, execution. It matters because each layer has a different reason to change. Adding a new comparison operator changes the parser and planner. Adding a buffer pool changes the executor. Adding a `database/sql/driver` wrapper changes the public API. Keeping them separate means each change is bounded.

### What a planner is and why one exists

The most direct way to wire the parser to the executor would be: walk the AST directly inside the executor. For SELECT, peek at `Where`, descend into the BinaryExpr, pull out the column name, look up the schema. That works for a grammar this small. It also bakes execution policy into the parser's data structures, makes it hard to swap execution strategies later, and conflates two different concerns: *what does this query mean* (the planner's job) and *how do we run it* (the executor's job).

The planner sits between them as a translation step:

- It **validates** the query against the schema. If you `SELECT bogus FROM users`, the planner catches it before any disk I/O happens; the error message can point at the column name.
- It **chooses** an execution strategy. `SELECT * FROM users WHERE id = ?` becomes a `PrimaryKeyLookupPlan` (one tree.Get); `SELECT * FROM users` becomes a `TableScanPlan` (one tree.Scan); `SELECT name FROM users` wraps either in a `ProjectionPlan`.
- It **enforces v0.1 limitations**. The parser accepts `WHERE name = ?` (any column on the left of `=`); the planner narrows that to "must be the primary key" and returns `ErrWhereOnlyPrimaryKey` for anything else. That's the right place for the rejection — early enough to give the user a clean error, late enough that the parser doesn't have to know about which columns are PKs.

In M8 the planner has five plan types: `CreateTablePlan`, `InsertPlan`, `TableScanPlan`, `PrimaryKeyLookupPlan`, `ProjectionPlan`. That's enough for the v0.1 SQL surface. Future milestones will add more (Filter for non-PK WHERE, NestedLoopJoin for joins, GroupBy for aggregates) without changing the planner's shape — each new plan type is one new struct and one new branch in `Planner.Plan`.

### What an executor is and how materialization vs streaming plays out

The executor takes a plan plus a `[]any` of caller-supplied args and produces a `Result` or `*Rows`:

```go
func (e *Executor) Run(plan planner.Plan, args []any, sqlSrc string) (Result, error)
func (e *Executor) RunQuery(plan planner.Plan, args []any) (*Rows, error)
```

`Run` is for plans that don't return rows (CREATE TABLE, INSERT). `RunQuery` is for SELECT plans. The split mirrors `database/sql`'s `Exec` vs `Query`.

The interesting choice for `RunQuery`: should the result stream lazily (the executor returns an iterator that produces rows on demand as `Rows.Next` is called), or materialize eagerly (the executor walks the whole tree, accumulates every row into a slice, returns the slice wrapped in `Rows`)?

Streaming is the "right" answer for production engines. A `SELECT *` on a billion-row table shouldn't allocate a billion-row slice. The user can stop reading after a few hundred rows; lazy iteration only does the work that's actually consumed.

Streaming is also more code. It requires a `btree.Cursor` (a stateful walker that survives across calls), and the cursor has to handle leaf-chain transitions in the middle of an iteration. v0.1 doesn't have one. Building one is M5+ territory we deferred.

Materialization is the v0.1 choice. `Tree.Scan(fn)` walks every row via callback; the executor's callback appends each decoded `[]record.Value` to `Rows.Values`. `Rows.Next` is then a slice walk. Memory cost is bounded by the result set's size — which, for v0.1's supported queries (no aggregates, no joins, no large-result-set workloads), is fine.

This trade-off is documented in [ADR-0016](../adr/0016-rows-materialization.md). The public `Rows` API is unchanged when v0.2 switches to streaming; only the implementation moves.

### Parameter binding and Scan: where types meet Go

Two type-mapping problems live at the executor / public-API boundary:

1. **Bind direction (Go → SQL)**: the user calls `db.Exec(ctx, "INSERT INTO users VALUES (?, ?)", 1, "Felipe")`. The executor needs to convert `1` (a Go `int`) and `"Felipe"` (a Go `string`) into `record.Value`s the row encoder understands.
2. **Scan direction (SQL → Go)**: the user calls `rows.Scan(&id, &name)` with `*int64` and `*string`. The Rows implementation needs to assign the appropriate `record.Value` into each destination.

In both directions, v0.1 is strict:

- **Bind**: `int`, `int32`, `int64` → `record.Int`; `string` → `record.Text`; `bool` → `record.Bool`; `nil` → `record.Null()`. Anything else (`float64`, `[]byte`, a custom type) returns `ErrUnsupportedArgType`. No implicit conversion from `float64` to `int64` — explicit cast or error.
- **Scan**: `*int64` ← `KindInteger`; `*string` ← `KindText`; `*bool` ← `KindBoolean`. NULL into any of those returns `ErrScanNullIntoNonNullable`. `*any` (i.e. `*interface{}`) takes any kind, including NULL (becomes `nil`). Type mismatches return `ErrScanTypeMismatch`.

`database/sql` allows broader conversions (e.g. scan a `string` column into `*int`, parsing the string). v0.1 doesn't, deliberately. The cost of permissive types is unclear error messages when something silently coerces wrong; the cost of strict types is occasional explicit casting in user code. v0.1 picks strict; v0.2+ can revisit.

### The same-size cell update: how INSERT survives root growth

There's one subtle correctness story buried under "INSERT works": when a table's tree root grows via a root split (M5's last big invariant change), the *table tree's* `RootPageID` changes. The catalog has a row that records that ID. If the catalog row doesn't get updated, the next time we open the database the catalog will hand back a stale RootPageID — and we'll be looking at what used to be the root but is now just one leaf among many. Silent data loss.

In M6 we noted this as a known gap and deferred. M8 closes it.

The fix has two parts:

1. **`btree.UpdateCellSameSize(pg, key, newPayload)`** — find the cell with `key`, verify `len(newPayload) == len(existingPayload)`, overwrite the payload bytes in place. Returns `ErrSizeChanged` on size mismatch, `ErrKeyNotFound` if absent. The same-size constraint is real and documented in [ADR-0018](../adr/0018-btree-update-cell-same-size.md) — relaxing it requires either delete+reinsert (no delete primitive in v0.1) or growing the cell (rarely fits in the slot it lives in).
2. **`Catalog.SetTableRoot`** re-encodes the catalog object with the new RootPageID and calls `tree.UpdateCellSameSize` on the catalog tree. The catalog row's encoded layout puts RootPageID at a fixed offset (per [ADR-0014](../adr/0014-catalog-row-encoding.md)); re-encoding with only that field changed produces an identical-length payload by construction.

The executor then, after every INSERT: read `tree.RootPageID()`, compare to the catalog's stored root; if it changed, call `catalog.SetTableRoot(name, newRoot)`. The change is now durable; close/reopen recovers correctly.

There's a regression test (`TestExecInsertGrowsTableRootIsPersisted`) that inserts 500 rows with 400-byte payloads — guaranteed to root-split — then closes the database, reopens it, queries the last row, and confirms it's still retrievable. Before this commit cycle, that test would fail silently.

### Why `Tx` exists but `Begin` always returns an error

The `database/sql`-shaped public API has `db.Begin() *Tx` and `Tx.Exec/Query/Commit/Rollback`. Users who've never used GoDB before reasonably expect that shape to work.

But v0.1 has no rollback journal, no WAL, no buffer pool. Transactions in the real sense — atomic commit, real rollback, isolation between concurrent operations — depend on all three. Writes in v0.1 are autocommit + `fsync`. Pretending to have transactions when none of the machinery exists would mislead callers.

GoDB v0.1 picks the honest path ([ADR-0017](../adr/0017-no-transactions-in-v0-1.md)): `Tx` exists as a type with the expected methods, but `DB.Begin(ctx)` always returns `(nil, ErrTransactionsUnsupported)`. The Tx type's methods are stubs returning the same sentinel — they're declared so v0.2 can implement them without expanding the public API surface, but in v0.1 they're unreachable because Begin never hands out a Tx.

Code that needs transactions fails loudly at Begin with a clear message. Code that uses `db.Exec` directly works fine in autocommit mode and stays forward-compatible when v0.2 lands real transactions.

## Decisions

- **Three layers**: planner, executor, public API. Each in its own package; clear contracts between them. The planner doesn't do I/O; the executor doesn't parse; the public API doesn't dispatch plan nodes directly.
- **Five plan types in v0.1** (CreateTablePlan, InsertPlan, TableScanPlan, PrimaryKeyLookupPlan, ProjectionPlan) — just enough for the supported SQL.
- **WHERE only on the primary key**. The parser is permissive; the planner narrows. `ErrWhereOnlyPrimaryKey` is the marker. v0.2 adds `Filter` for non-PK predicates.
- **`SELECT *` produces a bare `TableScanPlan`** (no wrapping projection). Named columns wrap with `ProjectionPlan`. Avoids needless tree walks at execution time.
- **Materialized Rows in v0.1** ([ADR-0016](../adr/0016-rows-materialization.md)). Streaming in v0.2 without an API change.
- **`Begin` returns `ErrTransactionsUnsupported`** ([ADR-0017](../adr/0017-no-transactions-in-v0-1.md)). Tx type exists for forward compat.
- **`btree.UpdateCellSameSize` primitive** ([ADR-0018](../adr/0018-btree-update-cell-same-size.md)) closes the M6 SetTableRoot persistence gap. Constrained to same-size updates because that's what the catalog needs and what the v0.1 btree can safely offer without a delete primitive.
- **Strict Scan types**: `*int64`, `*string`, `*bool`, `*any`. No implicit conversions like `database/sql` allows. Less surprise.
- **Strict bind types**: `int/int32/int64`, `string`, `bool`, `nil`. Same principle.
- **Shallow context support**: `ctx.Err()` checked at entry; deeper cancellation isn't honored in v0.1. Most operations are short-lived against a single pager; long-running queries don't yet exist.

## The code

### Planner

[`internal/planner/plan.go`](../../internal/planner/plan.go) defines the `Plan` interface (an empty marker method `planNode()`) and the five concrete plan types. Each plan type is a plain struct holding the resolved schema, table name, root page id (resolved at plan time so the executor doesn't have to consult the catalog twice), and any plan-specific fields (e.g. `ProjectionPlan.Output` is a slice of schema-column indices).

[`internal/planner/planner.go`](../../internal/planner/planner.go) holds the `Planner` struct (a tiny wrapper over a `*catalog.Catalog`) and `Plan(stmt)` dispatch. Three helper functions — `planCreateTable`, `planInsert`, `planSelect` — do the work. Each consults the catalog for the table; each validates schema-shape constraints (no PK, multiple PKs, unknown column names, value-count mismatches); each produces the most specific plan for the input.

The planner's tests live in `planner_test.go`: 18 tests covering supported shapes, rejection paths, and the SELECT-*-vs-named-columns distinction.

### Executor

[`internal/exec/executor.go`](../../internal/exec/executor.go) holds the `Executor` struct (a pager + catalog handle) and the two entry points:

```go
func (e *Executor) Run(plan planner.Plan, args []any, sqlSrc string) (Result, error)
func (e *Executor) RunQuery(plan planner.Plan, args []any) (*Rows, error)
```

`Run` dispatches via type-switch to `runCreateTable` / `runInsert`. `RunQuery` dispatches to `runTableScan` / `runPKLookup` / `runProjection`. Each runs about 20 lines of glue plus calls into btree / catalog / record.

The interesting parts:

- **`bindArgs(exprs, args)`** walks the plan's value expressions, consuming user args at each `*sql.Placeholder`. Literals (`*sql.IntegerLiteral`, etc.) pass through unchanged. Identifiers in value position are rejected (the parser accepts them syntactically; the executor doesn't yet support column-to-column equality).
- **`runInsert`** is where the root-drift handling lives: after a successful `tree.Insert`, compare `tree.RootPageID()` to `info.RootPageID`. If different, call `e.catalog.SetTableRoot(name, newRoot)` — which now (per the [btree+catalog commit](../../internal/btree/leaf.go)) actually persists.
- **`runProjection`** runs the inner plan recursively and picks out columns by index. The output's `Columns` is the projection's `Names`, not the underlying schema's names.

Tests live in `executor_test.go`: 13 tests, including the headline `TestExecInsertGrowsTableRootIsPersisted` that proves the persistence story end-to-end.

### Public API

Four files in [`pkg/godb/`](../../pkg/godb/):

- [`godb.go`](../../pkg/godb/godb.go) — `DB` struct, `Open`, `Close`. Open wires up the pager, catalog, planner, and executor. The `mapInternalErr` function translates internal-package sentinels (catalog.ErrTableExists, planner.ErrColumnNotFound, exec.ErrTypeMismatch, etc.) into the public `godb.ErrXxx` sentinels so callers always dispatch on stable identifiers.
- [`exec.go`](../../pkg/godb/exec.go) — `DB.Exec`. Path: `ctx.Err()` → `sql.Parse` → `planner.Plan` → `executor.Run` → public `Result`.
- [`query.go`](../../pkg/godb/query.go) — `DB.Query` (same path, ending at `executor.RunQuery`), plus `Rows.Next` / `Scan` / `Columns` / `Err` / `Close`. The strict scan-type rules live in `scanValueInto`.
- [`tx.go`](../../pkg/godb/tx.go) — `Tx` type with all the expected methods, all returning `ErrTransactionsUnsupported`. `DB.Begin` returns `(nil, ErrTransactionsUnsupported)`.

Plus [`errors.go`](../../pkg/godb/errors.go) (17 exported sentinels) and [`options.go`](../../pkg/godb/options.go) (the functional-options pattern with `WithCreateIfMissing`).

The public API has 19 integration tests in `godb_test.go` that drive the full pipeline (Open → CREATE TABLE → INSERT → SELECT → Scan → Close → reopen → re-query). The headline is `TestExecCreateInsertSelectFullLoop`; the persistence-proof is `TestExecInsertGrowsTableRootSurvivesReopen`.

## Tests as proof

A few worth pointing at:

- **`TestExecCreateInsertSelectFullLoop`** (in [`pkg/godb/godb_test.go`](../../pkg/godb/godb_test.go)) is the headline — the full SQL-string-to-Scan loop in one ~30-line test. If this passes, the engine works end-to-end.
- **`TestExecInsertGrowsTableRootSurvivesReopen`** is the M6→M8 persistence gap proof, run through the public API. It inserts 500 400-byte rows (guaranteed root split), closes the DB, opens a fresh handle, queries the last row, asserts retrieval. Before commit `feat(btree+catalog): same-size cell update primitive, SetTableRoot now persists` this test would fail silently.
- **`TestRowsScanIntoInterface`** pins the `*any` NULL-handling story (NULL becomes nil; other kinds become their Go-typed value).
- **`TestRowsScanNullIntoNonNullableReturnsError`** is the converse: NULL into `*string` returns `ErrScanNullIntoNonNullable`. Strict-typing honesty.
- **`TestQueryNonPKWhereReturnsErrWhereOnlyPrimaryKey`** proves the planner's WHERE-only-PK constraint surfaces through the public sentinel — callers can `errors.Is(err, godb.ErrWhereOnlyPrimaryKey)` and dispatch.
- **`TestBeginReturnsErrTransactionsUnsupported`** is the honest "v0.1 doesn't pretend to have transactions" guarantee.
- **`TestContextCancellationOnExec`** shows the (shallow) context-cancellation support.

The full M8 test count: 6 new btree, 1 changed catalog, 18 planner, 13 executor, 19 pkg/godb — about 57 new or rewritten tests, all passing race-clean.

## What this layer cannot do yet

- **No streaming.** Rows are materialized. v0.2 with the buffer pool + btree cursor.
- **No transactions.** Begin returns the sentinel. v0.2.
- **No `UPDATE`/`DELETE`/`ALTER TABLE`/`DROP TABLE`.** Parser rejects; planner doesn't need them. v0.2+.
- **No non-primary-key `WHERE`.** Planner rejects with `ErrWhereOnlyPrimaryKey`. v0.2 adds `TableScan + Filter`.
- **No compound predicates with `AND`/`OR`.** Parser rejects. v0.2+.
- **No comparison operators other than `=`.** v0.2.
- **No `JOIN`/`GROUP BY`/`ORDER BY`/`LIMIT`/`HAVING`.** v0.3+.
- **No `database/sql/driver` wrapper.** Likely M9 or later. The shape is database/sql-compatible-ish so the wrapper will be small.
- **No buffer pool.** v0.2.
- **No concurrent operations on the same DB from multiple goroutines.** v0.1 single-writer model. The pager serializes; the public API doesn't add concurrency.
- **No prepared statements.** Every Exec/Query re-parses. v0.2 if there's a real need.
- **No implicit type conversions in Scan.** Strict types. v0.2+ may add a `database/sql`-style conversion option.
- **No migrations package.** Later milestone.
- **No CLI commands.** M10.

Each has a milestone home.

## Further reading

- The Go [`database/sql`](https://pkg.go.dev/database/sql) package documentation — the conventional shape `godb.DB` mirrors.
- SQLite's [query planner overview](https://www.sqlite.org/queryplanner.html) — for the planner side, much more sophisticated than v0.1's but the same shape.
- Postgres's docs on the [planner/optimizer](https://www.postgresql.org/docs/current/planner-optimizer.html) — the production-grade reference for what these layers can grow into.
- *Database Internals* (Petrov), chapter 7 (Query Processing) — the textbook treatment of parser → planner → executor.

## Where the next chapter picks up

M9 — polish, integration, and the `database/sql/driver` wrapper. With M8 shipped, the engine has a working public API. M9 hardens it for use: comprehensive multi-table integration tests, an optional `database/sql/driver` wrapper (so users can write `sql.Open("godb", path)` and get a real `*sql.DB`), better error context (wrap with source SQL + statement number for debugging), and any cleanup needed before M10 (CLI) starts building on top.

The exact M9 scope is fluid — if the v0.1 release feels close after M9's polish work, M9 may also start integration-testing toward M11. That's where the next chapter picks up.

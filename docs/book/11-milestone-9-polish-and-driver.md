# Chapter 11 — Polish and the database/sql Driver (M9)

## Where we are

By the end of [Chapter 10](10-milestone-8-public-api.md) the engine had a working public Go API — `godb.Open`, `db.Exec`, `db.Query`, `Rows.Scan` — and the full SQL→storage loop closed. A user could import `pkg/godb` and run real queries against a real `.godb` file.

What they couldn't do: plug GoDB into the rest of the Go ecosystem. Many Go applications, libraries, and tutorials assume `database/sql`. There's no `sql.Open("godb", ...)`. The native API works, but users coming from elsewhere have to learn a new shape before they can do anything.

M9 fixes that. It adds:

1. **A `database/sql/driver` wrapper** (`pkg/driver`) so users can `sql.Open("godb", path)` and get a real `*sql.DB`.
2. **`godb.SQLError`** as a type alias for the internal parser-error type, so callers don't have to import `internal/sql` to `errors.As` for source positions.
3. **`godb.StatementError`** as a wrapper that carries the source SQL alongside the failure, so log lines are self-contained.
4. **`DB.Sync()`** for an explicit durability checkpoint without closing.
5. **Multi-table integration tests** through the public API.

This is a polish milestone. The loop already worked; M9 gives it enough public API shape that M10 (CLI) can build on top and M11 (v0.1 release) can ship.

## Foundation

### What `database/sql` is and why every Go database eventually has to deal with it

`database/sql` is the Go standard library's database abstraction. It's not a database — it's a glue layer. It defines a small interface (`database/sql/driver.Driver`, `Conn`, `Stmt`, `Rows`, `Result`, `Value`) and provides a friendlier consumer-facing API (`*sql.DB`, `*sql.Rows`, `sql.NullString`, prepared statement caching, connection pooling, `errors.As(*sql.SQLError)`, etc.) on top.

Every database driver in the Go ecosystem implements that small interface, registers itself via `sql.Register("name", driver)`, and from then on consumers can write:

```go
import (
    "database/sql"
    _ "github.com/some/driver"  // registers under "name"
)

db, _ := sql.Open("name", "dsn")
```

…and use the standard library's `*sql.DB`. The driver does the actual work; `database/sql` provides the user-facing API and the connection pool.

This convention is so deeply embedded in Go database tooling that not playing along has real costs. A SQL package without a `database/sql/driver` can't be used with the standard query builders, with the standard migration tools, with the standard test fixtures. M9 plays along.

### The adapter pattern: wrap the native API, don't reimplement it

There were two paths to satisfy `database/sql/driver`:

1. **Make `pkg/godb` *be* the driver.** Restructure `godb.DB` to implement `driver.Conn` directly; the native API becomes the driver-shaped one with hand-written helpers for the Go-friendly part on top.
2. **Keep `pkg/godb` native; add `pkg/driver` as a thin wrapper.** The native API stays small and typed; the driver wraps it, translating between `driver.Value` and Go-friendly types.

GoDB picks path 2 ([ADR-0019](../adr/0019-driver-wraps-godb.md)). The reason: `database/sql/driver`'s interface carries historical idioms — `NumInput()`, `driver.Value` (a typed `any`), the `driver.ErrSkip` protocol for unsupported features — that don't fit a Go-native experience. If `pkg/godb` had to satisfy that interface directly, its native shape would be compromised.

So we have two layers:

- **`pkg/godb`** — Go-native ergonomics. Typed errors, `errors.Is` dispatch, `Rows.Scan(*int64, *string)` directly. What you'd write for yourself.
- **`pkg/driver`** — `database/sql/driver` interface implementation. Wraps `pkg/godb`. Translates value types. Registers as `"godb"` in `init()`.

Each has a different audience and a different evolution path. The driver can grow new optional `database/sql/driver` interfaces (`Pinger`, `ColumnType`, `NamedValueChecker`) without touching `pkg/godb`. `pkg/godb` can change its internal binding rules without breaking the driver.

This is the [adapter pattern](https://en.wikipedia.org/wiki/Adapter_pattern), applied at the package level. Most well-designed Go database libraries that do `database/sql` end up with this shape; the [modernc.org/sqlite driver](https://gitlab.com/cznic/sqlite) is a much larger but recognizable example.

### `database/sql` value types vs GoDB's

`database/sql/driver.Value` is a typed `any` accepting six types: `int64`, `float64`, `bool`, `[]byte`, `string`, `time.Time`, plus `nil`. GoDB v0.1's record kinds are four: `INTEGER` (int64), `TEXT` (string), `BOOLEAN` (bool), `NULL`.

So `pkg/driver` accepts `int64`/`string`/`bool`/`nil` and passes them through to `pkg/godb`. It rejects `float64`, `[]byte`, and `time.Time` with clear errors naming the v0.1 limitation. v0.2 will add column types for those (FLOAT, BLOB, TIMESTAMP) — at which point the driver translation gets richer, but the public surface doesn't change for the existing four kinds.

`time.Time` deserves a special note: it's the standard Go type for SQL timestamps, and `database/sql` users will reflexively reach for `db.Exec(..., time.Now())`. Today GoDB rejects this with `"time.Time args are not supported in GoDB v0.1 (no TIMESTAMP column type yet)"`. A clear message; not surprising once you read it. v0.2.

### What polish actually means

This milestone exists because v0.1 isn't quite ready to release without the polish. Specifically:

- **Error context.** A failed `db.Exec("UPDATE users SET name = 'x'")` should tell you both *what went wrong* and *what SQL you tried to run*, in one log line. `StatementError` wraps the underlying error with the source SQL so the user sees both at the call site.
- **Source positions without internal imports.** The M8 tutorial had to tell users to import `internal/sql` to `errors.As` for `*sql.SQLError`. That's a wart. The type alias drops the workaround.
- **Mid-life flushes.** `Close` syncs. But long-running processes (a future CLI shell, a stress harness) want a checkpoint *without* tearing down the DB. `DB.Sync()` is that primitive.
- **Multi-table coverage.** Every existing `pkg/godb` test uses one table. The catalog layer's tests cover multi-table at the internal layer, but we want pin-it-through-the-public-API coverage for catalog row routing under multi-table loads.

None of these are "features" in the milestone sense — none of them let the user do something they couldn't do before. They're the difference between "the engine works" and "the engine works in the way a Go user would expect it to work." That's polish.

## Decisions

- **`pkg/driver` wraps `pkg/godb`**, not the other way around. ADR-0019.
- **`Conn.Begin` returns `godb.ErrTransactionsUnsupported`** — same as the native `DB.Begin`. Consistent with [ADR-0017](../adr/0017-no-transactions-in-v0-1.md). We don't return `driver.ErrSkip` (that protocol is for chained drivers; not us).
- **No parse caching in `Stmt`.** Each `Stmt.Exec` re-parses the SQL through `pkg/godb`. v0.2+ can add a parse cache if profiling shows it's a hot path.
- **`Stmt.NumInput()` returns -1.** Tells `database/sql` not to validate arg count up-front; the planner does it.
- **Strict v0.1 type mapping.** `float64` / `[]byte` / `time.Time` args are rejected with messages naming the eventual column-type story. No silent conversions.
- **`godb.SQLError` is a type alias**, not a wrapping struct. Same underlying type as `internal/sql.SQLError`; `errors.As` works transparently.
- **`StatementError.Unwrap` returns the wrapped error**, so `errors.Is(err, godb.ErrXxx)` and `errors.As(err, &godb.SQLError{})` both traverse the wrap. The sentinel dispatch story stays intact.
- **`DB.Sync()` delegates to `catalog.Sync()`** which refreshes the header and `fsync`s the file. Idempotent. Returns `ErrDatabaseClosed` post-`Close`.
- **Multi-table tests live alongside `godb_test.go`** in a separate `integration_test.go` file in the same package, not in a dedicated `internal/integration/` package. They drive through the public API exclusively; same-package access lets them use the test fixtures (`tempDB`, `ctx`) without re-export.

## The code

### `pkg/driver/driver.go`

The wrapper is ~260 lines. Top-down:

- `init()` registers the driver as `"godb"`.
- `Driver.Open(name)` calls `godb.Open(name)` and wraps in a `Conn`.
- `Conn.Prepare(query)` returns a `Stmt` holding the SQL text — no parse, just remember it.
- `Conn.Close()` closes the underlying `*godb.DB`.
- `Conn.Begin()` returns `godb.ErrTransactionsUnsupported`.
- `Stmt.Exec` / `Query` delegate to `ExecContext` / `QueryContext` via `valuesToNamed` (legacy → context-aware shape conversion).
- `Stmt.ExecContext(ctx, args)` calls `namedToAny` (which validates v0.1's value-type whitelist) then `s.conn.db.Exec(ctx, s.sql, goArgs...)` and wraps the result.
- `Stmt.QueryContext(ctx, args)` similarly delegates to `s.conn.db.Query` and wraps in `*Rows`.
- `Rows.Next(dest []driver.Value)` calls `r.inner.Next()` (the underlying `*godb.Rows`), scans into a `[]any` of holders (`scanDest[i] = &holders[i]`), then translates each holder into a `driver.Value` via `toDriverValue`.
- `Rows.Close()` and `Rows.Columns()` delegate.
- `Result` is just two int64s.

The value mapping is centralized in `namedToAny` and `toDriverValue`, two small functions that switch on type. `namedToAny` is the place where `time.Time` / `float64` / `[]byte` get the friendly rejection.

### `pkg/godb` additions

In [`pkg/godb/errors.go`](../../pkg/godb/errors.go):

```go
type SQLError = internalsql.SQLError

type StatementError struct {
    SQL string
    Err error
}

func (e *StatementError) Error() string {
    return fmt.Sprintf("godb: error in %q: %v", e.SQL, e.Err)
}

func (e *StatementError) Unwrap() error { return e.Err }
```

Plus a small `wrapStatementErr(sql, err)` helper that returns nil for nil input and a `*StatementError` otherwise. `DB.Exec` and `DB.Query` route every non-nil error through it on the way out.

In [`pkg/godb/godb.go`](../../pkg/godb/godb.go), `DB.Sync()` is ~15 lines: grab the mutex, check `guardOpen`, call `db.catalog.Sync()` (which refreshes the header and `fsync`s).

### `pkg/godb/integration_test.go`

Five scenarios, all driven through `DB.Exec` / `DB.Query`. The most interesting ones:

- `TestIntegrationOneTableGrowsWhileOthersStaySmall` — proves that growing one table's tree doesn't disrupt the catalog's tracking of the other tables. The big table (500 rows × 200 bytes) is guaranteed to root-split. The small tables stay tiny. After the dust settles, all three tables' counts are correct.
- `TestIntegrationFiveTableWorkloadSurvivesReopen` — 5 tables with varying row counts; close, reopen, every table's count matches; spot-check one PK lookup per table. The headline cross-table reopen test.
- `TestIntegrationInterleavedCreateAndInsertMixedOrder` — interleave `CREATE TABLE A` / `INSERT A` / `CREATE TABLE B` / `INSERT A+B`. Pins that catalog-tree writes and table-tree writes interleave cleanly.

## Tests as proof

Per-package new test counts:

- `pkg/godb` — 4 new (Sync ×2, StatementError ×1, SQLError alias ×1) + 5 new integration tests.
- `pkg/driver` — 12 new (Open/Close, Exec, Query, Prepare, Begin, NullString, ctx cancel, errors.Is forwarding, unsupported SQL, float rejection, reopen, concurrent reads).

A few worth pointing at specifically:

- **`TestDriverExecCreateInsert`** is the headline driver test — `database/sql.Exec` of CREATE TABLE + INSERT, then `Result.LastInsertId()` and `RowsAffected()` populate. If this passes, the driver loop works.
- **`TestDriverScanWithSqlNullString`** proves the standard library's nullable types work through the wrapper. Users coming from the `database/sql` ecosystem expect `sql.NullString`, and it works.
- **`TestDriverConcurrentReads`** confirms that the database/sql connection pool can run reads from multiple goroutines safely against a seeded DB.
- **`TestStatementErrorWrapsSQLAndPreservesSentinel`** is the proof that the error wrapping doesn't break sentinel dispatch — `errors.Is(err, godb.ErrUnsupportedSQL)` still works after the `StatementError` wrap.
- **`TestIntegrationOneTableGrowsWhileOthersStaySmall`** is the proof that multi-table catalog routing survives root growth.

## What this layer cannot do yet

- **No parse caching in `Stmt`.** Each `Stmt.Exec` re-parses. v0.2 if profiling shows it matters.
- **No optional `database/sql/driver` interfaces yet** (`ColumnType`, `Pinger`, `NamedValueChecker`, `SessionResetter`). Easy to add when ecosystem demand arrives.
- **No safe multi-conn write concurrency.** Each `*godb.DB` is a separate pager; multiple driver Conns mean multiple pagers; concurrent writes from multiple conns aren't safe. Users who need strict single-writer semantics call `sql.DB.SetMaxOpenConns(1)`. v0.2's buffer pool can revisit.
- **No `time.Time` / `float64` / `[]byte` value support.** Rejected at the driver layer with clear messages. v0.2+ adds the column types (FLOAT, BLOB, TIMESTAMP).
- **No multi-statement `Exec`** (running a schema file). Defer to v0.2 alongside the transaction story.
- **No mid-query cancellation.** Only `ctx.Err()` at entry is honored. True cancellation requires the executor to check ctx periodically.
- **No CLI.** M10.

Each has a milestone home.

## Further reading

- The Go [`database/sql`](https://pkg.go.dev/database/sql) and [`database/sql/driver`](https://pkg.go.dev/database/sql/driver) package documentation — the canonical reference for the interfaces this chapter walks through.
- The [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) source — a high-quality pure-Go SQLite driver, much larger than GoDB's but the layering is recognizable.
- The [`go-sql-driver/mysql`](https://github.com/go-sql-driver/mysql) driver — another good study in `database/sql/driver` implementation.

## Where the next chapter picks up

M10 — CLI. With the public API stable and the `database/sql` driver in place, the CLI is mostly UI over what already exists. The plan covers an interactive shell, `exec <file.sql>` for running scripts, `query "<sql>"` for one-shots, `inspect header/page/tree` for poking at internals, and `check` for validation. The chapter focuses on CLI design choices (one binary with subcommands; how the interactive shell handles partial SQL; how to make `inspect` outputs useful without being noisy) rather than rehashing engine internals.

That's where the next chapter picks up.

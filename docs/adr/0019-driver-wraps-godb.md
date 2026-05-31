# ADR-0019: `pkg/driver` wraps `pkg/godb`; the layering is composition, not reimplementation

- Status: Accepted
- Date: 2026-05-30
- Tags: api, layering, ecosystem

## Context

`database/sql/driver` defines a specific interface shape: `Driver.Open(name) Conn`, `Conn.Prepare(query) Stmt`, `Stmt.Exec(args) Result` / `Stmt.Query(args) Rows`, and so on. The value type is `driver.Value` (a typed `any` accepting `int64`/`float64`/`bool`/`[]byte`/`string`/`time.Time`/`nil`). Connections are pooled by `database/sql.DB`; each pooled conn calls `Driver.Open` separately. There's an established convention of returning `driver.ErrSkip` to mean "this driver doesn't handle that; try the next" — useful for some interfaces, irrelevant for ours.

GoDB's native API (`pkg/godb`) has a different shape: `Open(path) *DB`, `DB.Exec(ctx, sql, args...)`, `DB.Query(...) *Rows`, `Rows.Scan(*int64, *string, ...)`. It's smaller, Go-native, and doesn't carry `database/sql`'s historical idioms (`NumInput()`, `driver.Value` conversions, the `driver.ErrSkip` protocol).

We want both. Native API for users who want Go-flavored ergonomics; `database/sql/driver` compatibility for users who want to plug into the ecosystem (existing apps, libraries that assume `database/sql`, tutorials, the standard connection pool).

There are two paths to satisfy both:

1. **`pkg/godb` IS the driver.** Make `*godb.DB` implement `database/sql/driver.Driver` directly — the native API is the driver-shaped one, with hand-written helpers for the Go-friendly part on top.
2. **`pkg/driver` WRAPS `pkg/godb`.** Keep `pkg/godb` native and shaped for ergonomic Go use. Write a small `pkg/driver` package that translates between `database/sql/driver`'s shapes and `pkg/godb`'s.

## Decision

Path 2. `pkg/godb` stays the native API. `pkg/driver` is a thin wrapper:

- `driver.Conn` holds a `*godb.DB`.
- `driver.Stmt` holds the SQL text + a `*driver.Conn` reference; `Exec` / `Query` route through `*godb.DB.Exec` / `*godb.DB.Query`.
- `driver.Rows` wraps `*godb.Rows`. The driver's `Next(dest []driver.Value) error` translates from `godb.Rows.Scan(*any, *any, ...)` into the `driver.Value` slice.
- Value-type translation is centralized in two helpers (`namedToAny`, `toDriverValue`); v0.1 supports `int64`/`string`/`bool`/`nil` and rejects `float64`/`[]byte`/`time.Time` with clear errors.
- `init()` registers the driver as `"godb"`.

The two packages have orthogonal evolution paths:

- `pkg/godb` can change its internal binding rules, add new column kinds, expose new types — without affecting the driver as long as the underlying contract (Exec returns Result, Query returns Rows with Scan into typed dests) stays put.
- `pkg/driver` can implement new `database/sql/driver` optional interfaces (`ColumnType`, `Pinger`, `NamedValueChecker`, `SessionResetter`) as ecosystem demand arrives, without touching `pkg/godb`.

## Consequences

**Enables.** Two clean audiences, two clean APIs. `pkg/godb`'s shape is what a Go developer would write for themselves — small, typed, error sentinels you can switch on. `pkg/driver`'s shape is what `database/sql` callers expect — `sql.Open("godb", path)`, `db.Prepare`, `sql.NullString`. Maintenance burden is bounded: the driver is a small wrapper (~260 lines including tests).

The error story is preserved across the wrapping. `godb.ErrTableNotFound`, `godb.ErrUnsupportedSQL`, and friends all propagate through `*StatementError` (which `Unwrap`s to the sentinel) up through the driver up through `database/sql`. Callers do `errors.Is(err, godb.ErrTableNotFound)` regardless of which path they used.

**Constrains.** Two layers to maintain. Each is small and the responsibilities are clear, but a Value-mapping change touches both packages (the conversion in `pkg/driver` plus the corresponding sentinels in `pkg/godb`).

Connection pooling is a tension we accept rather than solve. `database/sql.DB` is a pool; each pooled `driver.Conn` holds a separate `*godb.DB` (= separate pager). For v0.1's single-writer model this isn't ideal — concurrent writes from multiple conns would race at the pager. The escape hatch is `sql.DB.SetMaxOpenConns(1)`. v0.2's buffer pool can revisit by letting multiple conns share a pager.

**Reversibility.** The two layers are fully decoupled; either can be rewritten without touching the other. The decision itself is easy to back out of (collapse driver into pkg/godb) if we ever decide we want a single layer, but doing so would mean reshaping `pkg/godb`'s ergonomics, which we don't want.

## Alternatives considered

**Path 1 (`pkg/godb` IS the driver).** Single layer, no wrapper. Rejected: `pkg/godb`'s API would inherit `database/sql`'s historical idioms — `NumInput()` returns int, `driver.Value` instead of typed args, the `driver.ErrSkip` protocol for unsupported features — that don't fit a Go-native experience. The native API would lose its current shape (typed sentinels, `errors.Is` dispatch, `Rows.Scan(*int64, *string)` directly) in service of the driver interface's needs.

**Skip `database/sql` entirely.** Some Go database packages do this (e.g., the early Bolt/BoltDB era). Rejected: a meaningful chunk of the Go ecosystem assumes `database/sql`; CLI tools, ORMs, query builders, tutorials, and idiomatic application code patterns all start from `sql.Open`. The wrapper cost is small for the compatibility benefit.

**`pkg/godb` wraps `pkg/driver`.** The opposite layering — implement the driver first, then layer ergonomics on top. Rejected: same idiom-inheritance problem as Path 1, plus an extra translation layer to fight `database/sql`'s shape rather than embrace it. The natural direction is native-first, ecosystem-adapter-second.

## Related

- Code: [`pkg/driver/driver.go`](../../pkg/driver/driver.go) — the wrapper. [`pkg/godb/`](../../pkg/godb/) — the native API the wrapper composes over.
- Book: [Chapter 11 — Polish + database/sql driver (M9)](../book/11-milestone-9-polish-and-driver.md).
- See also: [ADR-0017 (no transactions in v0.1)](0017-no-transactions-in-v0-1.md) — the driver's `Begin` returns the same sentinel; the layering choice means we got the consistency for free.
- See also: [ADR-0003 (`internal/` vs `pkg/` layout)](0003-internal-vs-pkg-layout.md) — established the same pattern at a higher level.
- External: Go [`database/sql`](https://pkg.go.dev/database/sql) and [`database/sql/driver`](https://pkg.go.dev/database/sql/driver) package documentation.

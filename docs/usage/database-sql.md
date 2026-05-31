# Using GoDB via `database/sql`

GoDB ships a `database/sql/driver` wrapper at `pkg/driver` so you can use the Go standard library's database API instead of the native `pkg/godb` one. This page is for users who'd rather route through `database/sql` — likely because their application already does (existing code, ORMs, query builders, migration tooling, the standard connection pool).

If you'd rather use the Go-native API directly, see the [embedded-API tutorial](embedded-api.md). Both paths run the same SQL against the same `.godb` file format; only the call shape differs.

## Quickstart

```go
package main

import (
    "database/sql"
    "fmt"
    "log"

    _ "github.com/felipegalante/godb/pkg/driver"  // registers "godb"
)

func main() {
    db, err := sql.Open("godb", "app.godb")
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    if _, err := db.Exec(`CREATE TABLE users (
        id     INTEGER PRIMARY KEY,
        name   TEXT NOT NULL,
        active BOOLEAN
    )`); err != nil {
        log.Fatal(err)
    }
    db.Exec(`INSERT INTO users VALUES (?, ?, ?)`, 1, "Felipe", true)
    db.Exec(`INSERT INTO users VALUES (?, ?, ?)`, 2, "MG", true)

    rows, err := db.Query(`SELECT * FROM users WHERE id = ?`, 1)
    if err != nil {
        log.Fatal(err)
    }
    defer rows.Close()

    for rows.Next() {
        var id int64
        var name string
        var active bool
        if err := rows.Scan(&id, &name, &active); err != nil {
            log.Fatal(err)
        }
        fmt.Printf("%d %s active=%v\n", id, name, active)
    }
}
```

The blank import (`_ "github.com/felipegalante/godb/pkg/driver"`) registers the driver under the name `"godb"`. After that you use `database/sql` as you would with any other driver.

## What works (same as `pkg/godb`)

The driver wraps `pkg/godb` directly, so everything that works there works here:

- `CREATE TABLE` with `INTEGER` / `TEXT` / `BOOLEAN` column types; `PRIMARY KEY` and `NOT NULL` constraints.
- `INSERT INTO ... VALUES (?, ?, ...)` with positional `?` placeholders.
- `SELECT * FROM table` (full scan).
- `SELECT col1, col2 FROM table` (projection).
- `SELECT ... FROM table WHERE id = ?` (primary-key lookup only in v0.1).
- Unsupported SQL (`JOIN`, `GROUP BY`, `UPDATE`, etc.) returns an error wrapping `godb.ErrUnsupportedSQL` with the source position.

## What `database/sql` adds on top

These are the standard-library features that the native API doesn't provide directly — using `database/sql` gives you them for free:

### Prepared statements

```go
stmt, _ := db.Prepare(`INSERT INTO users VALUES (?, ?, ?)`)
defer stmt.Close()
for i := int64(1); i <= 100; i++ {
    stmt.Exec(i, fmt.Sprintf("user-%d", i), i%2 == 0)
}
```

The driver doesn't actually cache the parsed AST yet (each `Stmt.Exec` re-parses in v0.1), but the API shape is right. If parse caching matters for your workload, the v0.2 `Stmt` will start caching transparently.

### Nullable types via `sql.NullString` / `sql.NullInt64` / `sql.NullBool`

```go
var nickname sql.NullString
rows.Scan(&id, &nickname)
if nickname.Valid {
    fmt.Println("nickname:", nickname.String)
}
```

`database/sql` handles the NULL-vs-not-null distinction via these wrapper types. They work transparently with the driver — pass them to `Scan` and check `.Valid`.

### Context-aware methods

```go
ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
defer cancel()
db.ExecContext(ctx, `INSERT INTO ...`, args...)
db.QueryContext(ctx, `SELECT ...`, args...)
```

The driver implements `driver.StmtExecContext` and `driver.StmtQueryContext`, so `database/sql` routes context-aware calls through them directly. In v0.1, context cancellation is checked at entry (a cancelled `ctx` is rejected before parsing); deeper mid-query cancellation arrives in v0.2.

### Connection pool

`*sql.DB` is a connection pool. By default `database/sql` may open multiple driver connections under the hood; each one is a separate `*godb.DB` (with its own pager) in v0.1. For strict single-writer semantics:

```go
db.SetMaxOpenConns(1)
```

Multi-conn reads against a seeded `.godb` are safe (each conn opens its own pager; reads don't interfere). Multi-conn writes are not safe in v0.1; cap to one conn or serialize at the application layer. v0.2's buffer pool revisits this and may let multiple driver conns share a pager.

### Error dispatch

The driver wraps `pkg/godb`, which wraps internal-layer errors, all preserving `errors.Is` dispatch:

```go
import "github.com/felipegalante/godb/pkg/godb"

_, err := db.Exec(`SELECT * FROM nope`)
if errors.Is(err, godb.ErrTableNotFound) { /* ... */ }
```

For parser errors with source position info:

```go
var sqlErr *godb.SQLError
if errors.As(err, &sqlErr) {
    fmt.Printf("error at line %d, col %d: %s\n",
        sqlErr.Pos.Line, sqlErr.Pos.Column, sqlErr.Message)
}
```

For the source SQL that produced an error (handy for log lines):

```go
var stmtErr *godb.StatementError
if errors.As(err, &stmtErr) {
    fmt.Println("failed SQL:", stmtErr.SQL)
}
```

You import `pkg/godb` for the sentinels even when calling through `database/sql` — they're the canonical error names.

## What doesn't work (v0.1 limitations)

These match `pkg/godb`'s v0.1 limitations, surfaced through the driver:

- **`db.Begin()` returns an error.** Transactions are not supported in v0.1 (see [ADR-0017](../adr/0017-no-transactions-in-v0-1.md)). The error wraps `godb.ErrTransactionsUnsupported`. v0.2 adds real transactions via the rollback journal.
- **`float64`, `[]byte`, and `time.Time` args are rejected** at the driver layer with messages naming the v0.1 limitation. The column types `FLOAT`, `BLOB`, and `TIMESTAMP` arrive in v0.2.
- **Named parameters** (`@name`, `:name`) are not supported. Only positional `?` placeholders.
- **`UPDATE` and `DELETE` are not supported.** The parser rejects them with `ErrUnsupportedSQL`. v0.2.
- **`JOIN`, `GROUP BY`, `ORDER BY`, `LIMIT`, `HAVING`** are not supported. v0.3+.
- **`WHERE` only on the primary key.** Non-PK predicates return `godb.ErrWhereOnlyPrimaryKey`. v0.2 adds TableScan + Filter.
- **No parse caching in `Stmt`** (each `Stmt.Exec` re-parses). Performance, not correctness; v0.2.

## When to use which API

- **Use `pkg/godb` directly** if you're starting fresh and want the Go-native experience: typed errors, `errors.Is` dispatch, `Rows.Scan(*int64, *string)` directly without `sql.NullString` ceremony for non-null columns.
- **Use `pkg/driver` + `database/sql`** if you're integrating into an existing codebase that already uses `database/sql`, if you need `sql.NullString`/`sql.NullInt64` for nullable columns, or if you use tooling that assumes the standard library's API (migration tools, query builders, etc.).

Both paths are stable; both run the same SQL against the same `.godb` file. Pick whichever fits your codebase's existing conventions.

## Further reading

- [Embedded API tutorial](embedded-api.md) — the `pkg/godb` path.
- [Current state](current-state.md) — full snapshot of what works internally as of M9.
- [ADR-0019](../adr/0019-driver-wraps-godb.md) — the layering decision (driver wraps native, not the other way around).
- [Book chapter 11](../book/11-milestone-9-polish-and-driver.md) — the engineering narrative for M9.
- Go [`database/sql`](https://pkg.go.dev/database/sql) package documentation — the canonical reference for the standard-library API the driver exposes.

# Embedded API tutorial

The `pkg/godb` package is GoDB's public Go API. Import it from your application code and you get an embedded database in roughly the shape of `database/sql` — `Open`, `Exec`, `Query`, `Rows.Next`, `Rows.Scan`. No CGo, no separate process, no network. The whole database is one `.godb` file.

This guide walks the full surface as of v0.1.

## Install

```bash
go get github.com/felipegalante/godb/pkg/godb
```

GoDB requires Go 1.22+ and has no third-party runtime dependencies.

## Open and close

```go
import (
    "context"
    "log"

    "github.com/felipegalante/godb/pkg/godb"
)

func main() {
    db, err := godb.Open("app.godb")
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()
    // ... use db ...
}
```

`Open` creates the file if it doesn't exist (default). To require an existing file:

```go
db, err := godb.Open("app.godb", godb.WithCreateIfMissing(false))
if err != nil {
    // err wraps os.ErrNotExist if the file isn't there
}
```

`Close` syncs the catalog and closes the underlying file. It's idempotent — calling Close on an already-closed DB is a no-op. Always `defer db.Close()` after a successful Open.

## Schemas (CREATE TABLE)

```go
ctx := context.Background()

_, err := db.Exec(ctx, `CREATE TABLE users (
    id     INTEGER PRIMARY KEY,
    name   TEXT NOT NULL,
    active BOOLEAN
)`)
```

Supported column types: `INTEGER`, `TEXT`, `BOOLEAN`.

Supported constraints: `NOT NULL`, `PRIMARY KEY`. Constraint order is flexible (`INTEGER PRIMARY KEY NOT NULL` and `INTEGER NOT NULL PRIMARY KEY` are equivalent).

Every table must declare exactly one `INTEGER PRIMARY KEY` column. Other constraint forms (`UNIQUE`, `CHECK`, `DEFAULT`, `REFERENCES`) are rejected with `godb.ErrUnsupportedSQL`.

## Inserts

```go
_, err = db.Exec(ctx, `INSERT INTO users VALUES (?, ?, ?)`, 1, "Felipe", true)

// Or with an explicit column list:
_, err = db.Exec(ctx, `INSERT INTO users (id, name) VALUES (?, ?)`, 2, "MG")
```

`?` placeholders bind to args in occurrence order. Supported Go arg types:

| Go type        | maps to              |
|----------------|----------------------|
| `int`, `int32`, `int64` | `INTEGER`   |
| `string`       | `TEXT`               |
| `bool`         | `BOOLEAN`            |
| `nil`          | `NULL`               |

Anything else (`float64`, `[]byte`, custom types) returns `godb.ErrUnsupportedArgType`. There are no implicit conversions; pass the right type.

`Exec`'s return value is `godb.Result{RowsAffected, LastInsertID}`. `LastInsertID` is the primary key of the just-inserted row.

```go
res, err := db.Exec(ctx, `INSERT INTO users VALUES (?, ?, ?)`, 3, "Jane", false)
log.Printf("inserted row id=%d", res.LastInsertID) // 3
```

## Queries

```go
rows, err := db.Query(ctx, `SELECT * FROM users WHERE id = ?`, 1)
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
    log.Printf("%d %s active=%v", id, name, active)
}
if err := rows.Err(); err != nil {
    log.Fatal(err)
}
```

Two query shapes in v0.1:

- `SELECT * FROM table` — full table scan in primary-key order.
- `SELECT * FROM table WHERE primary_key = ?` — point lookup; returns 0 or 1 row.

Column-list projection is supported: `SELECT name, active FROM users` returns rows with two columns.

WHERE on any column other than the primary key returns `godb.ErrWhereOnlyPrimaryKey`. v0.2 will add `TableScan + Filter` for non-PK predicates.

WHERE compounding with `AND` / `OR` is not supported in v0.1; the parser rejects with `godb.ErrUnsupportedSQL`.

### Scan type rules

`Rows.Scan(dest...)` matches the result's columns to destination pointers, in order. Supported destination types:

| Destination | Accepts | NULL behavior |
|-------------|---------|---------------|
| `*int64`    | `INTEGER` | `ErrScanNullIntoNonNullable` |
| `*string`   | `TEXT`    | `ErrScanNullIntoNonNullable` |
| `*bool`     | `BOOLEAN` | `ErrScanNullIntoNonNullable` |
| `*any` (`*interface{}`) | any kind | becomes `nil` |

Type mismatches (e.g. scanning an `INTEGER` column into `*string`) return `godb.ErrScanTypeMismatch`. The destination count must match the result's column count (`godb.ErrScanWrongCount` otherwise).

v0.1 does **no implicit conversions**. Unlike `database/sql`, scanning an `INTEGER` into `*string` is an error rather than a stringification. Optional conversions are deferred to later releases.

For columns that may be NULL, use `*any`:

```go
var id int64
var name string
var nickname any  // may be nil if the row's nickname is NULL
rows.Scan(&id, &name, &nickname)
```

## Error handling

Every error returned from `pkg/godb` wraps one of the exported sentinels. Dispatch with `errors.Is`:

```go
if errors.Is(err, godb.ErrTableNotFound) { ... }
if errors.Is(err, godb.ErrDuplicatePrimaryKey) { ... }
if errors.Is(err, godb.ErrUnsupportedSQL) { ... }
```

Full sentinel list (in [`pkg/godb/errors.go`](../../pkg/godb/errors.go)):

- `ErrDatabaseClosed` — operation called after `Close`.
- `ErrTransactionsUnsupported` — `db.Begin` returns this in v0.1.
- `ErrTableNotFound`, `ErrTableExists`.
- `ErrColumnNotFound`, `ErrInvalidSchema` (CREATE TABLE shape errors).
- `ErrTypeMismatch`, `ErrNullViolation`, `ErrDuplicatePrimaryKey` (data errors at INSERT).
- `ErrUnsupportedSQL`, `ErrWhereOnlyPrimaryKey` (SQL surface limitations).
- `ErrInsertCountMismatch` (value count vs column count).
- `ErrPlaceholderCountMismatch` (args count vs `?` count).
- `ErrUnsupportedArgType` (arg type not in the table above).
- `ErrScanWrongCount`, `ErrScanTypeMismatch`, `ErrScanNullIntoNonNullable` (Scan errors).

For SQL parser errors specifically, you can dig out the source position by `errors.As`-ing into a `*godb.SQLError`:

```go
var sqlErr *godb.SQLError
if errors.As(err, &sqlErr) {
    log.Printf("SQL error at line %d, column %d", sqlErr.Pos.Line, sqlErr.Pos.Column)
}
```

For the SQL text that produced an error (useful in log lines), unwrap a `*godb.StatementError`:

```go
var stmtErr *godb.StatementError
if errors.As(err, &stmtErr) {
    log.Printf("failed SQL: %s", stmtErr.SQL)
}
```

Sentinel dispatch (`errors.Is(err, godb.ErrXxx)`) traverses both wrappers, so you can combine these freely with the sentinel checks above.

## Mid-life durability checkpoints

If your process is long-running (a daemon, a stress harness) and you want to force a flush without tearing down the DB:

```go
if err := db.Sync(); err != nil {
    log.Print(err)
}
```

`Sync` refreshes the database header's catalog root id (in case a recent catalog operation grew the catalog tree's root) and `fsync`s the file. It's idempotent. `Close` already calls `Sync` internally; this method is for callers who want checkpoints between operations.

## Transactions

Not supported in v0.1:

```go
tx, err := db.Begin(ctx)
// err is godb.ErrTransactionsUnsupported, tx is nil
```

Writes in v0.1 are autocommit with `fsync` on `Close`. v0.2 adds real transactions via a rollback journal. See [ADR-0017](../adr/0017-no-transactions-in-v0-1.md) for the rationale.

## Context

`Exec` and `Query` accept a `context.Context`. In v0.1 we check `ctx.Err()` at entry — passing an already-cancelled context returns `context.Canceled` (or `context.DeadlineExceeded`) before any work happens. Deeper cancellation (mid-query) isn't honored in v0.1; queries are short against single-file storage.

```go
ctx, cancel := context.WithTimeout(context.Background(), time.Second)
defer cancel()
db.Exec(ctx, "INSERT INTO ...", args...)
```

## Concurrency

A `*godb.DB` is safe to call from multiple goroutines — the pager serializes I/O with an internal mutex. **But writes are not isolated** between goroutines: an in-flight INSERT can interleave with a Query's tree walk. The v0.1 contract is single-writer-in-process; concurrent reads are best-effort but not transactional.

v0.2 with the buffer pool + rollback journal makes proper concurrent reads + isolated writes a real promise.

## Putting it together

A complete program that creates a database, inserts a few rows, queries them back, closes, reopens, and re-queries — all in 40 lines:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/felipegalante/godb/pkg/godb"
)

func main() {
    ctx := context.Background()

    db, err := godb.Open("demo.godb")
    if err != nil {
        log.Fatal(err)
    }

    db.Exec(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, active BOOLEAN)`)
    for i, name := range []string{"Felipe", "MG", "Jane"} {
        db.Exec(ctx, `INSERT INTO users VALUES (?, ?, ?)`, int64(i+1), name, i%2 == 0)
    }
    db.Close()

    // Reopen and read back.
    db, err = godb.Open("demo.godb")
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    rows, _ := db.Query(ctx, `SELECT id, name, active FROM users`)
    defer rows.Close()
    for rows.Next() {
        var id int64
        var name string
        var active bool
        rows.Scan(&id, &name, &active)
        fmt.Printf("%d %s active=%v\n", id, name, active)
    }
}
```

Expected output:

```
1 Felipe active=true
2 MG active=false
3 Jane active=true
```

## Current and future surfaces

- **`database/sql`** is available through `pkg/driver`, so you can `sql.Open("godb", "app.godb")` and use the standard `database/sql` package with prepared statements, connection pooling, and nullable scan types.
- **The CLI** ships as the `godb` binary with an interactive shell, `inspect`, `check`, `dump`, `exec`, and `query`.
- **v0.2 planned work** includes transactions, `UPDATE`/`DELETE`, secondary indexes, a buffer pool, and streaming rows.

See the [usage index](README.md) and [current-state guide](current-state.md) for the current release surface and planned work.

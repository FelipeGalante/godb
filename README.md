# GoDB

A SQLite-inspired embedded relational database engine in Go.

GoDB is an educational, portfolio-grade project that builds a real database engine — page-based storage, B+tree indexing, SQL parsing, query execution — from the ground up.

## What GoDB is

- A single-file embedded database (`.godb`).
- Written in Go, with a developer-friendly API.
- SQLite-inspired in spirit and architecture.
- A serious learning vehicle for storage internals.

## What GoDB is not

- Not a drop-in SQLite replacement.
- Not compatible with the SQLite file format.
- Not compatible with the full SQLite SQL dialect.
- Not a network server, not distributed, not OLAP.
- Not a production-grade database.

## Project status

**v0.1.0 — first tagged release.** GoDB has a native Go API (`pkg/godb`: `Open` / `Exec` / `Query` / `Rows.Scan`), a `database/sql/driver` wrapper (`pkg/driver`: `sql.Open("godb", path)`), and a command-line interface (the `godb` binary: shell, `exec`, `query`, `inspect`, `check`, `dump`), with cross-table integration coverage. The supported SQL (`CREATE TABLE`, `INSERT`, `SELECT [WHERE id = ?]`) runs end-to-end against a `.godb` file. Multiple tables of arbitrary size survive close/reopen cycles. It remains a small, educational engine — see the [v0.1 limitations](#v01-limitations) and the [roadmap](#roadmap-abbreviated) below, the [CHANGELOG](CHANGELOG.md) for what shipped, and the [development book](docs/book/) for the engineering narrative.

## Roadmap (abbreviated)

- M0 — project skeleton ✅
- M1 — pager (file format, page read/write/allocate, header validation) ✅
- M2 — record encoding ✅
- M3 — slotted page ✅
- M4 — single-page B+tree ✅
- M5 — multi-page B+tree (splits, descent, root grow) ✅
- M6 — catalog (named tables, persisted metadata) ✅
- M7 — SQL lexer + parser (small grammar, recursive descent) ✅
- M8 — public Go API + planner + executor (the loop closes) ✅
- M9 — polish + `database/sql/driver` + integration tests ✅
- M10 — CLI (shell, exec, query, inspect, check, dump) ✅
- M11 — v0.1 release ✅

## Install

**The CLI** (the `godb` binary):

```bash
go install github.com/felipegalante/godb/cmd/godb@v0.1.0
godb data.godb            # open an interactive shell (see the CLI guide)
```

**The library** — import whichever surface you prefer:

```bash
go get github.com/felipegalante/godb/pkg/godb     # native Go API
go get github.com/felipegalante/godb/pkg/driver   # database/sql driver
```

### From source (development)

```bash
git clone https://github.com/FelipeGalante/godb.git
cd godb
make build       # builds the CLI binary at ./godb
make test        # runs the full test suite
```

## Quickstart

Import `github.com/felipegalante/godb/pkg/godb` and run SQL:

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

    db, err := godb.Open("app.godb")
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    db.Exec(ctx, `CREATE TABLE users (
        id     INTEGER PRIMARY KEY,
        name   TEXT NOT NULL,
        active BOOLEAN
    )`)
    db.Exec(ctx, `INSERT INTO users VALUES (?, ?, ?)`, 1, "Felipe", true)
    db.Exec(ctx, `INSERT INTO users VALUES (?, ?, ?)`, 2, "MG", true)

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
        fmt.Printf("%d %s active=%v\n", id, name, active)
    }
}
```

Supported SQL in v0.1: `CREATE TABLE`, `INSERT`, `SELECT [WHERE primary_key = ?]`. Unsupported constructs (`JOIN`, `GROUP BY`, `UPDATE`, `DELETE`, `AND`/`OR` in WHERE, …) return `godb.ErrUnsupportedSQL` with a position-aware message. Transactions arrive in v0.2 (`db.Begin` returns `godb.ErrTransactionsUnsupported` in v0.1).

### Or via `database/sql`

```go
package main

import (
    "database/sql"
    "fmt"
    "log"

    _ "github.com/felipegalante/godb/pkg/driver"  // registers the "godb" driver
)

func main() {
    db, err := sql.Open("godb", "app.godb")
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`)
    db.Exec(`INSERT INTO users VALUES (?, ?)`, 1, "Felipe")

    rows, err := db.Query(`SELECT * FROM users WHERE id = ?`, 1)
    if err != nil {
        log.Fatal(err)
    }
    defer rows.Close()
    for rows.Next() {
        var id int64
        var name string
        rows.Scan(&id, &name)
        fmt.Println(id, name)
    }
}
```

`sql.NullString` / `sql.NullInt64`, `ExecContext`/`QueryContext`, prepared statements via `db.Prepare`, and the standard connection pool all work as usual. Transactions still return `godb.ErrTransactionsUnsupported` via `db.Begin` — same v0.1 limitation as the native API.

### Or from the command line

No Go required — the `godb` binary drives and inspects a database directly. The db path is the first argument, sqlite-style.

```bash
make build                                       # builds ./godb

godb data.godb exec schema.sql                   # run a SQL script
godb data.godb query "SELECT * FROM users"       # one-shot, rendered as a table
godb -format csv data.godb query "SELECT * FROM users"
godb data.godb                                   # interactive shell (.help for commands)
godb data.godb dump > backup.sql                 # round-trippable SQL
godb data.godb inspect tree                      # walk every table's B+tree
godb data.godb check                             # validate every tree (non-zero exit on corruption)
```

See the [CLI tutorial](docs/usage/cli.md) for every command with worked examples.

See [`docs/usage/`](docs/usage/) for the full guides — [`embedded-api.md`](docs/usage/embedded-api.md) for `pkg/godb`, [`database-sql.md`](docs/usage/database-sql.md) for the `database/sql` path, [`cli.md`](docs/usage/cli.md) for the command-line interface — and the honest "what works right now, what's coming" overview.

## v0.1 limitations

GoDB v0.1 is intentionally small. These are deliberate scope decisions, not bugs — each is addressed in a later version (see the [v0.2 roadmap](docs/usage/current-state.md#whats-next)):

- **SQL:** no `UPDATE` / `DELETE` / `ALTER TABLE` / `DROP TABLE`; no `JOIN` / `GROUP BY` / `ORDER BY` / `LIMIT`; `WHERE` only on the primary key, no compound `AND`/`OR`, no operators other than `=`.
- **No transactions** — `Begin` returns `godb.ErrTransactionsUnsupported`; writes are autocommit-only.
- **No buffer pool** (every read hits disk), **no freelist** (pages are append-only), **no secondary indexes**.
- **No `UNIQUE` / `CHECK` / `DEFAULT` / foreign keys**; rows are fully materialized (no streaming cursor); strict types (no implicit coercions); `float64` / `[]byte` / `time.Time` are not accepted as bind args.
- **CLI:** no `?` placeholders (literal SQL only); single-writer, no concurrent sessions on one file.

The on-disk `.godb` format is stable within a version; the public API surface and the compatibility policy are recorded in [ADR-0021](docs/adr/0021-versioning-and-compatibility.md). The full, always-current list lives in [`docs/usage/current-state.md`](docs/usage/current-state.md).

## Development

```bash
make test    # unit + integration tests
make race    # race detector
make vet     # go vet
make build   # build CLI to ./godb
make fmt     # gofmt + goimports
make clean   # remove binary and *.godb files
```

## Documentation

- [Usage guide](docs/usage/) — how to use godb today (and where on the roadmap each feature lands). Start here if you want to *run* godb rather than read its internals.
- [Product Requirements Document](docs/prd.md) — what GoDB is, who it's for, what v0.1 has to do.
- [Architecture Decision Records](docs/adr/) — the load-bearing engineering decisions and the tradeoffs behind them.
- [The development book](docs/book/) — a chapter-per-milestone narrative covering the database-internals concepts and the code that implements them. Start with the [introduction](docs/book/00-introduction.md).
- [CHANGELOG](CHANGELOG.md) — what shipped in each release.
- [docs/](docs/) — entry point for everything documentation.

## License

MIT

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

**Pre-alpha — the loop is closed.** GoDB has a working public Go API: `godb.Open` + `db.Exec`/`db.Query` run the supported SQL (`CREATE TABLE`, `INSERT`, `SELECT [WHERE id = ?]`) end-to-end against a `.godb` file. The full stack — storage, records, slotted pages, multi-page B+tree, catalog of named tables, SQL lexer + parser, planner, executor — is in place. Multiple tables of arbitrary size survive close/reopen cycles. M9 polishes (better error context, optional `database/sql/driver` wrapper); M10 brings the CLI; M11 tags v0.1. See the [Roadmap](#roadmap-abbreviated) below and the [development book](docs/book/) for the engineering narrative.

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
- M9 — polish, integration, `database/sql/driver` (next)
- M10 — CLI
- M11 — v0.1 release

## Install (dev)

```bash
git clone https://github.com/felipegalante/godb.git
cd godb
make build       # builds the CLI binary at ./godb (banner only until M10)
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

Supported SQL in v0.1: `CREATE TABLE`, `INSERT`, `SELECT [WHERE primary_key = ?]`. Unsupported constructs (`JOIN`, `GROUP BY`, `UPDATE`, `DELETE`, `AND`/`OR` in WHERE, …) return `godb.ErrUnsupportedSQL` with a position-aware message. Transactions arrive in v0.2 (`db.Begin` returns `godb.ErrTransactionsUnsupported` in v0.1). The CLI binary still just prints a banner; full commands land at M10.

See [`docs/usage/`](docs/usage/) for the full embedded-API tutorial and the honest "what works right now, what's coming" guide.

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
- [docs/](docs/) — entry point for everything documentation.

## License

MIT

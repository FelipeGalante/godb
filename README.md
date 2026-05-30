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

**Pre-alpha.** The storage stack, typed records, slotted pages, a multi-page B+tree, a catalog of named tables, and a SQL lexer + parser are landed. The engine can read SQL — `CREATE TABLE`, `INSERT`, `SELECT WHERE id = ?` — into a typed AST. It can hold multiple named tables that survive a close/reopen cycle. What it cannot do yet is *execute* the parsed SQL — that's M9. The public Go API arrives at M8, the CLI commands at M10. See the [Roadmap](#roadmap-abbreviated) below and the [development book](docs/book/) for the engineering narrative.

## Roadmap (abbreviated)

- M0 — project skeleton ✅
- M1 — pager (file format, page read/write/allocate, header validation) ✅
- M2 — record encoding ✅
- M3 — slotted page ✅
- M4 — single-page B+tree ✅
- M5 — multi-page B+tree (splits, descent, root grow) ✅
- M6 — catalog (named tables, persisted metadata) ✅
- M7 — SQL lexer + parser (small grammar, recursive descent) ✅
- M8 — public Go API (next)
- M9 — executor
- M10 — CLI
- M11 — v0.1 release

## Install (dev)

```bash
git clone https://github.com/felipegalante/godb.git
cd godb
make build
./godb
```

## What you can do today

The CLI binary prints a banner and exits; full commands (`exec`, `query`, `inspect`, `check`, an interactive shell) arrive at M10. The public Go API (`godb.Open`, `db.Exec`, `db.Query`, `db.Begin`) arrives at M8. So "use" today means "read the [development book](docs/book/) and study the internal packages."

See [`docs/usage/`](docs/usage/) for the honest "what works right now, what's coming, and where to start reading" guide.

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

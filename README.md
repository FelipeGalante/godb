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

**Pre-alpha.** The storage layer is the only thing landed. There is no SQL yet, no B+tree yet, no CLI commands beyond a banner. See the roadmap below for what's coming.

## Roadmap (abbreviated)

- M0 — project skeleton ✅
- M1 — pager (file format, page read/write/allocate, header validation) ✅
- M2 — record encoding
- M3 — slotted page
- M4 — single-page B+tree
- M5 — multi-page B+tree
- M6 — catalog
- M7 — SQL lexer + parser
- M8 — public Go API
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

## Development

```bash
make test    # unit + integration tests
make race    # race detector
make vet     # go vet
make build   # build CLI to ./godb
make fmt     # gofmt + goimports
make clean   # remove binary and *.godb files
```

## License

MIT

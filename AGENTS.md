# GoDB — Codex Context

## Project summary

GoDB is a SQLite-inspired embedded relational database engine in Go. It is
built bottom-up milestone-by-milestone, with a single-file `.godb` format,
page-based storage, B+tree indexing, SQL parsing, planning, and execution.
It is not SQLite-compatible by design.

- Module: `github.com/felipegalante/godb`
- Go 1.22 (pinned via mise)
- Zero external dependencies — stdlib-only throughout, no exceptions.

**Current status:** M0–M11 all complete and landed on `main`. **v0.1.0 is
tagged and released** (`go install …/cmd/godb@v0.1.0`, `go get …/pkg/godb`).
Next work is v0.2 (buffer pool, transactions, UPDATE/DELETE, range scans,
secondary indexes).

---

## Milestone map

| # | Name | Status |
|---|------|--------|
| M0 | Project skeleton | ✅ |
| M1 | Pager (file I/O, 4 KB pages) | ✅ |
| M2 | Record encoding (typed values, LEB128) | ✅ |
| M3 | Slotted pages | ✅ |
| M4 | Single-page B+tree | ✅ |
| M5 | Multi-page B+tree (splits, root growth) | ✅ |
| M6 | Catalog (named tables, persisted metadata) | ✅ |
| M7 | SQL lexer + recursive-descent parser | ✅ |
| M8 | Public Go API + planner + executor | ✅ |
| M9 | Polish + `database/sql` driver + integration tests | ✅ |
| M10 | CLI (shell, exec, query, inspect, check, dump) | ✅ |
| M11 | Tag v0.1 release | ✅ |

---

## Architecture

Three-layer stack. Each layer is internal-only; only `pkg/` is importable
from outside the module.

```
cmd/godb/            CLI entry point (thin wrapper → internal/cli.Run)
pkg/godb/            Native public API   (Open, Exec, Query, Rows, Tables)
pkg/driver/          database/sql/driver wrapper (registered as "godb")
internal/cli/        CLI commands + REPL + statement splitter
internal/exec/       Executor (logical plan → materialized results)
internal/planner/    Planner (AST → logical plan + schema validation)
internal/sql/        Lexer, AST, recursive-descent parser
internal/catalog/    Table metadata, persisted via its own B+tree
internal/btree/      Multi-page B+tree (leaf/internal nodes, splits)
internal/record/     Row encoding/decoding (binary codec, Kind types)
internal/storage/    Pager (fixed 4 KB pages, file I/O, header)
internal/buffer/     Placeholder — buffer pool, v0.2
internal/tx/         Placeholder — transactions, v0.2
```

Key design choices (each has an ADR in `docs/adr/`):
- Fixed 4 KB pages, big-endian multibyte ints (ADR-0001, ADR-0002)
- No SQLite compatibility (ADR-0004)
- Bottom-up build order: storage before SQL (ADR-0005)
- No buffer pool in v0.1 — direct pager I/O (ADR-0006)
- LEB128 uvarint, not SQLite varint (ADR-0009)
- Rows fully materialized before Query returns (ADR-0016)
- No transactions in v0.1 — Begin returns ErrTransactionsUnsupported (ADR-0017)
- `btree.UpdateCellSameSize` for in-place catalog root updates (ADR-0018)
- `pkg/driver` wraps `pkg/godb`, not vice versa (ADR-0019)
- CLI is stdlib-only, db-first invocation, `internal/cli` (ADR-0020)

---

## Public API surface

**pkg/godb** (native):
```go
db, err := godb.Open(path, godb.WithCreateIfMissing(true))
res, err := db.Exec(ctx, "INSERT INTO ...", args...)
rows, err := db.Query(ctx, "SELECT ...", args...)
rows.Next() / rows.Scan(&dest...) / rows.Err() / rows.Close()
tables := db.Tables()   // []godb.TableInfo, read-only introspection
db.Sync() / db.Close()
```

**pkg/driver** (database/sql):
```go
sql.Open("godb", path)   // same behaviour via Prepare/Exec/Query
```

**CLI** (`godb <db> [command]`):
- `godb data.godb` — interactive shell
- `godb data.godb exec file.sql`
- `godb data.godb query "SELECT ..."`
- `godb data.godb inspect {header|page N|tree}`
- `godb data.godb check`
- `godb data.godb dump`
- Flags: `-format {table|csv}`, `-version`, `-help`

**Supported SQL (v0.1):**
- `CREATE TABLE t (col TYPE [NOT NULL] [PRIMARY KEY], ...)`
- `INSERT INTO t [(cols)] VALUES (?, ...)`
- `SELECT [*|cols] FROM t [WHERE pk = ?]`
- Types: `INTEGER`, `TEXT`, `BOOLEAN`; NULL is a first-class value (ADR-0008)

---

## v0.1 limitations (intentional, not bugs)

- No `UPDATE` / `DELETE` / `ALTER TABLE` / `DROP TABLE`
- No `JOIN`, `GROUP BY`, `ORDER BY`, `LIMIT`
- `WHERE` only on the primary key column (`ErrWhereOnlyPrimaryKey`)
- No compound `WHERE` (AND/OR), no range operators
- No transactions (`Begin` returns `ErrTransactionsUnsupported`)
- No buffer pool — every read hits disk
- No freelist — pages are append-only
- No secondary indexes
- No foreign keys, UNIQUE, CHECK, DEFAULT
- Rows are fully materialized — no streaming cursor
- No implicit type coercions (`ErrTypeMismatch` on mismatch)
- `float64`, `[]byte`, `time.Time` not accepted as bind args
- CLI does not support `?` placeholders — literal SQL only

v0.2 planned work: buffer pool, transactions + rollback journal, UPDATE/DELETE,
range scans, secondary indexes, freelist reuse, page checksums.

---

## Error sentinels (pkg/godb)

`ErrDatabaseClosed`, `ErrTransactionsUnsupported`, `ErrTableNotFound`,
`ErrTableExists`, `ErrColumnNotFound`, `ErrTypeMismatch`,
`ErrDuplicatePrimaryKey`, `ErrNullViolation`, `ErrUnsupportedSQL`,
`ErrWhereOnlyPrimaryKey`, `ErrInvalidSchema`, `ErrInsertCountMismatch`,
`ErrPlaceholderCountMismatch`, `ErrUnsupportedArgType`,
`ErrScanWrongCount`, `ErrScanTypeMismatch`, `ErrScanNullIntoNonNullable`

All matchable with `errors.Is`. Struct errors: `godb.SQLError` (carries
`Pos.Line / Pos.Column`), `godb.StatementError` (wraps with source SQL).

---

## Code style & naming conventions

**Errors:** sentinel `Err*` vars for dispatch; `fmt.Errorf("pkg.Func: %w", err)`
for wrapping; `guardOpen()` pattern for lifecycle checks; internal sentinels
mapped to public ones via `mapInternalErr()`.

**Tests:**
- Flat — no `t.Run` subtests
- `tempDB(t, opts...)` helper with `t.Cleanup` close
- `ctx()` shorthand returns `context.Background()`
- Error variants checked with `errors.Is(err, godb.ErrXxx)`
- Integration tests in `*_integration_test.go`

**Naming:**
- Receivers: single- or two-letter abbreviations (`db`, `p`, `tr`)
- Constructors: `Create` / `Open` for on-disk types; `New` only for
  pure in-memory types
- Unexported helpers: camelCase, `guard`/`map` prefix where applicable

**Concurrency:** single-writer mutex at `DB` level (`db.mu`); all public
methods lock for their duration; no internal caching in v0.1.

**Options:** functional-options pattern (`type Option func(*options)`).

---

## Docs cadence (every milestone)

Every milestone commit cycle must include:
1. A new chapter in `docs/book/` (update `docs/book/README.md` index and
   `docs/book/00-introduction.md` milestone table).
2. A `README.md` refresh (project status and user-visible capability notes if
   features landed).
3. A `docs/usage/` update or new page for user-visible API/CLI changes;
   refresh `docs/usage/current-state.md` each milestone.
4. An ADR under `docs/adr/` only when a load-bearing design decision was made.

PRD (`docs/prd.md`) changes only when product direction changes.

---

## Build & test

```bash
make build    # → ./godb binary
make test     # go test ./...
make race     # tests with -race
make vet      # go vet
make fmt      # gofmt / goimports
make clean    # remove binary + *.godb files
```

---

## Next: v0.2

v0.1.0 is shipped. The first v0.2 minor bump is where the stable surface is
allowed to change (see [ADR-0021](docs/adr/0021-versioning-and-compatibility.md)).
Planned: buffer pool, transactions + rollback journal (real `*Tx`),
`UPDATE`/`DELETE`, range scans beyond PK equality, secondary indexes, freelist
reuse, page checksums. Several touch both the API and the on-disk format — hence
a minor bump, not a `v0.1.x` patch.

---

## Key file paths

| Path | Purpose |
|------|---------|
| `pkg/godb/godb.go` | DB type, Open/Close/lifecycle |
| `pkg/godb/errors.go` | Public error sentinels & `mapInternalErr` |
| `pkg/godb/exec.go`, `query.go` | Exec / Query implementation |
| `pkg/driver/driver.go` | `database/sql/driver` adapter |
| `internal/cli/` | All CLI logic (injected writers, unit-testable) |
| `internal/exec/executor.go` | Plan execution, DML/DDL |
| `internal/planner/planner.go` | AST → logical plan |
| `internal/btree/tree.go` | B+tree operations (splits, descent) |
| `internal/catalog/catalog.go` | Table metadata, bootstrap |
| `internal/storage/pager.go` | Page-level file I/O |
| `docs/adr/` | 21 ADRs (ADR-0001 – ADR-0021) |
| `docs/book/` | 14 chapters (M0–M11 narrative) |
| `docs/usage/` | User-facing guides (CLI, embedded API, driver) |
| `docs/prd.md` | Product Requirements Document |

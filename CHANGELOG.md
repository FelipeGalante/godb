# Changelog

All notable changes to GoDB are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and GoDB adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
The versioning and compatibility policy — what is stable within a version and
what is not — is recorded in [ADR-0021](docs/adr/0021-versioning-and-compatibility.md).

## [Unreleased]

_Work toward v0.2: buffer pool, transactions + rollback journal, `UPDATE`/`DELETE`,
range scans, secondary indexes, freelist reuse, page checksums._

## [0.1.0] - 2026-05-31

First tagged release. A small, single-file, SQLite-inspired embedded relational
database engine built bottom-up in pure Go (standard library only, zero external
dependencies). Educational and portfolio-grade — not SQLite-compatible by design.

### Added

**Storage (`internal/storage`)**
- Single-file `.godb` format with fixed 4 KB pages and a validated file header
  ([ADR-0001](docs/adr/0001-single-file-fixed-pages.md)).
- Pager: open/create, page read/write/allocate, `Sync` (fsync), and a persisted
  catalog-root slot in the header. Big-endian on-disk integers
  ([ADR-0002](docs/adr/0002-big-endian-on-disk.md)).

**Records (`internal/record`)**
- Typed scalar values `NULL`, `INTEGER`, `TEXT`, `BOOLEAN`; binary row encoding
  with a row-version byte and LEB128 uvarints
  ([ADR-0009](docs/adr/0009-leb128-uvarint.md)); schema validation of column
  count, nullability, and type. `NULL` and empty `TEXT` are distinct on disk
  ([ADR-0008](docs/adr/0008-null-and-empty-text-distinct.md)).

**B+tree (`internal/btree`)**
- Slotted-page layout with a sorted cell directory
  ([ADR-0010](docs/adr/0010-slotted-page-layout.md)); a multi-page B+tree with
  leaf/internal splits, root growth, descent, the leaf chain, and a full
  `Validate()` invariant check. Survives thousands of rows and close/reopen.

**Catalog (`internal/catalog`)**
- Multiple named tables in one database, each with its own B+tree; table
  metadata persisted via a B+tree of its own with a custom row encoding
  ([ADR-0014](docs/adr/0014-catalog-row-encoding.md)).

**SQL frontend (`internal/sql`)**
- Hand-written lexer + recursive-descent parser for a deliberately small grammar
  ([ADR-0015](docs/adr/0015-sql-grammar-scope.md)): `CREATE TABLE`, `INSERT`,
  `SELECT [WHERE primary_key = expr]`. Types `INTEGER`, `TEXT`, `BOOLEAN`;
  constraints `NOT NULL`, `PRIMARY KEY`. Unsupported constructs are recognized
  and rejected with a position-aware error.

**Public Go API (`pkg/godb`)**
- `Open`, `Exec`, `Query`, `Rows.Next`/`Scan`/`Columns`/`Err`/`Close`, `Sync`,
  `Close`; functional options (`WithCreateIfMissing`). Strict bind/scan types,
  no implicit conversions. Matchable error sentinels (`errors.Is`), `SQLError`
  (source position) and `StatementError` (carries source SQL). Rows are fully
  materialized in v0.1 ([ADR-0016](docs/adr/0016-rows-materialization.md)).
- `DB.Tables()` read-only introspection accessor (table names + `CREATE` text).

**`database/sql` driver (`pkg/driver`)**
- Registers as `"godb"`; `sql.Open("godb", path)` with the standard
  `database/sql` surface (Prepare, ExecContext/QueryContext, `sql.NullString`,
  the connection pool). Wraps `pkg/godb` rather than reimplementing it
  ([ADR-0019](docs/adr/0019-driver-wraps-godb.md)).

**CLI (`godb` binary)**
- db-first, sqlite-style invocation
  ([ADR-0020](docs/adr/0020-cli-architecture.md)): interactive shell (REPL with
  `.help`/`.tables`/`.schema`/`.mode`/`.dump`/`.exit`), `exec <file.sql>`,
  `query "<sql>"`, `dump`, `inspect header|page <n>|tree`, `check`. Flags
  `-format table|csv`, `-version`, `-help`. Stdlib-only.

### Known limitations

v0.1 is intentionally small. No `UPDATE`/`DELETE`/`ALTER`/`DROP`, no
`JOIN`/`GROUP BY`/`ORDER BY`/`LIMIT`, `WHERE` only on the primary key, no
compound `WHERE`, no transactions (`Begin` returns `ErrTransactionsUnsupported`,
[ADR-0017](docs/adr/0017-no-transactions-in-v0-1.md)), no buffer pool
([ADR-0006](docs/adr/0006-no-buffer-pool-in-v0-1.md)), no freelist, no secondary
indexes, no streaming rows, no `?` binding from the CLI. See
[`docs/usage/current-state.md`](docs/usage/current-state.md) for the full list;
each item names the version that addresses it.

[Unreleased]: https://github.com/FelipeGalante/godb/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/FelipeGalante/godb/releases/tag/v0.1.0

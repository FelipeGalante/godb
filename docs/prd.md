# GoDB — Product Requirements Document

- Status: Living document (revised when product direction changes)
- Last revised: 2026-05-30
- Owner: Felipe Galante
- Source of record for detailed design: the original project spec (in your private notes); this PRD distills it into a product-shaped contract.

## 1. Summary

GoDB is a SQLite-inspired embedded relational database engine written in Go. It targets a single Go developer who wants a small, durable, file-backed database they can `import` and use from their application without CGo, without running a separate server, and without committing to SQLite or another existing engine.

The v0.1 target is a real (though limited) database engine — fixed-size pages, a B+tree, a tiny SQL subset, a clean public API, and a CLI for inspection — that another developer can install, use, and reason about.

## 2. Vision and non-vision

### Vision

A focused embedded database engine that:

- Ships a usable embedded database for small local-first applications.
- Exposes the engine through a native Go API, a `database/sql` driver, and a CLI.
- Keeps the implementation inspectable: the [book](book/) walks through the internals as the engine grows.
- Stays small. The bar for adding a feature is "does it pass the milestone discipline" — not "is it possible."

### Non-vision

GoDB is **not** aiming to be any of the following, and never will:

- A drop-in SQLite replacement.
- Compatible with the SQLite file format.
- Compatible with the full SQLite SQL dialect.
- A network database server.
- A distributed database.
- An OLAP / analytical engine.
- A high-concurrency database.
- A replacement for PostgreSQL, SQLite, DuckDB, or Badger.

If a requested change would push the project toward any of the above, the answer is no. See [ADR-0004: No SQLite compatibility](adr/0004-no-sqlite-compatibility.md).

## 3. Target users and use cases

### Primary user

A Go developer building a single-process, single-user application — a CLI tool, a desktop app, a local-first web app, a self-hosted backend — who wants relational, persistent storage without operational overhead.

### Concrete use cases

- **Embedded application database.** A Go binary uses GoDB the way a CLI tool might use BoltDB or modernc.org/sqlite: open a `.godb` file, run a few `CREATE TABLE` and `INSERT` statements, then `SELECT` from the application code.
- **Implementation reference.** A developer reading the [book](book/) and source can understand how GoDB's storage, catalog, SQL, planner, and executor layers fit together.
- **Internals playground.** A user wants to experiment with page sizes, B+tree fanout, buffer-pool eviction policies, or transaction modes without forking an established engine to do it.

### Out of scope as users

- Multi-process or multi-machine workloads.
- Applications that need MVCC, advanced concurrency, or hot backup.
- Applications that need full SQLite SQL.

## 4. Functional requirements

The functional surface area is sized by version. Each version is a usable engine; later versions extend it without breaking the file format established in v0.1.

### v0.1 (current target)

- **Storage**: single `.godb` file; fixed 4 KB pages (see [ADR-0001](adr/0001-single-file-fixed-pages.md)); a validated file header; create-or-open; durable writes after explicit `Sync()`.
- **Records**: scalar values `NULL`, `INTEGER`, `TEXT`, `BOOLEAN`; binary row encoding with a row-version byte; schema validation of column count, nullability, and type.
- **Tables**: multiple tables; one B+tree per table; an `INTEGER PRIMARY KEY` rowid; insert, point lookup by primary key, full table scan.
- **SQL**: `CREATE TABLE`, `INSERT INTO ... VALUES`, `SELECT * FROM table`, `SELECT cols FROM table WHERE primary_key = ?`.
- **API**: `godb.Open(path)`, `db.Exec(ctx, sql, args...)`, `db.Query(ctx, sql, args...)`, basic row scanning, `db.Begin(ctx)`.
- **CLI**: interactive shell, execute SQL from stdin or a file, inspect metadata, inspect pages, dump table rows.
- **Testing**: unit tests per low-level package; integration tests against real temporary database files; crash/reopen tests; golden tests for SQL parsing.

Source spec section: §3.1 of the project spec.

### v0.2 (planned)

- Storage: page checksums, freelist page reuse, dirty page tracking, a real buffer pool with eviction.
- B+tree: deletion, range scans, better validation tools.
- Transactions: explicit `BEGIN`/`COMMIT`/`ROLLBACK`, rollback journal, atomic single-process commit, recovery on open.
- SQL: `UPDATE`, `DELETE`, full `WHERE` comparison operators, `ORDER BY primary_key`, `LIMIT`.
- Indexes: `CREATE INDEX`, secondary B+tree indexes, index lookup for equality predicates.

Source spec section: §3.2.

### v0.3 (planned)

- Query engine: projection pruning, simple planner, index selection, basic nested-loop joins.
- SQL: inner joins; aggregates (`COUNT(*)`, `MIN`, `MAX`); optional `GROUP BY`; limited `ALTER TABLE`.
- Developer support: `database/sql/driver` compatibility, a migration package, better error types, a documentation site.

Source spec section: §3.3.

### Beyond v0.3

Interesting and explicitly long-term: WAL mode, MVCC, concurrent readers and writers, prepared statement caching, query optimizer, foreign keys, additional constraints, triggers, views, subqueries, full-text search, compression, encryption, replication, network mode. These are listed so they don't get pulled forward into the current scope.

## 5. Non-functional requirements

Listed in priority order. When two conflict, earlier wins.

1. **Correctness.** Every layer has tests. Storage and B+tree validation are run frequently in tests; race tests pass for the public API. The on-disk format is invariant within a version.
2. **Test discipline.** Unit tests in every internal package. Integration tests using real temporary files (`t.TempDir`). Race tests pass. Property-style tests for the B+tree (insert + validate after each).
3. **Observability of internals.** The CLI ships `inspect header`, `inspect page`, `inspect tree`, `check` so a developer can look inside a database without external tools.
4. **Ergonomic API.** The public Go API is what a developer would write themselves on a day they were paying attention. Errors are typed; opening, querying, and scanning rows feels familiar.
5. **Performance.** A non-priority compared to the above. v0.1 reads/writes pages directly with no buffer pool ([ADR-0006](adr/0006-no-buffer-pool-in-v0-1.md)). The buffer pool, eviction, and indexed lookup arrive in v0.2/v0.3.
6. **Portability.** Pure Go, no CGo. Builds and tests on linux/amd64, linux/arm64, darwin/amd64, darwin/arm64.

## 6. Success criteria for v0.1

GoDB v0.1 is "done" when:

- A developer can install the CLI.
- A developer can import the Go package.
- A database file can be created and reopened.
- Tables can be created.
- Rows can be inserted.
- Rows persist after process restart.
- Rows can be selected by primary key.
- Rows can be scanned.
- The B+tree supports thousands of rows.
- The catalog persists table metadata.
- The CLI can inspect the header and tree.
- The database checker validates B+tree invariants.
- All tests pass; race tests pass.
- README explains limitations clearly.

Source spec section: §32.

## 7. Top risks

The full risk register lives in §30 of the project spec. The three risks most likely to derail v0.1:

1. **Scope risk.** Trying to look like full SQLite too early. Mitigation: a strict milestone sequence (see [ADR-0005](adr/0005-bottom-up-build-order.md)) and a public SQL support matrix that documents what is and isn't supported.
2. **B+tree complexity.** Splits, internal nodes, and cursor traversal are easy to get subtly wrong. Mitigation: a `Validate()` function called after every insert in tests; an inspector CLI command; explicit invariants written into both code comments and the book chapter on B+trees.
3. **SQL parser scope.** The parser is easy to over-engineer. Mitigation: support a small grammar in v0.1, reject everything else with a clear error message naming the unsupported feature.

## 8. Out of scope (permanent)

These are not delayed — they are deliberately never going to be part of GoDB:

- SQLite file format compatibility.
- SQLite SQL dialect compatibility.
- A network protocol or server mode.
- Distributed operation (replication, sharding).
- MVCC.
- Cross-process concurrency.

If any of these become interesting, they belong in a different project, not GoDB.

## 9. Future work reference

See:

- [docs/book/](book/) — the chapter-per-milestone narrative.
- [docs/usage/current-state.md](usage/current-state.md) — current release capabilities and next planned work.
- Spec §21 (recommended implementation order).

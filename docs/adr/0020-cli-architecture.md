# ADR-0020: The CLI is stdlib-only, lives in `internal/cli`, and is db-first

- Status: Accepted
- Date: 2026-05-31
- Tags: cli, layout, process, api

## Context

M10 ships the `godb` binary. Until M10, `cmd/godb/main.go` was a one-line stub that printed a banner. The engine already had everything the CLI needs: SQL runs through the stable `pkg/godb` API (`Exec`/`Query`/`Rows`), and the introspection commands (`inspect`, `check`) can read the internal `storage`/`btree`/`catalog` packages directly because the CLI lives in the same module. So the CLI is mostly UI — but several structural choices had to be made and pinned, because changing them later would mean reshaping the package layout, the dependency story, or the public API.

Four questions had to be answered:

1. **Dependencies.** Reach for a CLI framework (cobra/urfave-cli) or stay stdlib-only? The engine has *zero* third-party dependencies — `go.mod` is `module` + `go 1.22` — and that's a deliberate, advertised property.
2. **Where the code lives.** Put logic in `cmd/godb/main.go`, or a separate package? `main` packages are notoriously hard to unit-test.
3. **Invocation shape.** How does a user name the database and pick a command?
4. **How shell meta-commands read the catalog.** `.tables`/`.schema`/`dump` need the table list. The pager has only an in-process mutex and no cross-process lock, so a second handle on the same file would be an uncoordinated, unsafe view.

A fifth, smaller question — how to split a multi-statement script into individual statements — is decided here too because the obvious answer (re-use the SQL lexer) is wrong.

## Decision

**The CLI is stdlib-only, all logic lives in `internal/cli` with a thin `main`, invocation is db-first, statement-splitting is purpose-built, and shell meta-commands read through the open `*godb.DB` via a new `db.Tables()` accessor.**

Concretely:

- **Stdlib only.** `flag`, `bufio`, `os`, `encoding/csv`, `strings`, `strconv`. No cobra, no urfave-cli. Preserves the zero-dependency stance ([ADR-0003](0003-internal-vs-pkg-layout.md) context).
- **`internal/cli` + thin `main`.** `cmd/godb/main.go` is `os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))`. `cli.Run(args, stdin, stdout, stderr) int` parses flags, dispatches, returns an exit code. Every handler takes injected `io.Reader`/`io.Writer`, so the whole CLI is unit-testable in-process with `bytes.Buffer`s — no subprocess.
- **db-first, sqlite-style invocation.** `godb [-format ...] <db> [command] [args]`. The database path is the first positional; a bare `godb <db>` opens the interactive shell. Matches `sqlite3 file.db` muscle memory.
- **Three exit codes.** `0` success, `1` runtime error, `2` usage error. A `usageError` type (built by `usagef`) plus `errors.As` in `Run` map misuse to `2`.
- **Purpose-built statement splitter.** `splitStatements` is a byte scanner that mirrors only the two lexer rules for constructs that can contain a `;` — single-quoted strings (with the `''` escape) and `--` line comments — and splits on top-level `;`. It does *not* re-use `internal/sql`'s lexer.
- **`db.Tables()` read accessor, not a second pager handle.** A new `pkg/godb` method returns `[]TableInfo{Name, SQL}` sorted by name, locking the same mutex as every other `DB` method. Shell meta-commands and `dump`/`.dump` read through the already-open handle. `inspect`/`check` run in their own process where no `*godb.DB` is held, so they open the pager read-only directly.

## Consequences

**Enables.** A usable binary with zero new dependencies — `go.mod` stays a two-line file, and the "no third-party deps" property the project advertises survives the CLI milestone. Because handlers take injected writers, the end-to-end tests build a temp `.godb`, run real commands, and assert on captured stdout/stderr and exit codes without spawning a process — fast and deterministic. The db-first shape is familiar to anyone who has used `sqlite3`. The exit-code contract makes the CLI scriptable (`godb db check && deploy`).

The `db.Tables()` accessor is the safe path for shell introspection: one handle, one mutex, a consistent read against live (possibly still-buffered) state. It's a tiny, read-only addition to the public surface — `TableInfo` exposes only `Name` and `SQL`, nothing that ties callers to internal types.

**Constrains.** Stdlib-only means no free flag niceties (subcommand grouping, auto-generated completion, rich help) — `usageText` is a hand-maintained string, and nested subcommands are dispatched by a hand-written `switch`. That's acceptable at this command count (six commands + a shell) and re-evaluable if the surface grows.

The purpose-built splitter is a second place that encodes the string-literal and comment rules (the lexer is the first). If a future grammar change adds, say, block comments or a second quote style, both the lexer and the splitter must learn it. The duplication is small and called out in `split.go`'s doc comment.

`db.Tables()` widens the public API by one method and one type — a forward commitment. It's deliberately minimal so the commitment is cheap.

**Reversibility.** All CLI code is behind `internal/cli`, so it can be rewritten freely. Adopting a CLI framework later is a contained change (rewrite `Run`/`dispatch`); it would only become load-bearing if it pulled a dependency into `go.mod`. The db-first invocation, once documented and in users' fingers, is the stickiest part — changing argument order would break every script. `db.Tables()` is public and therefore subject to the usual compatibility expectations, but it's read-only and small.

## Alternatives considered

**Use cobra (or urfave/cli).** The ergonomic default for Go CLIs — subcommands, flag groups, generated help and completion for free. Rejected: it would be the project's *first* third-party dependency, breaking a property that's both advertised and pedagogically useful (the whole engine is stdlib-only, so a reader can audit every line). The CLI is small enough that `flag` + a `switch` is no real burden, and the saved boilerplate isn't worth the dependency.

**Put the logic in `cmd/godb/main.go`.** Simpler file layout, no extra package. Rejected: `main` packages are awkward to unit-test (you end up shelling out to the built binary, which is slow and flaky), and the CLI has real logic worth testing directly — statement splitting, rendering, the SELECT/Exec router. A separate `internal/cli` with injected writers makes all of it testable in-process.

**Command-first invocation (`godb <command> <db> ...`).** The other common shape (closer to `git <command>`). Rejected in favor of db-first to match `sqlite3`'s `sqlite3 file.db` convention, which is the closest analog and the one users of an embedded SQL database are most likely to have in their fingers. Decided with the project owner up front.

**Re-use the SQL lexer to split statements.** `internal/sql` already tokenizes; in principle it knows where statements end. Rejected: the lexer's job is to *validate* and *reject* unsupported SQL with `ErrUnsupportedSQL`. The splitter must stay dumb — it has to hand *every* chunk, valid or not, to `Exec`/`Query` so the public API produces the real, position-aware error. Splitting must succeed even on SQL the lexer would refuse. A 40-line byte scanner that knows only the string/comment rules is the right tool.

**Open a second pager/catalog handle for shell meta-commands.** The naive way to answer `.tables`. Rejected: the pager has only an in-process mutex and no cross-process file lock (v0.1 single-writer). A second handle on the open file is an uncoordinated view — it can't see writes still buffered in the first handle, and two writing handles corrupt each other. The shell already holds a `*godb.DB`; meta-commands must read through it. Hence `db.Tables()`.

## Related

- Code: [`internal/cli/`](../../internal/cli/) — the whole CLI. [`cmd/godb/main.go`](../../cmd/godb/main.go) — the thin wrapper. [`pkg/godb/introspect.go`](../../pkg/godb/introspect.go) — the `Tables()` accessor.
- Book: [Chapter 12 — The Command-Line Interface (M10)](../book/12-milestone-10-cli.md).
- Usage: [`docs/usage/cli.md`](../../docs/usage/cli.md) — the user-facing tutorial.
- See also: [ADR-0003 (`internal/` vs `pkg/` layout)](0003-internal-vs-pkg-layout.md) — the same internal-implementation / stable-public-surface split, applied to the CLI.
- See also: [ADR-0015 (SQL grammar scope)](0015-sql-grammar-scope.md) — why the splitter only has to know about string literals and `--` comments.
- See also: [ADR-0001 (single file, fixed pages)](0001-single-file-fixed-pages.md) — the single-file, single-writer model that makes a second handle unsafe and forces the `db.Tables()` route.

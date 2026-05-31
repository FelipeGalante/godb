# Chapter 12 — The Command-Line Interface (M10)

## Where we are

By the end of [Chapter 11](11-milestone-9-polish-and-driver.md) the engine had two stable front doors: the native Go API (`pkg/godb`) and the `database/sql/driver` wrapper (`pkg/driver`). Both run the full SQL→storage loop against a real `.godb` file. Both assume you're writing Go.

What you couldn't do: drive a database without writing a program. Create a table, run a query, dump the contents, peek at a page — all of it required compiling a binary first. The `cmd/godb/main.go` stub printed a banner and exited. For an engine whose whole point is to be inspectable and learnable, that's a gap.

M10 closes it. It ships a real `godb` binary with:

1. **An interactive shell** (REPL) — type SQL, see results, with `.`-prefixed meta-commands.
2. **`exec <file.sql>`** — run a multi-statement SQL script.
3. **`query "<sql>"`** — run one statement and render the result.
4. **`inspect header | page <n> | tree`** — read the on-disk structures directly.
5. **`check`** — validate the catalog tree and every table tree.
6. **`dump`** — emit a SQL script that recreates the database.

This is a UI milestone. Almost nothing new happens in the engine; the CLI is a thin layer over `pkg/godb` for SQL and over `internal/{storage,btree,catalog}` for introspection. The interesting work is in the *shape* of that layer: how to invoke it, how to split a script into statements, how to keep stdout clean, and how to make `inspect` useful without being noisy.

## Foundation

### What a database CLI is actually for

Three different jobs hide under "the CLI":

1. **Running SQL.** The obvious one — `exec` a script, `query` a one-shot, or type interactively. This is pure UI over the public API; the engine already does all the work.
2. **Inspecting internals.** `inspect` and `check` don't go through SQL at all. They open the pager and catalog directly and read the bytes — the file header, a page's slotted-page header, the shape of a B+tree, the validity of every tree. This is the part a *learner* reaches for: "show me what's actually on disk."
3. **Round-tripping.** `dump` emits SQL that, fed back through `exec`, recreates the database. It's a backup format, a migration tool, and a test that the engine's own output reloads cleanly — all at once.

A general-purpose database CLI (think `sqlite3` or `psql`) does far more — dot-commands for everything, output modes, `.import`, readline history, tab completion. GoDB's CLI is deliberately the minimal useful set plus a `-format` flag. The [ADR](../adr/0020-cli-architecture.md) records why.

### The statement-splitting problem

A SQL script is a single string with multiple statements separated by `;`. To run them one at a time, you have to split on `;` — but not *every* `;`. A semicolon inside a string literal (`INSERT ... VALUES ('a;b')`) or inside a `--` line comment is not a statement terminator. Split naively and you'll cut `'a;b'` in half.

So the splitter has to know just enough lexical structure to recognize the two constructs that can legitimately contain a `;`:

- **Single-quoted string literals**, with the `''` escape (two quotes inside a string mean one literal quote, *not* the end of the string).
- **`--` line comments**, which run to the end of the line.

Everything else is a byte to scan past. This is a stripped-down version of what the real lexer ([`internal/sql/lexer.go`](../../internal/sql/lexer.go)) does — we don't need tokens, just the rule for "is this `;` at the top level." Re-using the full lexer was tempting but wrong: the lexer rejects unsupported SQL with `ErrUnsupportedSQL`, and the splitter must hand *every* chunk to the API (so the API can produce the real error with a source position). The splitter stays dumb on purpose.

### Why the shell can't open a second handle

The interactive shell needs to answer `.tables` and `.schema` — list the tables, show their `CREATE` statements. The data for that lives in the catalog. The naive implementation: open a second pager/catalog handle and read it.

That's a bug waiting to happen. GoDB's pager has only an **in-process mutex** — there's no cross-process file lock (v0.1 is single-writer; see the gaps section of [Chapter 11](11-milestone-9-polish-and-driver.md)). A second handle on the same file would be an *uncoordinated* view: it wouldn't see writes still buffered in the first handle, and two handles writing would corrupt each other. The shell already holds an open `*godb.DB`. Meta-commands must read through *that* handle, not a new one.

So M10 adds one small accessor to the public API:

```go
// pkg/godb — read-only introspection for tooling/CLI.
type TableInfo struct {
    Name string
    SQL  string // original CREATE TABLE text
}
func (db *DB) Tables() ([]TableInfo, error) // sorted by name
```

It locks the same mutex every other `DB` method takes, so it's a consistent read against the live handle. `.tables`, `.schema`, and `dump`/`.dump` all go through it. SQL execution still flows through `Exec`/`Query`. By contrast, `inspect` and `check` run in their own process where *no* `*godb.DB` is held — they open the pager read-only directly, which is safe precisely because nothing else has the file open.

### Keeping stdout pipe-clean

A CLI that's meant to be scripted has to be disciplined about streams. The rule GoDB follows: **data on stdout, everything else on stderr.** Query rows, dump SQL, `inspect` output — stdout. Prompts, status lines (`(3 rows)`, `ok (1 row affected, ...)`), error messages, the connect banner — stderr.

This is what makes `godb data.godb dump > backup.sql` produce a clean file, and `godb data.godb query "SELECT ..." | wc -l` count only rows. If the status line `(42 rows)` leaked onto stdout, every pipe would be off by one. The handlers take *separate* `stdout` and `info`/`stderr` writers so the split is structural, not a matter of remembering to use the right `Fprintln`.

## Decisions

- **sqlite-style, db-first invocation.** The database path is the first positional argument; a bare `godb <db>` opens the interactive shell. `godb <db> <command> [args]` runs a subcommand. This matches `sqlite3 file.db` muscle memory. [ADR-0020](../adr/0020-cli-architecture.md).
- **Stdlib only — no cobra.** The whole engine has zero third-party dependencies (`go.mod` is just `module` + `go 1.22`). The CLI uses `flag`, `bufio`, `os`, `encoding/csv` and nothing else. ADR-0020.
- **All logic in `internal/cli`; `main` is a one-liner.** Handlers take injected `io.Reader`/`io.Writer` so the whole CLI is unit-testable without spawning a process. `cmd/godb/main.go` just wires `os.Stdin/Stdout/Stderr` and `os.Args`.
- **Three exit codes.** `0` success, `1` runtime error, `2` usage error (bad/missing args, unknown command). A `usageError` type plus `errors.As` in `Run` map misuse to `2`; everything else is `1`.
- **A purpose-built statement splitter**, not the real lexer. Mirrors only the string-literal and `--`-comment rules. ADR-0020.
- **Open modes differ by command.** `shell` and `exec` open create-if-missing **true** (a script can bootstrap a fresh file). `query`, `dump`, `inspect`, `check` open **false** so a missing file gives a clear error instead of silently creating an empty database.
- **`db.Tables()` accessor, not a second handle.** Shell meta-commands read through the open `*godb.DB`. ADR-0020.
- **No `?` parameter binding from the CLI.** SQL typed at the CLI is literal. Binding is a programmatic concern; the CLI doesn't expose a way to supply args, so `?` placeholders simply have nothing to bind to. Deliberate omission for v0.1.
- **SELECT vs everything-else dispatch by first token.** `runStatement` lexes just the first token: `SELECT` → `Query` + render rows; EOF (comment-only chunk) → skip; anything else → `Exec` + status line. One lex, no full parse.

## The code

All of it lives in [`internal/cli/`](../../internal/cli/). `cmd/godb/main.go` is the thin wrapper:

```go
func main() {
    os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
```

### `cli.go` — entry point and dispatch

[`Run`](../../internal/cli/cli.go) parses the global flags (`-format`, `-version`) with a `flag.FlagSet`, pulls the db path off the front of the positionals, and dispatches on the (optional) subcommand. The exit-code contract lives here: a handler that returns a `*usageError` (built by the `usagef` helper) maps to exit `2` via `errors.As`; any other error is `1`; nil is `0`. The `usageText` constant is the `-help` output.

### `split.go` — the statement splitter

[`splitStatements`](../../internal/cli/split.go) is the byte scanner from the Foundation section. A small state machine: a boolean `inString`, special handling for `''` (peek the next byte; if it's also a quote, skip both and stay in-string), and a fast-forward past `--` comments to the next newline. Top-level `;` cuts a statement; the trailing `;` is dropped and each chunk is `TrimSpace`d. Empty chunks (a stray `;`, a comment-only run) are discarded. It's used by both `exec` and the shell.

### `run.go` — the SELECT/Exec router

[`classify`](../../internal/cli/run.go) does the single-token lex; [`runStatement`](../../internal/cli/run.go) is the dispatcher. SELECT routes to `db.Query` → `renderRows` → a `(N rows)` status line on the info writer. Everything else routes to `db.Exec` → an `ok (N rows affected, last insert id M)` line. Comment-only chunks (`stmtSkip`) are no-ops. The `plural` helper is a tiny generic over `~int | ~int64` so the status lines read naturally.

### `render.go` — table and CSV output

[`renderRows`](../../internal/cli/render.go) drains a `*godb.Rows` into a `[][]any` (scanning each column into an `*any` holder), then formats it. `renderTable` computes column widths in a first pass, then left-aligns with `%-*s` and joins columns with ` | ` / the separator with `-+-`, trimming trailing spaces so lines don't carry padding. `renderCSV` uses `encoding/csv` (so quoting and embedded commas are handled by the stdlib). Three value formatters: `displayCell` (for the table — `NULL`, decimal int, `true`/`false`, raw string), `csvCell` (NULL → empty field, since CSV has no null), and `sqlLiteral` (for `dump` — `NULL`, int, `TRUE`/`FALSE`, and `'...'` with the `''` escape).

### `shell.go` — the REPL

[`runShell`](../../internal/cli/shell.go) opens the db (create-if-missing true) and loops over `bufio.Scanner` lines. The buffer-and-accumulate logic is the heart of it: a line that starts with `.` (when no statement is mid-entry) is a meta-command; otherwise the line is appended to a buffer, and [`lastTopLevelSemicolon`](../../internal/cli/shell.go) checks whether the buffer now contains at least one complete statement. If it does, everything up to the last top-level `;` is split and executed, and the remainder stays in the buffer for the next line. This is what lets a single statement span many lines, and several statements share one line. Prompts (`godb> ` when the buffer is empty, `  ...> ` mid-statement) go to stderr. At EOF, any unterminated trailing statement is run. `runMeta` handles `.help`, `.tables`, `.schema [name]`, `.mode`, `.dump`, `.exit`/`.quit`.

### `inspect.go` — reading the bytes

[`runInspect`](../../internal/cli/inspect.go) opens the pager read-only (create-if-missing false). `header` prints every field of `pager.Header()` plus the `Magic`. `page <n>` decodes the type byte from `Data[0]`, then for btree pages reads the slotted-page header (`btree.ReadHeader`) and prints cell count, free bytes, free-space offset, cell-dir end, and either the right sibling (leaf) or rightmost child (internal). Page 0 is special — it's the file header, so it prints that. `tree` opens the catalog and walks every table's B+tree with `walkPage`, descending via `IterateInternalCells` + `RightmostChild` and printing an indented per-page summary (`leaf page 7: 12 cells`).

### `check.go` — validation

[`runCheck`](../../internal/cli/check.go) opens the pager + catalog, then calls `btree.Open(pager, root).Validate()` on the catalog tree (root from `Header.CatalogRootPageID`) and on every table tree. Each is reported `OK` or `CORRUPT: <reason>`. If any tree is corrupt, the command returns an error — so the process exits non-zero, which is what a CI pipeline or a `&&` chain wants.

### `dump.go` — round-trippable SQL

[`dumpAll`](../../internal/cli/dump.go) walks `db.Tables()` and, for each, prints the stored `CREATE` statement (trailing `;` normalized) followed by one `INSERT INTO <name> (cols...) VALUES (...)` per row. The column list comes from `rows.Columns()` and the values from `sqlLiteral`, which guarantees the output reloads cleanly. It backs both the `dump` subcommand and the shell's `.dump`.

## Tests as proof

The tests live in three files and lean on a `run(stdin, args...)` helper that captures stdout/stderr into `bytes.Buffer`s and returns the exit code — the whole CLI is exercised in-process, no subprocess needed.

- **[`split_test.go`](../../internal/cli/split_test.go)** pins the splitter's tricky cases: a `;` inside `'a;b'` is not a terminator, the `''` escape doesn't end the string early, a `;` inside a `--` comment is ignored, comment-only and empty chunks are dropped, and a trailing unterminated statement survives.
- **[`render_test.go`](../../internal/cli/render_test.go)** pins the formatters: `parseFormat` accepts table/csv (case-insensitive) and rejects the rest; `displayCell`/`csvCell` differ on NULL; `sqlLiteral` produces `TRUE`/`FALSE` and escapes `O'Brien` → `'O''Brien'`; `renderTable` and `renderCSV` produce exact expected bytes.
- **[`cli_test.go`](../../internal/cli/cli_test.go)** is the end-to-end suite: build a temp `.godb`, `exec` a schema+inserts script, `query` it (table and CSV), `dump` and reload into a fresh db (the round-trip test), `check` (exit 0), `inspect header/page/tree`, and the error cases (missing db → exit 1, unknown command → exit 2, `-version` → exit 0). It also drives the shell over a piped stdin to prove multi-line statement accumulation and embedded-semicolon handling.

What the tests *don't* try to prove: terminal behavior (line editing, history, colors) — there isn't any, by design. The CLI reads lines and writes bytes; that's all that's tested.

## What this layer cannot do yet

- **No `?` parameter binding.** SQL at the CLI is literal; there's no way to supply bind args. Deliberate for v0.1.
- **No concurrent CLI sessions on one file.** Single-writer, no cross-process lock (the same v0.1 limitation the driver chapter notes). Two `godb` processes writing the same file is unsafe.
- **No readline niceties.** No history, no tab completion, no line editing, no syntax highlighting. `bufio.Scanner` reads lines; that's it. A reader who wants these can pipe through `rlwrap`.
- **No output modes beyond table/csv.** No JSON, no `.import`, no column/line modes. `-format` and `.mode` cover table and CSV.
- **No streaming for large results.** `renderTable` buffers all rows to compute column widths (and the engine materializes `Rows` anyway — see [ADR-0016](../adr/0016-rows-materialization.md)). Fine for the scale v0.1 targets; revisited when the buffer pool and a streaming cursor land in v0.2.
- **`inspect` is read-only and shallow.** It shows headers and tree shape, not raw cell payloads or a hex dump. Enough to learn the structure; not a forensic tool.

Each has a milestone home or is a deliberate v0.1 omission.

## Further reading

- The [`flag`](https://pkg.go.dev/flag), [`bufio`](https://pkg.go.dev/bufio), and [`encoding/csv`](https://pkg.go.dev/encoding/csv) package docs — the entire stdlib surface the CLI is built on.
- The [SQLite CLI documentation](https://sqlite.org/cli.html) — the reference for the dot-command and db-first conventions GoDB borrows a minimal subset of.
- The CLI usage tutorial, [`docs/usage/cli.md`](../usage/cli.md) — every command with worked examples, from the *user's* side rather than the implementer's.

## Where the next chapter picks up

M11 — the v0.1 release. With the engine, both APIs, and the CLI all in place, M11 is about making GoDB *installable and usable from another project*: tagging v0.1, verifying a clean `go get` of `pkg/godb` and `pkg/driver` from a fresh module, and the release hygiene (version string, changelog, the README's install story) that turns a repository into a release. No new engine capability — the loop is closed — just the packaging that lets someone else pick it up.

That's where the next chapter picks up.

# Using the GoDB CLI

GoDB ships a single binary, `godb`, that lets you drive and inspect a database without writing any Go. It's a thin layer over the same `pkg/godb` API the [embedded-API tutorial](embedded-api.md) covers, plus a few introspection commands that read the on-disk structures directly.

If you want to *embed* GoDB in a Go program, read [`embedded-api.md`](embedded-api.md) (native) or [`database-sql.md`](database-sql.md) (`database/sql`). This page is for using the engine from a shell.

## Build it

```bash
make build      # builds ./godb
./godb -help
```

There's no `go install` target yet (that lands with the v0.1 tag at M11). For now the binary is built into the repo root.

## Invocation shape

The database path is always the **first** argument, sqlite-style. A bare `godb <db>` opens the interactive shell; add a command to do one thing and exit.

```
godb [-format table|csv] <db> [command] [args]
```

| Invocation | What it does |
|------------|--------------|
| `godb data.godb` | open the interactive shell |
| `godb data.godb shell` | same, explicit |
| `godb data.godb exec schema.sql` | run a SQL script |
| `godb data.godb query "SELECT * FROM users"` | run one statement, print the result |
| `godb data.godb inspect header` | dump the file header |
| `godb data.godb inspect page <n>` | dump one page's header |
| `godb data.godb inspect tree` | walk every table's B+tree |
| `godb data.godb check` | validate the catalog + every table tree |
| `godb data.godb dump` | print SQL to recreate the database |

Global flags (before or interleaved with the db path, per Go's `flag` parsing):

- `-format table|csv` — output format for query/dump rows (default `table`).
- `-version` — print the version and exit.
- `-help` — print usage and exit.

**Exit codes:** `0` success, `1` runtime error (e.g. SQL error, missing file on a read command), `2` usage error (bad arguments, unknown command). Useful in scripts: `godb data.godb check && deploy`.

**Streams:** result rows, dump SQL, and `inspect` output go to **stdout**; prompts, status lines, and errors go to **stderr**. So `godb data.godb dump > backup.sql` produces a clean file and `godb data.godb query "..." | wc -l` counts only rows.

**A note on file creation:** `shell` and `exec` create the database if it doesn't exist (so a script can bootstrap a fresh file). `query`, `dump`, `inspect`, and `check` do **not** — a missing file gives a clear error instead of silently creating an empty database.

## `exec` — run a SQL script

Put your schema and seed data in a file:

```sql
-- schema.sql
CREATE TABLE users (
    id     INTEGER PRIMARY KEY,
    name   TEXT NOT NULL,
    active BOOLEAN
);
INSERT INTO users VALUES (1, 'Felipe', TRUE);
INSERT INTO users VALUES (2, 'MG', TRUE);
INSERT INTO users VALUES (3, 'Jane', FALSE);
```

```bash
godb data.godb exec schema.sql
```

Statements are separated by `;`. Semicolons inside string literals (`'a;b'`) and `--` comments are handled correctly. Each statement runs in order; execution stops at the first failure and reports the failing statement's 1-based index:

```
godb: statement 3: ...error message...
```

Status lines (`ok (1 row affected, ...)`, `(N rows)`) go to stderr.

## `query` — one-shot statement

```bash
godb data.godb query "SELECT * FROM users"
```

```
id | name   | active
---+--------+-------
1  | Felipe | true
2  | MG     | true
3  | Jane   | false
```

The row count (`(3 rows)`) is printed to stderr. Switch to CSV with `-format`:

```bash
godb -format csv data.godb query "SELECT id, name FROM users"
```

```
id,name
1,Felipe
2,MG
3,Jane
```

CSV uses the standard quoting rules (`encoding/csv`): a value containing a comma is quoted, and `NULL` becomes an **empty field** (CSV has no null). A non-SELECT statement (`INSERT`, `CREATE TABLE`) also works through `query` and prints an `ok (...)` status line instead of a table.

> **No `?` binding from the CLI.** SQL typed at the CLI is literal — there's no way to supply bind arguments, so don't use `?` placeholders here. Parameter binding is a programmatic feature; use the [Go API](embedded-api.md) for that.

## The interactive shell

```bash
godb data.godb
```

```
godb 0.1.0-dev (M10)
Connected to data.godb — .help for commands, .exit to quit.
godb>
```

Type SQL terminated by `;`. Statements can span multiple lines — the prompt changes to `  ...>` while a statement is mid-entry:

```
godb> SELECT *
  ...> FROM users
  ...> WHERE id = 1;
id | name   | active
---+--------+-------
1  | Felipe | true
(1 row)
godb>
```

You can also put several statements on one line; they run left to right.

### Meta-commands

Lines beginning with `.` (when no statement is pending) are shell commands, not SQL:

| Command | Effect |
|---------|--------|
| `.help` | show the command list |
| `.tables` | list table names |
| `.schema [name]` | show `CREATE` statements — all tables, or just one |
| `.mode table\|csv` | set the result output format for the session |
| `.dump` | print SQL to recreate the database (same as the `dump` command) |
| `.exit` / `.quit` | leave the shell |

```
godb> .tables
users
godb> .schema users
CREATE TABLE users (
    id     INTEGER PRIMARY KEY,
    name   TEXT NOT NULL,
    active BOOLEAN
);
godb> .mode csv
godb> SELECT id, name FROM users;
id,name
1,Felipe
2,MG
3,Jane
(3 rows)
godb> .exit
```

The shell is line-based — no history, tab completion, or line editing in v0.1. Pipe through `rlwrap godb data.godb` if you want those.

## `dump` — round-trippable SQL

```bash
godb data.godb dump
```

```sql
CREATE TABLE users (
    id     INTEGER PRIMARY KEY,
    name   TEXT NOT NULL,
    active BOOLEAN
);
INSERT INTO users (id, name, active) VALUES (1, 'Felipe', TRUE);
INSERT INTO users (id, name, active) VALUES (2, 'MG', TRUE);
INSERT INTO users (id, name, active) VALUES (3, 'Jane', FALSE);
```

The output is valid GoDB SQL: feed it back through `exec` to reload into a fresh database.

```bash
godb data.godb dump > backup.sql
godb fresh.godb exec backup.sql      # round-trips cleanly
```

Text values are single-quoted with `''` escaping (`O'Brien` → `'O''Brien'`), booleans are `TRUE`/`FALSE`, and `NULL` is emitted literally — so a dump and reload reproduces every value exactly. Each `INSERT` names its columns explicitly, so the reload doesn't depend on column order.

## `inspect` — read the on-disk structures

`inspect` opens the file read-only and shows you what's actually on disk. It pairs well with [chapter 03 (pager)](../book/03-milestone-1-pager.md), [chapter 05 (slotted pages)](../book/05-milestone-3-slotted-pages.md), and [chapter 07 (B+tree)](../book/07-milestone-5-multi-page-btree.md) when you want to follow the file format from bytes to tables.

### `inspect header`

```bash
godb data.godb inspect header
```

```
magic:              GODB
format version:     0.1
page size:          4096
page count:         3
catalog root page:  1
freelist head page: 0
change counter:     0
last txn id:        0
checksum algo:      0
flags:              0
```

Every field of the [database file header](../book/03-milestone-1-pager.md). Page 0 *is* the header, so `inspect page 0` prints the same thing. (Page 1 here is the catalog's own B+tree — the catalog is itself just a table-typed tree, so it reports as `table-leaf` too.)

### `inspect page <n>`

```bash
godb data.godb inspect page 2
```

```
page 2
  type:             table-leaf (0x04)
  cells:            3
  free bytes:       3999
  free space off:   4033
  cell dir end:     34
  right sibling:    0
```

The type byte plus the slotted-page header — cell count (here 3, one per row), free space, and the directory/sibling pointers. Internal pages show `separators` and `rightmost child` instead. This is the view for "is this page laid out the way the [slotted-page chapter](../book/05-milestone-3-slotted-pages.md) says it should be?"

### `inspect tree`

```bash
godb data.godb inspect tree
```

```
table "users" (root page 2):
  leaf page 2: 3 cells
```

Walks every table's B+tree from its root, printing an indented per-page summary. A larger table that has split shows internal pages and their children:

```
table "events" (root page 4):
  internal page 4: 2 separators
    leaf page 2: 41 cells
    leaf page 5: 38 cells
    leaf page 6: 40 cells
```

This is the structural view: how deep is the tree, how many leaves, where did the splits land.

## `check` — validate every tree

```bash
godb data.godb check
```

```
catalog tree: OK
table "users": OK
table "events": OK
```

`check` runs the full B+tree validator (`Tree.Validate` — slotted-page invariants, key ordering, separator/range consistency, equal leaf depth) on the catalog tree and every table tree. A corrupt tree is reported as `CORRUPT: <reason>` and the command **exits non-zero**, so you can gate on it:

```bash
godb data.godb check && echo "all trees valid"
```

## A complete session

Build a database, query it, inspect it, validate it, back it up:

```bash
# 1. Build a database from a script.
cat > schema.sql <<'SQL'
CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, active BOOLEAN);
INSERT INTO users VALUES (1, 'Felipe', TRUE);
INSERT INTO users VALUES (2, 'MG', TRUE);
SQL
godb data.godb exec schema.sql

# 2. Query it (table, then CSV).
godb data.godb query "SELECT * FROM users"
godb -format csv data.godb query "SELECT * FROM users"

# 3. Inspect the internals.
godb data.godb inspect header
godb data.godb inspect tree

# 4. Validate.
godb data.godb check

# 5. Back up and round-trip into a fresh file.
godb data.godb dump > backup.sql
godb fresh.godb exec backup.sql
godb fresh.godb query "SELECT * FROM users"     # identical to step 2
```

## Limitations (v0.1)

The CLI is intentionally minimal. It does **not** have:

- **`?` parameter binding** — SQL at the CLI is literal. Use the [Go API](embedded-api.md) for bind args.
- **Concurrent sessions on one file** — single-writer, no cross-process lock. Don't run two writing `godb` processes against the same file.
- **Readline niceties** — no history, tab completion, or line editing. `rlwrap` if you want them.
- **Output modes beyond table/CSV** — no JSON, no `.import`.
- **`UPDATE` / `DELETE` / `JOIN` / non-PK `WHERE` / transactions** — these are engine limitations, not CLI ones. See [`current-state.md`](current-state.md) for the full "what's not yet usable" list.

The supported SQL surface is exactly what the engine supports: `CREATE TABLE`, `INSERT`, `SELECT [WHERE primary_key = literal]`. Anything else returns a clear `ErrUnsupportedSQL` message naming the feature.

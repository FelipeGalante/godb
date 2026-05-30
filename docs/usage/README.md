# Using GoDB

This is the entry point for *using* GoDB. The [book](../book/) tells you how GoDB is built; the [PRD](../prd.md) explains what GoDB is meant to be; the [ADRs](../adr/) record why specific design choices were made. None of those answer the practical question: *how do I run GoDB, and what can it do for me right now?* That's what this directory is for.

It's also honest. GoDB is pre-alpha. As of this writing the public Go API doesn't exist yet (it lands at M8) and the CLI prints a banner and exits (full commands land at M10). The pages here describe what's usable today, what's coming, and where to read next.

## Where we are

| Milestone | Status | What it gives the user |
|-----------|--------|------------------------|
| M0 — Skeleton | ✅ | A Go module that builds; a CLI binary that prints a banner; `make test`, `make race`, `make vet`. |
| M1 — Pager | ✅ | A durable `.godb` file format with 4 KB pages and a validated header. Internal. |
| M2 — Records | ✅ | Typed values (`NULL`/`INTEGER`/`TEXT`/`BOOLEAN`) and row encoding. Internal. |
| M3 — Slotted page | ✅ | Many cells in one page, sorted by key, with O(log n) lookup. Internal. |
| M4 — Single-page B+tree | ✅ | A `Tree` type that wraps a single leaf and persists across reopens. Internal. |
| M5 — Multi-page B+tree | ✅ | Splits, descent, root grow. ~10,000-row trees survive close/reopen. Internal. |
| M6 — Catalog | next | Named tables (multiple `Tree`s in one database). Still internal. |
| M7 — SQL lexer + parser | | `CREATE TABLE`, `INSERT`, `SELECT` parsed into an AST. Still internal. |
| **M8 — Public Go API** | | **`godb.Open`, `db.Exec`, `db.Query`, `db.Begin`.** This is the milestone that makes "use godb" a real sentence. |
| M9 — Executor | | End-to-end SQL execution. |
| M10 — CLI | | Interactive shell, `exec`, `query`, `inspect`, `check`. |
| M11 — v0.1 release | | Tagged release; install + use from another Go project. |
| v0.2 | | Transactions, deletion, buffer pool, secondary indexes. |

Everything before M8 is "internal layers landing one at a time." M8 is where this guide gets real content (an embedded-API tutorial). M10 adds a CLI guide. Until then, this directory tells you what's possible *if you read or extend the engine* — see [`current-state.md`](current-state.md) for the honest snapshot.

## What you can do today

Three things, in order from least to most involved:

### 1. Read the [development book](../book/)

The book walks the engine from the first commit forward, one chapter per milestone. It's written for someone who knows Go and wants to learn how a database engine is put together. Start at the [introduction](../book/00-introduction.md) and follow chapters in order; by the end of [chapter 07](../book/07-milestone-5-multi-page-btree.md) you've read everything the engine knows how to do today.

### 2. Build and run the CLI

```bash
make build
./godb
```

Prints `godb: SQLite-inspired database engine in Go` and exits. The CLI subcommands (`exec`, `query`, `inspect`, `check`, the interactive shell) all land at M10.

### 3. Use the internal packages as a learning sandbox

Today the only usable Go API is the internal one. Importing it requires either forking the repo or pointing your module's replace directive at a local clone, because `internal/` blocks external imports by design (see [ADR-0003](../adr/0003-internal-vs-pkg-layout.md)).

```go
import (
    "github.com/felipegalante/godb/internal/btree"
    "github.com/felipegalante/godb/internal/record"
    "github.com/felipegalante/godb/internal/storage"
)

pager, _ := storage.OpenPager("app.godb", storage.PagerOptions{CreateIfMissing: true})
tree, _ := btree.Create(pager)
pager.SetCatalogRoot(tree.RootPageID())

values := []record.Value{record.Int(1), record.Text("Felipe"), record.Bool(true)}
payload, _ := record.EncodeRow(values)
tree.Insert(1, payload)

pager.Sync()
pager.Close()
```

Caveats, in clear-eyed order:

- **Internal APIs can change without warning.** They will change. We rename things across milestones (M5 renamed several M4 tests because their preconditions changed).
- **No transactions, no atomic splits.** A crash mid-Insert can leave the on-disk tree inconsistent. v0.2's rollback journal fixes this.
- **No buffer pool.** Every read/write hits disk. Fine for learning, slow for benchmarks.
- **No SQL, no schema enforcement at the storage layer.** Validation is your code's job until M9.
- **One tree per database** until M6 brings the catalog.

If those caveats are fine, [`current-state.md`](current-state.md) shows the same shape end-to-end with the full read-back loop.

## When to read what

- **Just want to use it?** Wait for M8. In the meantime, this directory.
- **Want to learn how databases work?** Start with the [book introduction](../book/00-introduction.md). It assumes Go knowledge and zero database-internals knowledge.
- **Want to understand a specific decision?** Browse the [ADR index](../adr/).
- **Want to know what's deliberately *not* being built?** Read the [PRD](../prd.md), specifically the Vision/Non-vision and Out-of-scope sections.

## What lands here next

As features land, new pages join this directory:

- M6: a short section in [`current-state.md`](current-state.md) showing the catalog's `Create` / `Lookup` shape and how it composes with `btree.Tree`.
- M8: `embedded-api.md` — the proper "import godb, open a database, run SQL from Go" tutorial.
- M10: `cli.md` — the interactive shell, all the `inspect` subcommands, the `check` validator.
- v0.2: `transactions.md`, `migrations.md`.

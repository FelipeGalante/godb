# ADR-0016: `Rows` is materialized in v0.1; streaming arrives in v0.2

- Status: Accepted
- Date: 2026-05-30
- Tags: api, executor, performance

## Context

The B+tree's iteration API is callback-based: `Tree.Scan(fn)` visits every cell in key order, calling `fn(key, payload)` for each. The public `Rows` interface is pull-based, in the shape of `database/sql.Rows`: `Next() bool` advances; `Scan(...)` decodes the current row.

The cleanest pull-based bridge would be a `btree.Cursor` — a stateful walker that wraps the leaf-chain traversal and yields cells one at a time on demand. v0.1 doesn't have one. Building one is non-trivial: it has to track the current page, the current cell index within that page, and handle leaf transitions; it has to be reentrant against pager writes that could happen between calls (though v0.1 is single-writer, so this is bounded).

## Decision

For v0.1, `internal/exec.RunQuery` accumulates every result row into a slice inside the returned `*Rows` before returning. The public `godb.Rows.Next()` is a slice walk; `Scan` reads the current row's `[]record.Value` and copies into the user's destinations.

Memory cost is bounded by the query's result-set size. For the v0.1 supported SQL (SELECT * with no LIMIT, SELECT cols, SELECT WHERE id = ? — which is 0 or 1 rows), this is acceptable.

## Consequences

**Enables.** Simpler code: no cursor state, no leaf-transition bookkeeping in the executor. No concurrency between iteration and the pager. `Rows.Scan` and `Rows.Next` are trivial.

**Constrains.** A `SELECT *` on a multi-million-row table allocates a multi-million-`record.Value` slice. v0.1's target workloads (small embedded apps, learning, demos) don't hit this. Production-scale workloads would.

**Reversibility.** Easy. When v0.2 lands a buffer pool + `btree.Cursor`, `RunQuery` switches to returning a streaming iterator and `Rows.Next`/`Scan` walk it lazily. The **public API doesn't change** — only the `Rows` implementation does. Code written against v0.1 continues to work unchanged.

## Alternatives considered

**Add `btree.Cursor` now.** Scope expansion. Cursors are subtle (the leaf-chain walker has to deal with the page boundary in the middle of an iteration), and v0.1 has no need for them outside this one use case. Better to land them when the buffer pool is also there (v0.2) so the cursor uses pinned pages instead of unmediated `Pager.ReadPage` calls.

**Goroutine + channel.** Kick off `Tree.Scan` in a goroutine and send rows over a channel; `Rows.Next` receives. Rejected: introduces concurrency where the pager doesn't yet support it, opens cleanup questions (what happens if the caller forgets to `Close`?), and adds an asynchronous failure mode (errors arrive on the channel after Next has returned).

**Refactor `Tree.Scan` to be re-entrant.** Change the interface from "callback per cell" to "advance + read current" so the executor can drive it. Rejected: invasive change to a well-tested layer, with no clear interface that doesn't end up looking like a cursor.

## Related

- Code: [`internal/exec/executor.go`](../../internal/exec/executor.go) — `runTableScan` and `runPKLookup` produce the materialized rows; `pkg/godb/query.go` — `Rows.Next`/`Scan` walk the slice.
- Book: [Chapter 10 — Public Go API + Planner + Executor (M8)](../book/10-milestone-8-public-api.md).
- See also: [ADR-0010 (slotted page layout)](0010-slotted-page-layout.md) — the data structure a future cursor would walk.

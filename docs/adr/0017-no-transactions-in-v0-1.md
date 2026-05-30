# ADR-0017: Transactions are not supported in GoDB v0.1

- Status: Accepted
- Date: 2026-05-30
- Tags: api, transactions, scope

## Context

The `database/sql`-shaped public API has `db.Begin()` returning a `*Tx`, on which `Tx.Exec`/`Tx.Query`/`Tx.Commit`/`Tx.Rollback` are the transactional surface. Users who'd never used GoDB before would reasonably expect this shape to work.

But v0.1 has no rollback journal, no WAL, no buffer pool. Real transactions — atomic commit, rollback on failure, isolation across concurrent readers/writers — depend on all three of those layers existing. Writes in v0.1 are autocommit + `fsync` per spec §16.1. Pretending to expose transactions when none of the underlying machinery is there would mislead callers.

There are three options for the API surface:

1. Don't expose `Begin` / `Tx` at all in v0.1; add them in v0.2. Adding the methods later is a surface expansion that breaks neither callers nor compatibility (it's purely additive).
2. Expose `Begin` and a `*Tx`, but have `Tx.Exec` and `Tx.Query` silently delegate to the DB in autocommit mode, with `Commit`/`Rollback` as no-ops. Forward-compatible at the call site, but the semantics are wrong — `Rollback` doesn't actually roll anything back.
3. Expose `Begin` and `*Tx`, but `Begin` always returns `(nil, ErrTransactionsUnsupported)`. The Tx type is declared (its methods are stubs returning the same sentinel) so v0.2 can implement them without changing the surface.

## Decision

GoDB v0.1 picks option 3: `Tx` exists in `pkg/godb` as a type with all the expected methods, but `DB.Begin(ctx)` always returns `(nil, ErrTransactionsUnsupported)` in v0.1. The Tx methods are also stubs returning the same sentinel — they're declared so v0.2 can land transactions without expanding `pkg/godb`'s exported API.

Callers who try `Begin` get a clear error explaining that v0.1 doesn't support transactions. Callers who use `db.Exec` / `db.Query` directly get full autocommit behavior, which works.

## Consequences

**Enables.** Code written against v0.1 that *needs* transactions fails loudly at `Begin` with a clear message, not silently with wrong semantics. Code that uses `db.Exec` directly works correctly and stays forward-compatible when v0.2 lands real transactions. The `Tx` type's surface is stable from v0.1 onward (no API expansion when v0.2 ships).

**Constrains.** Some application patterns that would group multiple operations into a transaction (e.g. "insert these three rows atomically") can't be expressed safely in v0.1. The honest workaround is to design around it for now and revisit in v0.2.

**Reversibility.** Trivial. v0.2's transaction implementation lives behind the same Tx interface; flipping `Begin` from "return error" to "return real Tx" is a single change.

## Alternatives considered

**Option 1 (don't expose `Begin` at all).** Cleaner in v0.1; but adding `Begin` in v0.2 means callers using interface assertions or generic database wrappers may notice the API expansion. Rejected for forward-compat reasons.

**Option 2 (silent autocommit Tx).** Maximally forward-compatible at the call site. Rejected because the semantics are wrong: `tx.Exec(...); tx.Rollback()` would silently keep the writes, which is the worst possible footgun. Better to error than to lie.

**Real transactions via `pager.Sync` at Commit.** Tempting because it's superficially similar. Rejected: rolling back would require a journal, which is v0.2's job. And "transactions that commit but can't roll back" is again worse than no transactions.

## Related

- Code: [`pkg/godb/tx.go`](../../pkg/godb/tx.go), [`pkg/godb/godb.go`](../../pkg/godb/godb.go) (`DB.Begin`).
- Book: [Chapter 10 — Public Go API + Planner + Executor (M8)](../book/10-milestone-8-public-api.md).
- PRD §4 (v0.2 functional requirements includes the rollback journal that closes this gap).

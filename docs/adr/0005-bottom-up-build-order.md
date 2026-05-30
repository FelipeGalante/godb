# ADR-0005: Bottom-up build order — storage before SQL

- Status: Accepted
- Date: 2026-05-30
- Tags: process, milestones, scope

## Context

A relational database engine is many things at once: a file format, a buffer manager, an index structure, a catalog, a parser, a planner, an executor, a query API. Each of those is a project in its own right, and any of them can be made impressive in isolation.

The naive approach is to build "the parts that users see first" — a SQL parser, an `Open()`/`Query()` API, a CLI — and treat storage as something to fill in later. That's the path most weekend database projects take. It is the same path on which most of them get abandoned, because once you have a parser printing AST nodes you still have nothing that stores data.

The opposite approach — implement the storage layer first, then records, then a B+tree, then catalogue, then SQL — produces nothing flashy in the first weeks. But every step is provable, and once the storage stack is real the SQL layers on top are mostly translation.

GoDB has explicit milestones (M0 through M11) that codify this ordering. The decision to follow that ordering — versus, say, parallelizing parser and storage work, or "stubbing storage and building SQL first" — is what this ADR records.

## Decision

GoDB is built **strictly bottom-up**, in the order: storage → records → slotted page → B+tree (single page, then multi-page) → catalog → SQL (lexer/parser/AST) → planner → executor → public API → CLI → tests/docs → release.

In practice:

- Do not implement records before the pager works.
- Do not implement a B+tree before slotted pages work.
- Do not implement SQL before the catalog works.
- Do not implement the public API before the executor works.
- Do not implement the `database/sql` driver before the native API stabilizes.

Each milestone fully closes (with tests passing and a chapter in the book) before the next one starts.

## Consequences

**Enables.** Every milestone produces something demonstrably correct on its own. M1 (pager) can be tested in isolation against a temp file. M3 (slotted page) can be tested in isolation against an in-memory `*storage.Page`. M4 (single-page B+tree) sits on M3 and gets tested against the same surface. By the time SQL parsing arrives in M7, the underlying storage stack has been exercised by thousands of test runs.

This ordering also pays off when something breaks. A bug in M8 (executor) is bounded — it cannot be a storage bug, because storage has been settled for six milestones. Debugging is mostly diff-since-last-green.

**Constrains.** No early flashy demo. There is nothing to "show off" until M9 (executor) makes `SELECT * FROM users WHERE id = ?` end-to-end work. A reviewer looking only at the first few months sees a lot of test-heavy infrastructure code and no "feature."

**Reversibility.** Reversing the ordering on a single milestone (e.g. "let's stub the catalog and start on SQL") is technically possible but undermines the whole strategy. The order is the discipline.

## Alternatives considered

**Top-down: parser-first, then a stub storage that grows up.** The path most hobby DB projects take. Rejected: produces unrunnable code for months and tends to bake parser assumptions into the rest of the stack. The parser is also the easiest thing to over-engineer (see [PRD §7 risk #3](../prd.md)).

**Middle-out: build the catalog and a small executor against an in-memory store, then replace with real storage.** Common in production engines that need to ship a feature quickly. Rejected: GoDB's whole point is the storage layer. Building it last would let the rest of the engine encode assumptions about how storage should behave, then force storage to bend to match.

**Parallel tracks.** Three streams (storage, parser, API) advancing concurrently. Rejected: this is a solo project; parallelism is impossible without context-switching cost. Sequential is faster end-to-end here.

## Related

- Original spec §21 (recommended implementation order) and §31 (commits in implementation order).
- Book: [Chapter 01 — Foundations](../book/01-foundations-database-engines.md) — explains the layering this ordering follows.
- See also: ADR-0006 (no buffer pool in v0.1) — same scope-discipline principle applied to a single milestone.

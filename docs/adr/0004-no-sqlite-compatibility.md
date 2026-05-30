# ADR-0004: No SQLite file or SQL dialect compatibility

- Status: Accepted
- Date: 2026-05-30
- Tags: scope, product

## Context

GoDB is described as "SQLite-inspired." That description is deliberate and frequently misread. There are two flavors of compatibility someone might assume:

- **File format compatibility.** GoDB databases could be opened by `sqlite3`, and vice versa.
- **SQL dialect compatibility.** GoDB accepts the full SQLite SQL subset (or some agreed-upon overlap) and produces semantically identical results.

Either kind of compatibility would be a massive constraint on every design decision. Page format, record format, B+tree node layout, varint encoding, type affinity rules, NULL semantics, collation, function library — all of these would either have to clone SQLite or document precisely how they diverge.

GoDB's value proposition (a small, readable, idiomatic Go database engine usable as a learning vehicle and an embedded store) does not depend on compatibility with anything. The cost would be enormous; the benefit would be marginal.

## Decision

**GoDB is not, and will never be, compatible with the SQLite file format or SQL dialect.** "SQLite-inspired" means the architecture borrows SQLite's overall shape (single file, page-based, B+tree-backed, embedded, single-writer) — not the wire-level or syntax-level details.

Concretely:

- A `.godb` file cannot be opened by `sqlite3`. The magic bytes alone ("GODB" vs "SQLite format 3\000") prevent it.
- A `.sqlite` file cannot be opened by GoDB.
- SQL that works in `sqlite3` may or may not work in GoDB; the [SQL support matrix](../../docs/) (when M7+ lands) is the authoritative source.
- Type names happen to overlap (`INTEGER`, `TEXT`, `BOOLEAN`), but semantics may differ (e.g. GoDB does not have SQLite's type affinity).

This is not a "we might add compatibility later" — adding it would mean essentially rewriting the engine.

## Consequences

**Enables.** Every layer is free to make the choice that's best for GoDB's goals. Big-endian on disk ([ADR-0002](0002-big-endian-on-disk.md)). LEB128 varint ([ADR-0009](0009-leb128-uvarint.md)). Distinct NULL/empty-text encodings ([ADR-0008](0008-null-and-empty-text-distinct.md)). A small reserved row-version byte ([ADR-0007](0007-explicit-kind-byte-values.md)). None of these are constrained by what SQLite chose.

The book can teach concepts without saying "and here's why SQLite does it slightly differently." The CLI's `inspect` commands can show GoDB-specific structures without trying to mimic `sqlite3`'s output.

**Constrains.** GoDB cannot inherit any SQLite tooling. No `sqlite3` CLI, no SQLite-aware ORMs, no SQLite test corpus. Each of these has to be built (or imported from elsewhere) if and when needed.

**Reversibility.** Not reversible. A "GoDB compatibility mode" would be a separate engine.

## Alternatives considered

**Partial compatibility (read-only SQLite support).** Could let users migrate. Rejected: SQLite's format is complex; partial support is a footgun (silently wrong on edge cases). If someone wants SQLite, they should use SQLite.

**SQL dialect-only compatibility.** Same parser surface, different on-disk format. Rejected: parser scope already needs to be aggressively limited (see [PRD §7 risks](../prd.md)). Cloning SQLite's syntax would push us into supporting every quirk (type affinity, implicit conversions, edge cases) we explicitly don't want.

**Drop the "SQLite-inspired" framing entirely.** Considered. Rejected: the framing is honest — the architecture genuinely is shaped by SQLite. The phrasing helps readers calibrate scope ("embedded, single-file, B+tree, SQL-ish") without committing to compatibility.

## Related

- [PRD §2.2 (non-vision)](../prd.md), §8 (out of scope)
- Code: [internal/storage/header.go](../../internal/storage/header.go) — the "GODB" magic explicitly differs from SQLite's
- See also: ADR-0005 (bottom-up build order) — a compatibility constraint would invalidate that order entirely.

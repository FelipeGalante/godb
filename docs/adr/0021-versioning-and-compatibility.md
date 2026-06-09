# ADR-0021: Semantic versioning and the public/internal compatibility boundary

- Status: Accepted
- Date: 2026-05-31
- Tags: process, release, api

## Context

M11 tags `v0.1.0` — the first release someone can `go get` / `go install`. The
moment a version is tagged and the module proxy caches it, the project makes
implicit promises: which import paths can a user depend on without expecting
churn, what does a minor-version bump mean, and can an old `.godb` file still be
opened after an upgrade. Those promises need to be stated once, deliberately,
rather than discovered the hard way by the first user whose build breaks on a
`go get -u`.

GoDB's layout already draws a line: `internal/` is implementation
([ADR-0003](0003-internal-vs-pkg-layout.md)), and `pkg/` is the importable
surface. Go's tooling enforces that *importability* line (you cannot import
another module's `internal/`), but it says nothing about *stability* — whether
`pkg/godb`'s function signatures, the CLI's flags, or the on-disk format are
allowed to change between releases. The book and README repeatedly call v0.1
"small" or "bounded"; we need to be precise about what that means for someone
building on it.

There's also a concrete pre-1.0 question. GoDB is `v0.x`. Under semver, the
`0.y.z` series carries no API-stability guarantee at all — anything may change.
Taken literally that would make v0.1 useless to depend on. We want a softer,
stated policy instead.

## Decision

GoDB follows [Semantic Versioning](https://semver.org). The **stable surface**,
within a given minor version (`v0.1.x`), is:

- the public Go API in **`pkg/godb`**,
- the **`database/sql` driver** in `pkg/driver` (the registered `"godb"` name and
  its behavior through `database/sql`),
- the **`godb` CLI** — its commands, flags, and exit codes,
- the **on-disk `.godb` file format** — a file written by one `v0.1.x` is
  readable by any other `v0.1.x`.

Explicitly **not** covered, and free to change in *any* release including
patch releases:

- everything under **`internal/`** (storage, btree, catalog, sql, planner, exec,
  cli internals) — depend on it only by vendoring or copying, never by import,
- the development tooling, test helpers, and docs structure.

Pre-1.0 interpretation: while in `v0.x`, a **minor** bump (`v0.1` → `v0.2`) may
make breaking changes to the stable surface above — that is where breaking
changes are allowed to land and will be called out in the CHANGELOG. **Patch**
releases (`v0.1.0` → `v0.1.1`) are backward-compatible bug fixes only. When the
on-disk format must change, the new version ships a documented migration path
rather than silently refusing or corrupting old files; a file is never made
unreadable within its own minor series.

The version string is not exposed as a public Go constant. The git tag is the
source of truth for the library; the CLI carries a single internal `version`
constant only for `godb -version`.

## Consequences

**Enables.** A user can `go get github.com/felipegalante/godb/pkg/godb` and pin
`v0.1.x`, knowing the API and their `.godb` files will hold across patch
releases. CLI scripts can rely on commands, flags, and exit codes. The project
keeps full freedom to refactor `internal/` — which is most of the codebase, and
where v0.2's buffer pool and transactions will churn heavily — without a major
bump. The CHANGELOG and this ADR give one place to learn the rules.

**Constrains.** Once `v0.1.0` is tagged, the on-disk format is effectively frozen
for the `v0.1.x` line: a format change means at least a minor bump plus a
migration. The four stable surfaces now carry a real compatibility cost for
every change — which is the point, but it means API mistakes shipped in `v0.1.0`
live until `v0.2`. Not exposing a public `Version` constant means programmatic
version checks must read build info (`runtime/debug.ReadBuildInfo`) rather than a
GoDB symbol; acceptable, and avoidable churn on the public surface.

**Reversibility.** The policy is a promise, not code; it can be tightened or
loosened in a future ADR. The hardest-to-reverse part is the on-disk format
guarantee — breaking it retroactively would strand existing files — so that one
is treated as load-bearing from `v0.1.0` onward.

## Alternatives considered

**State no policy.** Tag `v0.1.0` and let users infer stability. Rejected: the
first `go get -u` that breaks a build, or the first upgrade that won't open an
old file, turns an unstated assumption into a bug report. A release is exactly
the moment to write the contract down.

**"Everything is unstable" (literal semver 0.x).** Declare that nothing is
guaranteed until `v1.0.0`. Rejected: technically defensible but practically
hostile — it makes v0.1 undependable and contradicts the PRD's success criterion
that "a developer can install and use" GoDB. A stated soft policy is more honest
about what we actually intend to keep stable.

**Go straight to `v1.0.0`.** Signal full API stability now. Rejected: v0.2 will
deliberately reshape things (transactions touch the API and the on-disk format;
the buffer pool reshapes `internal/storage`). Committing to 1.0 semantics before
those land would force either a premature `v2` module path or breaking 1.x's
promise. `v0.x` with a clear soft policy fits a pre-stable engine.

## Related

- [ADR-0003 (`internal/` vs `pkg/` layout)](0003-internal-vs-pkg-layout.md) — draws
  the importability line this ADR turns into a stability line.
- [ADR-0001 (single file, fixed pages)](0001-single-file-fixed-pages.md) and
  [ADR-0002 (big-endian on-disk)](0002-big-endian-on-disk.md) — the on-disk
  format whose within-a-version stability this ADR promises.
- [ADR-0017 (no transactions in v0.1)](0017-no-transactions-in-v0-1.md),
  [ADR-0016 (rows materialized)](0016-rows-materialization.md) — examples of
  documented limitations that a future minor version is allowed to change.
- Book: [Chapter 13 — Releasing v0.1 (M11)](../book/13-milestone-11-release.md).
- [CHANGELOG.md](../../CHANGELOG.md) — where breaking changes per release are
  recorded.
- External: [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html), the
  Go [modules reference](https://go.dev/ref/mod) (versioning, the module proxy).

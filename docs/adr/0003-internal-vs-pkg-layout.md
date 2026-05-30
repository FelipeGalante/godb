# ADR-0003: `internal/` for implementation, `pkg/godb` for stable API

- Status: Accepted
- Date: 2026-05-30
- Tags: layout, packaging, api

## Context

GoDB has two audiences at the Go-import level: the engine itself (internal layers — storage, record, btree, catalog, planner, executor, …) and developers using GoDB from their application. These audiences want very different stability guarantees:

- The internal layers churn heavily as the engine grows. We will rename `InsertCell` to `InsertLeafCell` when M5 lands the multi-page B+tree, change error types, swap encodings — and we'd like to do all of that without breaking application code.
- Application developers want a small, stable surface: `Open`, `Close`, `Exec`, `Query`, `Begin`, `Rows.Scan`. They should never accidentally import a low-level type that disappears two milestones later.

Go's `internal/` directory is a language-level enforcement of this: packages under `internal/` can only be imported by packages rooted at the same module path. An external consumer literally cannot `import "github.com/felipegalante/godb/internal/btree"`.

## Decision

GoDB uses the following package split:

- **`internal/`** holds all implementation packages. Subdirectories: `storage`, `record`, `btree`, `catalog`, `sql`, `planner`, `exec`, `tx`, `engine`, `buffer`. Anything not yet built has a placeholder directory.
- **`pkg/godb/`** is the stable public API that application developers import: `Open`, `DB`, `Tx`, `Rows`, options, and typed errors. Re-exports from `internal/engine` and `internal/record` where appropriate.
- **`pkg/driver/`** (later) exposes a `database/sql/driver` driver registered as `"godb"`, built on top of `pkg/godb`.
- **`cmd/godb/`** is the CLI binary.

The first compile error a hypothetical external user gets when reaching for an internal type tells them they reached for the wrong thing — it's a feature, not a bug.

## Consequences

**Enables.** Heavy internal refactors with zero public API surface impact. Confidence that any change inside `internal/btree` cannot break a downstream user, only break tests and the public-API wrappers. A clear social signal in code review: changes to `pkg/godb` are public-API changes and deserve more scrutiny than changes anywhere else.

**Constrains.** Two layers to traverse for any new public functionality (build it under `internal/`, then expose it through `pkg/godb`). For tiny things that's overhead, but tiny things are rare.

**Reversibility.** Easy to extend: if a previously-internal package needs to become public (e.g. publishing the schema types so users can construct schemas programmatically), wrap or re-export from `pkg/godb`. The reverse — taking back something that was public — is what `internal/` exists to prevent.

## Alternatives considered

**Flat layout, everything at the module root.** Common in small Go libraries. Rejected: no enforcement of internal/external split. Renaming a type or moving a function becomes a breaking change for every user.

**`internal/` only, no `pkg/`.** Some projects do internal-only and expose everything from the module root. Rejected: with multiple public-facing concerns (the embedded API, the `database/sql` driver, possibly CLI helpers later), a dedicated `pkg/` keeps each public artifact in its own directory.

**`api/` instead of `pkg/`.** Cosmetic. Either works; `pkg/` is more common in the Go community.

## Related

- Code: [pkg/godb/](../../pkg/godb/) (placeholder), [internal/](../../internal/)
- Book: [Chapter 02 — The Skeleton (M0)](../book/02-milestone-0-skeleton.md)
- See also: Go's documentation on internal packages — `go doc cmd/go internal`.

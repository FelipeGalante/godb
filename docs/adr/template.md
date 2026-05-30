# ADR-NNNN: <decision title in plain English>

- Status: Proposed | Accepted | Superseded by ADR-XXXX | Deprecated
- Date: YYYY-MM-DD
- Tags: <comma-separated, e.g. storage, encoding, layout, process>

## Context

The situation that forced the choice. What was unclear, what we couldn't punt, why doing nothing wasn't a valid option. Two or three paragraphs at most — point at the relevant code, milestone, or spec section if it helps.

## Decision

What we chose, stated plainly in one or two sentences. The first line of this section should be readable on its own — e.g. "GoDB uses fixed 4 KB pages for all database files in v0.1." Detail goes underneath.

## Consequences

What this enables (positive consequences) and what this constrains (negative consequences). Be honest. Note reversibility: is the decision easy to back out of, or does it bake into the on-disk format / public API / package boundaries?

## Alternatives considered

The other realistic paths and why each was rejected. One paragraph per alternative. If the alternative was "do nothing / defer," say so and why it wasn't acceptable.

## Related

- Other ADRs this depends on or supersedes.
- Book chapters that explain the surrounding concept.
- Code paths the decision shows up in (with `path/to/file.go:line` references where useful).
- Spec sections, RFCs, papers, blog posts, etc.

---

## How to use this template

1. Copy this file to `docs/adr/NNNN-kebab-case-title.md` (pick the next ADR number).
2. Fill in every section. Resist the temptation to skip "Alternatives" — if the decision had no alternatives, the ADR isn't needed in the first place.
3. Link the new ADR from `docs/adr/README.md`.
4. Reference the ADR from any code comment or book chapter that needs to explain the same decision.
5. ADRs are immutable once accepted, except for `Status` (which can flip to Superseded / Deprecated). Write a new ADR rather than editing an existing one.

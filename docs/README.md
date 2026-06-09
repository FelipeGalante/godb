# GoDB documentation

This directory holds everything that isn't code:

- **[Usage guide](usage/)** — how to use GoDB today and what the current release supports. Start here if you want to *run* GoDB rather than read its internals.
- **[Product Requirements Document](prd.md)** — what GoDB is, who it's for, what v0.1 needs to do, what is deliberately out of scope.
- **[Architecture Decision Records](adr/)** — the load-bearing decisions made along the way, each with the context that forced them, the alternatives considered, and the consequences accepted.
- **[The development book](book/)** — an internals companion explaining the database-engine concepts behind each layer, then walking through the code. Reads from the start of the project to the current milestone.

If you are reading the repo cold, start with the [usage guide](usage/) if you want to know what works today, or the [book introduction](book/00-introduction.md) if you want the engineering narrative from the first commit. The PRD answers *what is being built*; the ADRs answer *why the design looks like this*; the book answers *how the implementation works*; the usage guide answers *what can I do with GoDB right now*.

## Documentation cadence

Every milestone's commit cycle is expected to include documentation updates, not just code. Specifically:

- **A book chapter** under `book/` covering the milestone's foundation, decisions, code walkthrough, tests, and known gaps. Update the book index (`book/README.md`), the intro's milestone table (`book/00-introduction.md`), and the "after chapter N" footer line in both.
- **A top-level [README](../README.md) update** — refresh the *Project status* paragraph if its accuracy changed, and update user-visible capability notes when a new feature landed.
- **A [usage guide](usage/) update** — if the milestone introduced something a user can do (a new API surface, a CLI command, a migration tool, a new internal pattern that's worth showing in `current-state.md`). Add a section to `current-state.md` or a new page (e.g. `embedded-api.md`, `cli.md`, `transactions.md`) as the surface area grows.
- **An ADR** under `adr/` *only when* a load-bearing decision was made that future contributors might un-do without realizing the cost. Most milestones don't need one.

The PRD (`prd.md`) gets revised when product direction changes, not per-milestone.

A milestone's plan should call out which of these will be touched before implementation starts, so docs land in the same commit cycle and don't trail behind the code.

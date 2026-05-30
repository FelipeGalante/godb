# Chapter 02 — The Skeleton (M0)

## Where we are

You've finished [Chapter 01](01-foundations-database-engines.md) and have a mental model of the layered architecture: pager at the bottom, SQL at the top, with records, pages, and trees in between. We haven't written a line of code yet. Before we can write the *first* line — which will be in M1, the pager — we need a project to write it into. M0 sets that up: a Go module, the directory tree the engine will fill, a Makefile so common tasks are one command away, a CI workflow so the test suite is enforced from day one, and a CLI binary that does nothing but prove the build wires together.

This is the least flashy chapter in the book. It is also the one without which every other chapter would slowly fall apart.

## Foundation

There's a phenomenon in solo software projects where you spend the first session writing the *most fun* code (the parser, the UI, whatever caught your imagination) and then spend the next several months fighting unrelated infrastructure decisions you punted. Should the package live here or there? Is the test suite reliable? Does CI even run? What does "the build" mean? Each one is a tiny irritant; in aggregate they sap the energy you'd rather spend on actual implementation.

The cure is unsexy: do the boring setup work first, before there's anything interesting to write. Pick your package layout, install your tooling, get CI green on day zero. The skeleton you set up in the first hour is the skeleton you'll work inside for months.

### What "the skeleton" includes

For a Go project of GoDB's shape, "skeleton" means:

- A `go.mod` with the module path and the Go version pinned.
- A directory tree that reflects the layered architecture, even if most layers are empty placeholders.
- A `Makefile` (or similar) with the handful of commands you'll type fifty times a day: `test`, `vet`, `build`, `fmt`.
- A CI workflow that runs `test`, `vet`, and the race detector on every push and PR.
- A `LICENSE`, a `README`, a `.gitignore` — the social plumbing that turns a folder of files into a project.
- A binary entry point at `cmd/<tool>/main.go` that builds, even if it does almost nothing.

None of that is unique to a database. It's the baseline for any Go project that wants to be taken seriously by future-you, six months in.

### Internal vs. external packages

The Go language has a useful enforcement mechanism: packages under a directory named `internal/` can only be imported by packages rooted at the same module path. An external consumer literally cannot `import "github.com/felipegalante/godb/internal/btree"` — the compiler refuses.

This matters for GoDB because we have two audiences:

- **The engine itself** churns. The B+tree's API will change between M4 and M5. The pager will get a buffer pool in v0.2 and the signature of `ReadPage` will probably change. Every internal package will be torn up at least once.
- **Application developers using GoDB** want a small, stable surface: `Open`, `Close`, `Exec`, `Query`, `Begin`, `Rows.Scan`. They want to write their app once and not get bitten when GoDB releases v0.2.

[ADR-0003](../adr/0003-internal-vs-pkg-layout.md) records the choice: everything under `internal/` is implementation; `pkg/godb/` is the stable public API. The first compile error a hypothetical external user gets when reaching for an internal type is a feature, not a bug.

In M0 we set up *both* — `internal/` filled with empty placeholder directories (one per future package), and `pkg/godb/` likewise empty. The `.gitkeep` files in each empty directory exist solely to make git track them.

### CI from day one

The temptation, when starting a project, is to "add CI later." Don't. CI from day one does three things even when there are zero tests to run:

- It catches "it builds on my machine but not on Linux" issues at the first commit, not the hundredth.
- It establishes the discipline that the test suite is non-optional. By the time you have tests, the harness already runs them.
- It gives you a green badge to start from, so the moment a regression lands, you notice.

GoDB's CI is a single GitHub Actions workflow that runs `go vet`, `go test ./...`, and `go test -race ./...` on Go 1.22 on every push and PR. That's it. It will grow over time. M0 ships it.

## Decisions

| Decision | Why | Where |
|---|---|---|
| Single `.godb` extension | Distinctive; not confusable with `.sqlite` or `.db` | (lives in the file format; established in M1) |
| `internal/` for impl, `pkg/godb` for public API | Stable public surface, free internal churn | [ADR-0003](../adr/0003-internal-vs-pkg-layout.md) |
| Bottom-up build order | Each layer testable in isolation | [ADR-0005](../adr/0005-bottom-up-build-order.md) |
| Go 1.22 pinned via `mise` | Reproducible toolchain across machines | `mise.toml` |
| Conventional Commits-style messages | Searchable changelog | `git log --oneline` shows the pattern |
| GitHub Actions for CI | Standard, free, integrates with PRs | `.github/workflows/ci.yml` |

## The code

There isn't much. Open each file in turn:

- [`go.mod`](../../go.mod) — module path `github.com/felipegalante/godb`, `go 1.22`. Nothing else.
- [`mise.toml`](../../mise.toml) — pins Go 1.22 for the project via [mise](https://mise.jdx.dev/), the version manager. Committing this means anyone with mise installed can `mise install` and get the right Go version. Without it, contributors are at the mercy of whatever Go they happen to have.
- [`Makefile`](../../Makefile) — eight targets: `test`, `race`, `fmt`, `vet`, `lint`, `build`, `run`, `clean`. Most are one-liners around `go` commands. `lint` softly degrades if `staticcheck` isn't installed (so the Makefile doesn't break a fresh checkout). `clean` removes the binary plus any `*.godb` and `*.godb-journal` files left over from local testing.
- [`.gitignore`](../../.gitignore) — ignores the built binary, `*.godb` files, profiling output, editor cruft.
- [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml) — single job (`test`) on `ubuntu-latest`. Checks out, sets up Go 1.22 with module caching, runs `go vet`, `go test ./...`, `go test -race ./...` in that order. No lint job (we deferred staticcheck until it has something worth catching).
- [`cmd/godb/main.go`](../../cmd/godb/main.go) — five lines. Prints `godb: SQLite-inspired database engine in Go`. The point isn't the print; it's proving the module path and build pipeline work. The real CLI lands in M10.
- [`README.md`](../../README.md) — what GoDB is, what it isn't, install/build instructions, roadmap, license. Honest about being pre-alpha.
- [`LICENSE`](../../LICENSE) — MIT.

The directory tree is also part of the deliverable:

```
cmd/godb/
internal/{storage,buffer,record,btree,catalog,sql,planner,exec,tx,engine}/
pkg/{godb,driver}/
docs/
testdata/{sql,golden,corrupt}/
```

Every directory under `internal/` and `pkg/` (and `docs/`, `testdata/sql/`, etc.) gets a `.gitkeep` so git tracks them. Once a directory has real files, the `.gitkeep` is removed. By the time you're reading this, `internal/storage/`, `internal/record/`, `internal/btree/`, and `docs/` have grown real contents.

## Tests as proof

There are no tests in M0. `go test ./...` returns successfully because there are no test files to fail, and that's the bar: the test command runs cleanly without error so that future-tests don't fight an already-broken pipeline.

## What this layer cannot do yet

Everything. M0 is the empty house. You can `go run ./cmd/godb` and see a banner. That is the entire feature set.

What chapter 03 (M1) adds: an actual storage layer — a `Pager` that opens a `.godb` file, allocates pages, reads them, writes them, and persists a validated header across opens. The first time you'll be able to put a byte somewhere and get it back after restarting the process.

## Where the next chapter picks up

Chapter 03 starts with the question every database engine has to answer first: *how do we put bytes on disk in a way we can read them back?* That sounds trivial until you start writing it. There's a file. The file has a header. Pages are fixed size. How do we extend the file safely? What's a `pread`? When does the OS actually flush our writes? What do we do if the magic bytes are wrong?

By the end of M1 we'll have an answer — a small, well-tested pager that knows nothing about rows but everything about the file format. It's the foundation everything else sits on.

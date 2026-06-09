# Chapter 13 — Releasing v0.1 (M11)

## Where we are

By the end of [Chapter 12](12-milestone-10-cli.md) the engine was complete for
v0.1: storage, records, a B+tree, a catalog, a SQL frontend, a planner and
executor, a native Go API, a `database/sql` driver, and a CLI. Every success
criterion in the [PRD](../prd.md) was met — create and reopen a database, make
tables, insert and select by primary key, scan results, thousands of rows, a
catalog that persists, a CLI that inspects and checks, all tests and race tests
green.

What hadn't happened: cutting a release. There was no version anyone could
depend on, no `go install` that worked, no changelog, no statement of what's
stable. The repository was a finished engine that nobody could yet *get*.

M11 is that step, and only that step. It adds **no engine code**. It turns the
repository into something a stranger can install and build on:

1. A stable version string (`godb 0.1.0`, no more `-dev`).
2. A `CHANGELOG.md`.
3. A real install story in the README (`go install`, `go get`).
4. A versioning-and-compatibility policy ([ADR-0021](../adr/0021-versioning-and-compatibility.md)).
5. A pushed, annotated `v0.1.0` git tag.

This is the shortest chapter in the book, because releasing is mostly *deciding
what you're promising* and then doing a few mechanical things carefully.

## Foundation

### How a Go module is released

Releasing a Go library is unusual: there is no build artifact to upload, no
registry to push to, no package to publish. A release is **a git tag**. That's
it. When you run

```bash
git tag -a v0.1.0 -m "GoDB v0.1.0"
git push origin v0.1.0
```

you have released. From that moment a user anywhere can:

```bash
go install github.com/felipegalante/godb/cmd/godb@v0.1.0   # the CLI binary
go get github.com/felipegalante/godb/pkg/godb@v0.1.0       # the library
```

The plumbing behind this is the **module proxy** (`proxy.golang.org` by
default). When someone asks for `…@v0.1.0`, the Go toolchain asks the proxy,
which (the first time) fetches the tag from GitHub, repackages it as a module
zip, records a checksum in the transparency log, and caches it forever. The tag
is immutable: once `v0.1.0` is cached with a given checksum, re-pointing the tag
won't change what users get — which is exactly why you tag *after* the commits
are final and pushed.

The version must be a valid semver tag with a leading `v` (`v0.1.0`, not
`0.1.0`). The module path in `go.mod` is the identity; the tag is the version of
that identity.

### The case-sensitivity trap

GoDB's `go.mod` declares `module github.com/felipegalante/godb` — all lowercase.
The GitHub repository is at `github.com/FelipeGalante/godb` — capital F. Git and
GitHub treat the URL case-insensitively, so `git push` and `git clone` don't
care. The module proxy is fussier: it case-encodes module paths (an uppercase
letter becomes `!` + lowercase) so that case-insensitive filesystems can't
collide. In practice the canonical path is whatever `go.mod` says, and GitHub
resolves the lowercase form fine — but "in practice" is not "verified." The one
release check that can't be skipped is a real `go get` of the lowercase path
from a throwaway module on a clean module cache. If that resolves and builds, the
release is sound; if it doesn't, no amount of local testing would have caught it,
because locally you're in the module, not fetching it.

### What a version *promises*

The deeper part of releasing isn't mechanical — it's deciding what `v0.1.0`
commits you to. The instant a tag is cached, users infer guarantees: that they
can pin it, that `go get -u` within the line won't break their build, that a
`.godb` file written today opens tomorrow. Semver's literal `0.x` rule says
"no guarantees until 1.0," which would make v0.1 undependable. So GoDB states a
softer policy explicitly rather than leaving it to be discovered. That policy is
the substance of this milestone, and it lives in [ADR-0021](../adr/0021-versioning-and-compatibility.md).

## Decisions

- **The stable surface is four things** — `pkg/godb`, `pkg/driver`, the `godb`
  CLI (commands/flags/exit codes), and the on-disk `.godb` format — stable
  within a minor version. Everything under `internal/` is explicitly *not*
  covered and may change in any release. [ADR-0021](../adr/0021-versioning-and-compatibility.md).
- **Pre-1.0 semantics, stated softly.** Minor bumps (`v0.1` → `v0.2`) are where
  breaking changes are allowed and will be called out in the CHANGELOG; patch
  releases are bug-fixes only; the on-disk format never becomes unreadable
  within its own minor series (a format change ships a migration). ADR-0021.
- **No public `pkg/godb.Version` constant.** The git tag is the source of truth
  for the library. Exposing a Go constant would be one more public symbol to
  keep honest, and programs that truly need the build version can read
  `runtime/debug.ReadBuildInfo`. The CLI keeps a single internal `version`
  constant purely for `godb -version`.
- **Annotated tag, not lightweight.** `git tag -a v0.1.0` carries a message,
  author, and date — it's the release record, and GitHub renders it as a release.
- **CHANGELOG follows Keep a Changelog.** A human-readable, grouped summary with
  an `[Unreleased]` section at the top for v0.2 work, not a dump of `git log`.

## What changed

Almost nothing, which is the point.

- **[`internal/cli/cli.go`](../../internal/cli/cli.go)** — the one line of code in
  this milestone: `const version = "godb 0.1.0-dev (M10)"` becomes
  `"godb 0.1.0"`. Both `godb -version` and the shell banner read this constant,
  so the single edit covers both.
- **[`CHANGELOG.md`](../../CHANGELOG.md)** (new) — the v0.1.0 entry, grouped by
  area, plus the limitations recap.
- **[`README.md`](../../README.md)** — the `Install (dev)` section becomes a real
  `Install` section (`go install` for the CLI, `go get` for the library, `from
  source` underneath); the status line moves from "pre-alpha" to "v0.1.0"; a
  consolidated `v0.1 limitations` section; the release status moves to M11.
- **[ADR-0021](../adr/0021-versioning-and-compatibility.md)** (new) — the policy
  above.
- Docs index/status refreshes (`docs/book/`, `docs/usage/`) and a `CLAUDE.md`
  status bump.

## Tests as proof

There's no new code to unit-test, so the "test" for a release is a different
thing: **does a clean machine actually get a working module from the tag?** The
release verification is the proof, and it runs in two phases.

*Before tagging*, with `main` pushed: in a throwaway module outside the repo,
`go get github.com/felipegalante/godb/pkg/godb@main`, write a ten-line program
(Open → `CREATE TABLE` → `INSERT` → `Query` → `Scan`), and run it. This proves
the lowercase module path resolves against the real proxy despite the capital-F
remote — the one failure mode local testing can't surface.

*After tagging and pushing the tag*: from another fresh module,
`go install github.com/felipegalante/godb/cmd/godb@v0.1.0` and run the binary
against a scratch database (`exec` a schema, `query`, `check`); and
`go get …/pkg/godb@v0.1.0` and rebuild the same little program. If the proxy
hasn't caught up yet, `GOPROXY=direct` fetches straight from GitHub. When both
phases pass, `v0.1.0` is genuinely installable, not just green on the author's
machine.

## What this layer cannot do yet

A release doesn't add capability, so the gaps are the same ones every prior
chapter has been honest about — now frozen into a version and pointed at v0.2:

- **No transactions, no buffer pool, no freelist, no secondary indexes** — the
  big v0.2 items.
- **A thin SQL surface** — no `UPDATE`/`DELETE`, no joins or aggregates, `WHERE`
  only on the primary key.
- **No release automation** — the tag is cut by hand; there's no GitHub Actions
  release workflow or `goreleaser`. Fine for v0.1; a candidate convenience for
  later.
- **No prebuilt binaries** — installation is `go install` from source via the
  toolchain; there are no downloadable platform binaries attached to the release.

The difference now is that these are *versioned* promises: the on-disk format and
the four stable surfaces won't shift under a user within the `v0.1.x` line, and
when they do shift, it'll be a minor bump with a CHANGELOG entry and (for the
format) a migration.

## Further reading

- The Go [modules reference](https://go.dev/ref/mod) — versioning, the module
  proxy, `go install …@version`, minimal version selection.
- [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html) — the spec the
  compatibility policy is built on, including the `0.x` clause.
- [Keep a Changelog](https://keepachangelog.com/) — the format `CHANGELOG.md`
  follows.
- [Publishing a module](https://go.dev/doc/modules/publishing) — the official
  walkthrough of exactly the tag-and-push step this chapter performs.

## Where the next chapter picks up

v0.2. With v0.1 tagged and the compatibility line drawn, the next phase is the
first one allowed to change the stable surface: a buffer pool in front of the
pager, transactions with a rollback journal (so `Begin` finally returns a real
`*Tx`), `UPDATE`/`DELETE`, range scans beyond primary-key equality, secondary
indexes, freelist reuse, and page checksums. Several of those touch both the API
and the on-disk format — which is exactly why they wait for a minor bump rather
than slipping into a `v0.1.x` patch.

That's where the book continues — one milestone, one chapter, as it has from the
first commit.

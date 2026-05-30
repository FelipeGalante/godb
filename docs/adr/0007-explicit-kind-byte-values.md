# ADR-0007: Explicit byte values for `record.Kind` (no `iota`)

- Status: Accepted
- Date: 2026-05-30
- Tags: encoding, on-disk, record

## Context

The `record.Kind` enum tags every value stored in a row payload — `NULL`, `INTEGER`, `TEXT`, `BOOLEAN` — and the kind byte is written to disk as the first byte of every encoded value. If a kind byte's numeric value ever changes, every existing `.godb` file becomes unreadable (or worse, silently misread).

Go's idiomatic way to define a numeric enum is with `iota`:

```go
type Kind uint8

const (
    KindNull Kind = iota
    KindInteger
    KindText
    KindBoolean
)
```

This is concise and a little dangerous: alphabetizing the constants, inserting a new kind in the middle, or accidentally swapping two lines silently changes the on-disk values.

For variables only ever used in memory, `iota` is fine. For on-disk constants, it's a footgun.

## Decision

`record.Kind` constants use **explicit byte values** rather than `iota`:

```go
// internal/record/value.go
type Kind uint8

const (
    KindNull    Kind = 0
    KindInteger Kind = 1
    KindText    Kind = 2
    KindBoolean Kind = 3
)
```

A comment in `value.go` calls out that these are part of the on-disk format and must not be reordered. Any new kind gets a new, never-before-used value.

The same rule applies to `storage.PageType` (the page-type tag at byte 0 of every page) and to any other on-disk discriminator we add in the future.

## Consequences

**Enables.** The on-disk values are visible in code review. A diff that shuffles constants is obviously wrong. New contributors (and the book chapters) can refer to `KindInteger = 1` as a documented fact, not an emergent property of where the constant happens to sit in a list.

**Constrains.** A pinch more verbose. That's it.

**Reversibility.** Trivially reversible (in code), but if we ever ship a `.godb` file to anyone, the values are locked for that format version.

## Alternatives considered

**Use `iota` with a unit test that asserts the values.** Some projects do this. Rejected: the test guards against accidental change but not against intentional-but-wrong change ("I'm adding `KindFloat`, so I'll just stick it at the top — and update the test too"). Explicit values make the intent obvious without needing a test to enforce it.

**Use stringly-typed kinds on disk.** Trivially debuggable but enormously wasteful for billions of values. Rejected on space grounds alone.

## Related

- Code: [internal/record/value.go](../../internal/record/value.go), [internal/storage/page.go](../../internal/storage/page.go)
- Book: [Chapter 04 — Records: Variable-Length Typed Values](../book/04-milestone-2-records.md)
- See also: ADR-0008 (NULL and empty TEXT distinct) — depends on the kind byte being stable.

# ADR-0008: `NULL` and empty `TEXT` have distinct on-disk encodings

- Status: Accepted
- Date: 2026-05-30
- Tags: encoding, record, semantics

## Context

In relational semantics, `NULL` and the empty string are different things. `NULL` means "no value present"; `""` means "a value is present and it is the empty string." They behave differently in `WHERE` clauses (`WHERE x = ''` does not match `NULL`), they aggregate differently (`COUNT(x)` ignores `NULL` but counts `''`), and they have different uniqueness behaviors.

When encoding values to disk, the engine has to decide whether this distinction is preserved at the byte level or whether the higher SQL layer (the executor) reconstructs it from context. The simplest possible encoding ("a value is a `kind` byte plus a length plus bytes") would make `NULL` and `""` look identical: kind = TEXT, length = 0, no bytes.

Once on-disk encoding loses this distinction, restoring it later is impossible — the executor has no way to know which one the writer meant.

## Decision

GoDB encodes `NULL` and empty `TEXT` as **distinct byte sequences**:

```
NULL       : [0x00]                 // 1 byte: kind=NULL, no payload
TEXT ""    : [0x02, 0x00]           // 2 bytes: kind=TEXT, uvarint length=0
```

The `NULL` kind has no length byte and no payload — its mere presence is the encoding. The `TEXT` kind always includes a uvarint length, even when the length is zero, and zero or more UTF-8 bytes after.

`EncodeValue` produces these encodings; `DecodeValue` round-trips them. There is an explicit test (`TestNullAndEmptyTextAreDistinct`) that asserts the encoded byte slices are not `bytes.Equal`.

## Consequences

**Enables.** SQL semantics are correctly preserved through the storage layer. `NULL` and `""` can both be inserted into the same `TEXT` column and be distinguished on read. Aggregates and `WHERE` clauses (when M9 lands) have unambiguous truth to work from.

**Constrains.** Empty TEXT costs an extra byte per value compared to NULL. For a column that is mostly NULL, this is irrelevant. For a column that is mostly empty strings, it's a one-byte-per-row overhead — vanishing for any non-trivial row.

**Reversibility.** Cannot be changed without a file-format major version bump.

## Alternatives considered

**Use the same encoding for both, distinguish at the column level.** Possible if every column declared its nullability and we used a separate NULL bitmap (one bit per column at the start of the row). Rejected: more complex, doesn't even save the byte for the common case where columns are nullable but the value is present (the bitmap needs all those bits anyway), and SQLite doesn't use that approach.

**Encode empty TEXT as a special length-1 sentinel.** Avoids the extra byte. Rejected: tricky, error-prone, and saves a single byte in a rare case.

**Disallow empty strings entirely.** Some databases (early Oracle) treated empty TEXT as NULL. Rejected as a footgun for users coming from SQLite/Postgres, where they are distinct.

## Related

- Code: [internal/record/codec.go](../../internal/record/codec.go) — `EncodeValue` / `DecodeValue`; the `TestNullAndEmptyTextAreDistinct` test in [codec_test.go](../../internal/record/codec_test.go).
- Book: [Chapter 04 — Records: Variable-Length Typed Values](../book/04-milestone-2-records.md)
- See also: ADR-0007 (explicit kind byte values) — preserves the kind byte that distinguishes them.

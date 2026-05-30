# Chapter 04 — Records: Variable-Length Typed Values (M2)

## Where we are

[Chapter 03](03-milestone-1-pager.md) gave us a pager — a way to read, write, and allocate fixed-size 4 KB pages in a `.godb` file. The pager treats every page as an opaque byte array. We can put bytes anywhere; we just have no idea what those bytes mean.

This chapter builds the first interpretive layer. By the end of M2 we have a typed value system (`NULL`, `INTEGER`, `TEXT`, `BOOLEAN`), a row codec that turns a `[]Value` into a byte slice and back, and a schema with validation rules. None of it touches disk — M2 produces bytes that M3 will store inside slotted-page cells, and the resulting cells will live in pages owned by the M1 pager.

It is a deliberately I/O-free milestone. That isolation is the whole reason it gets its own commit and its own chapter.

## Foundation

### Variable-length typed data on disk: the actual hard problem

When you store a row in a relational database, you're storing a sequence of typed values whose lengths vary:

```
[id=42 (INTEGER), name="Felipe" (TEXT, 6 bytes), active=true (BOOLEAN)]
```

If you simply concatenate the raw values — 8 bytes for `42`, 6 bytes for `"Felipe"`, 1 byte for `true` — you get 15 bytes. But when you read them back, you have no idea where one value ends and the next begins, what type each value is supposed to be, or whether any of them are NULL. The bytes are useless without a *schema*.

You can solve this two ways:

1. **Make the bytes self-describing.** Tag each value with its type and length so the decoder can read it back without any external knowledge.
2. **Keep the schema separate.** Store the schema once (in a catalog, somewhere) and rely on it to interpret raw byte rows.

GoDB does some of both, but the row format itself is closer to the first approach: each value carries its type tag, and TEXT values carry their length. That makes the bytes meaningfully self-describing — a decoder can read them back without a schema in hand. The schema gets used to *validate* the values against what the table expects (column count matches, types match, NOT NULL is honored), not to *interpret* them.

This separation is more useful than it sounds. The codec stays tiny and schema-free. The schema lives independently. Eventually the catalog will own the schema for each table, but in M2 they are decoupled units that meet at a `Validate()` call.

### Why you can't just `gob`-encode rows

If you've written Go before, you might wonder why we don't use `encoding/gob`, `encoding/json`, or some other Go-native serialization. Reasons:

- **Format stability.** `gob` is Go-specific and its on-disk format can change between Go versions. A database file written by Go 1.22 should be readable by Go 1.30, by C, by Rust, by `xxd`. Roll-your-own with explicit byte layouts is the only way to guarantee that.
- **Partial reads.** A database may want to decode only the *key* of a cell, not its full payload (we do exactly this in M3 during binary search). `gob` reads the whole record or nothing.
- **Predictable size.** A row's encoded size needs to be computable from the row, not by encoding it and measuring the result. Slotted-page bookkeeping needs to know "will this fit before I write it."
- **Schema evolution.** Adding a column should not require rewriting every row. A self-describing format lets future versions trail extra bytes the old decoder ignores.

So we write our own codec. It's small (a couple hundred lines) and entirely in our control.

### Tagged unions in Go

A relational value is "one of: NULL, INTEGER, TEXT, BOOLEAN." That's a sum type. Go doesn't have native sum types, so the idiomatic equivalent is a **tagged struct** — a struct with a discriminator field and one field per possible payload:

```go
type Value struct {
    Kind Kind     // the tag
    Int  int64
    Text string
    Bool bool
}
```

Only the field matching `Kind` is meaningful. If `Kind == KindInteger`, look at `Int`. If `Kind == KindNull`, no fields are meaningful at all. This works, but it's easy to misuse — nothing stops a caller from setting both `Int` and `Text` on the same value. Mitigation: provide constructor functions (`Null()`, `Int(v int64)`, `Text(v string)`, `Bool(v bool)`) so callers don't construct `Value{}` literals directly.

This is a recurring Go pattern. It's not as type-safe as a Rust enum, but it's clear enough.

### Fixed-width vs. variable-length integers

Once you commit to a self-describing format, every value's encoding has two parts:

- **The kind byte.** One byte. Tells the decoder which of NULL, INTEGER, TEXT, BOOLEAN comes next.
- **The payload.** Length depends on the kind.

For **NULL**, the payload is zero bytes. The kind byte alone is the encoding.

For **BOOLEAN**, the payload is one byte: 0x00 or 0x01. Three values (kind=2, plus 0 or 1) are enough.

For **INTEGER**, the payload is eight bytes — a fixed-width signed 64-bit integer in big-endian. We could have used a varint here (saves bytes for small numbers) but didn't, because fixed-width INTEGER makes row size computation cheap (no decoding to figure out a value's width). The choice is a minor space/CPU trade; we picked CPU.

For **TEXT**, the payload is a varint length followed by that many UTF-8 bytes. TEXT is the one variable-length value type in v0.1, and the varint is necessary because text can be anywhere from zero bytes to thousands.

The varint we use is LEB128 (Little-Endian Base 128), the same encoding `encoding/binary.PutUvarint` produces. See [ADR-0009](../adr/0009-leb128-uvarint.md) for the reasoning. Small numbers encode in 1 byte; the encoding is self-delimiting (each byte's high bit tells you whether more bytes follow).

### NULL vs empty TEXT

Here's a subtle point that catches every first-time database author. In SQL, `NULL` and the empty string `''` are different things. `WHERE x = ''` does not match `NULL`. `COUNT(x)` ignores `NULL` but counts `''`.

If your on-disk encoding represents both as "TEXT, length 0" — the same byte sequence — that distinction is lost forever. The executor (when it eventually exists) has no way to know which one the writer intended.

GoDB encodes them differently:

```
NULL    : [0x00]              // 1 byte: kind=NULL, no payload
TEXT "" : [0x02, 0x00]        // 2 bytes: kind=TEXT, length=0
```

NULL is one byte. Empty TEXT is two. They're distinguishable on read. There's an explicit test (`TestNullAndEmptyTextAreDistinct`) that asserts the encoded byte slices are not `bytes.Equal`. See [ADR-0008](../adr/0008-null-and-empty-text-distinct.md).

### The row format

Encoding a single value is one piece. A *row* is a sequence of values, plus enough metadata to know how many there are:

```
[row version: 1 byte]          // always 0x01 in v0.1
[column count: uvarint]         // how many values follow
[value 1] [value 2] ... [value N]
```

The row version byte is reserved for layout changes. If we ever change the row format (say, to add a NULL bitmap up front for efficiency), we bump it to 0x02 and `DecodeRow` can dispatch on version. v0.1 only accepts version 1; anything else returns `ErrUnsupportedRowVersion`.

The column count is varint-encoded — no point in spending 8 bytes when most rows have a single-digit number of columns.

### Schema as a contract

A `Schema` is the table's column list:

```go
type Column struct {
    Name       string
    Kind       Kind
    NotNull    bool
    PrimaryKey bool
    Position   int
}

type Schema struct {
    Columns []Column
}
```

It's a contract the row must satisfy *before encoding* (or after decoding, for validation). The contract is intentionally narrow in v0.1:

- Number of values must equal number of columns.
- NULL is allowed only where `NotNull == false`.
- Non-null values must have the column's declared kind. No implicit conversion. `Int(0)` for a `TEXT` column is an error.

What the schema does *not* enforce in v0.1:

- Uniqueness (beyond what the primary-key B+tree enforces by being a B+tree).
- Foreign keys, check constraints, defaults — none of these exist yet.

The point of separating schema validation from encoding is that the codec stays type-agnostic. `EncodeRow([]Value{Int(1), Text("Felipe"), Bool(true)})` works regardless of whether there's a schema in scope. The catalog/executor wraps validation around it when there is one.

## Decisions

| Decision | Why | Where |
|---|---|---|
| Four kinds in v0.1: NULL, INTEGER, TEXT, BOOLEAN | Smallest set that makes SQL meaningful; FLOAT/BLOB/TIMESTAMP defer to v0.2 | [`value.go`](../../internal/record/value.go) |
| Explicit byte values for `Kind` | On-disk constants must not reorder | [ADR-0007](../adr/0007-explicit-kind-byte-values.md) |
| INTEGER is 8-byte fixed-width big-endian | O(1) decoding, cheap size computation; varint not worth the savings | [`codec.go`](../../internal/record/codec.go) |
| TEXT length is LEB128 uvarint | Stdlib encoding, small for common case | [ADR-0009](../adr/0009-leb128-uvarint.md) |
| NULL and empty TEXT are distinct encodings | Preserves SQL semantics through the storage layer | [ADR-0008](../adr/0008-null-and-empty-text-distinct.md) |
| Row version byte at start of every encoded row | Forward compatibility for row layout changes | [`codec.go`](../../internal/record/codec.go), `rowVersion` const |
| `Schema.Validate` is separate from `EncodeRow` | Codec stays schema-free; catalog/executor own validation | [`schema.go`](../../internal/record/schema.go) |
| PK column is not stripped from encoded row | Catalog layer (M6+) has the context to decide later | [ADR-0011](../adr/0011-pk-column-stays-in-row.md) |
| `internal/record/` has no I/O | Pure data layer; M3 stores its bytes, M2 doesn't know it | [`internal/record/`](../../internal/record/) |

## The code

Five files in [`internal/record/`](../../internal/record/), plus their tests.

### [`internal/record/value.go`](../../internal/record/value.go)

Defines the value model:

```go
type Kind uint8

const (
    KindNull    Kind = 0
    KindInteger Kind = 1
    KindText    Kind = 2
    KindBoolean Kind = 3
)
```

Note: explicit values, not `iota`. See [ADR-0007](../adr/0007-explicit-kind-byte-values.md). The file's doc comments say so out loud — future-you cannot accidentally `iota` these.

The `Value` struct is the tagged union, with constructor helpers (`Null`, `Int`, `Text`, `Bool`) and an `IsNull()` shortcut for the common test. Note that `Value` is a *value type*, not a pointer — copying it is cheap, and the codec passes it by value throughout.

### [`internal/record/codec.go`](../../internal/record/codec.go)

The encoder/decoder for both single values and full rows. The four interesting functions:

- `EncodeValue(dst []byte, v Value) ([]byte, error)` — append-style. Takes a destination slice and a value, returns the extended slice. Append-style avoids allocating intermediate buffers when encoding many values into the same row buffer.
- `DecodeValue(src []byte) (Value, int, error)` — reads one value from the front of `src`, returns the value and the number of bytes consumed. The bytes-consumed return lets the row decoder advance through a packed payload.
- `EncodeRow(values []Value) ([]byte, error)` — preallocates a buffer with a reasonable initial capacity, writes the row version byte, writes the column count as a uvarint, then encodes each value in turn. Returns the finished byte slice.
- `DecodeRow(src []byte) ([]Value, int, error)` — reverse of `EncodeRow`. Verifies the row version byte, reads the column count, decodes each value, returns the values and the bytes consumed. Callers that pass exact-fit buffers (e.g. a cell payload) can treat `n < len(src)` as a corruption signal.

The error paths use `fmt.Errorf("%w: ...", sentinelErr, ...)` so callers can `errors.Is` against the sentinel while still getting a contextual error string.

There's nothing clever in this file. It's careful, type-by-type, with a single test that's worth its weight: `TestNullAndEmptyTextAreDistinct`, which proves we haven't accidentally merged the two encodings.

### [`internal/record/schema.go`](../../internal/record/schema.go)

The schema model and its `Validate` method. Three checks, in order: column count, then per-column null-violation, then per-column type-mismatch. Returns wrapped sentinel errors so a caller can `errors.Is(err, ErrTypeMismatch)` without parsing strings.

There is no `Schema.Encode` or `Schema.Decode` in M2. The catalog (M6) will own schema persistence. M2's schema is in-memory only, used by tests and (later) by the executor.

### [`internal/record/errors.go`](../../internal/record/errors.go)

The sentinel errors the package returns: `ErrShortBuffer`, `ErrTrailingBytes`, `ErrInvalidKind`, `ErrInvalidUTF8`, `ErrInvalidBool`, `ErrUnsupportedRowVersion`, `ErrColumnCountMismatch`, `ErrNullViolation`, `ErrTypeMismatch`. Each has a doc comment explaining when it's returned.

### [`internal/record/codec_test.go`](../../internal/record/codec_test.go) and [`schema_test.go`](../../internal/record/schema_test.go)

Together, 19 tests. The codec tests cover round trips for every kind, NULL vs empty TEXT distinction, multi-byte UTF-8 (including 日本語 and emoji), integer boundaries (`math.MinInt64`, `math.MaxInt64`), short-buffer rejection across all kinds, invalid-kind/invalid-bool/invalid-utf8 rejection, row round trip (including the empty row), and unsupported row version rejection.

The schema tests cover ok, column-count mismatch, null violation, type mismatch, allow-null-in-nullable-column, and accept-boolean-false (to make sure `Bool(false)` is treated as a present value, not as missing).

If you want to understand what M2 actually guarantees, read the tests. They're the most precise documentation in the package.

## What this layer cannot do yet

- **No I/O.** M2 produces and consumes byte slices in memory. M3 stores those bytes in pages on disk.
- **No multi-row container.** A row is encoded standalone; there's no notion of "many rows together" until M3 introduces slotted pages.
- **No additional value kinds.** FLOAT, BLOB, TIMESTAMP, DATE — all v0.2 or later.
- **No schema persistence.** Schemas live in memory. The catalog (M6) will own table metadata on disk.
- **No PK stripping.** A row with an `INTEGER PRIMARY KEY` column encodes that column twice — once in the cell key (M3), once in the row payload. Future optimization at the catalog layer ([ADR-0011](../adr/0011-pk-column-stays-in-row.md)).
- **No collation, no comparators.** `WHERE name = 'Felipe'` semantics will need string comparison; M2 only encodes/decodes.

## Further reading

- The SQLite [Record Format](https://www.sqlite.org/fileformat.html#record_format) section. SQLite's row format is more sophisticated than GoDB's (compact varint type codes that encode both type and size in one number), but the underlying problems are the same.
- `encoding/binary` in the Go stdlib — particularly `PutUvarint`/`Uvarint`. GoDB uses these directly; understanding them takes 10 minutes and is worth it.
- "[Designing Data-Intensive Applications](https://dataintensive.net/)", chapter 4 ("Encoding and Evolution"). Has the clearest explanation of schema evolution and self-describing formats in print.

## Where the next chapter picks up

Chapter 05 takes the M2 byte slices and starts packing them. A 4 KB page can hold many rows — sometimes dozens, sometimes thousands — but only if you have a structure that supports variable-length cells with sorted-key lookup. That structure is the **slotted page**, and it is the data structure the B+tree (M4+) is built on.

You will leave Chapter 05 able to: allocate a fresh page, initialize it as a slotted leaf, insert M2-encoded rows as cells with rowid keys, look them up by key in O(log n), iterate them in key order, close the file, reopen it, and read everything back. That's the first time GoDB does something that genuinely resembles a database.

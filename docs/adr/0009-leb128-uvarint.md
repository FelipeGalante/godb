# ADR-0009: LEB128 uvarint via `encoding/binary` (not SQLite varint)

- Status: Accepted
- Date: 2026-05-30
- Tags: encoding, on-disk

## Context

GoDB needs a way to encode integers of arbitrary magnitude using as few bytes as possible. These show up in three places:

- Cell keys (rowids) in slotted leaf pages.
- Cell payload lengths.
- Row column counts.

The numbers are usually small (column counts of 4–10; payload lengths of a few hundred bytes; rowids in the thousands or low millions for most rows in most rows). A fixed 8-byte encoding wastes 6–7 bytes per value most of the time.

There are two well-known variable-length integer encodings:

- **LEB128 uvarint** (Little-Endian Base 128): each byte holds 7 bits of payload and 1 continuation bit; bytes are little-endian. This is what protobuf and Go's `encoding/binary.PutUvarint` / `Uvarint` use.
- **SQLite varint** (also sometimes called Big-Endian Base 128): up to 9 bytes; first 8 bytes carry 7 payload bits each, the 9th (if present) carries 8. Bytes are big-endian. SQLite uses this; some other engines borrow it.

LEB128 has a Go standard library implementation, well-tested and benchmarked. SQLite varint does not have a stdlib implementation and would have to be hand-written. Both encode small numbers in 1 byte and grow at similar rates.

## Decision

GoDB uses **LEB128 uvarint** via `encoding/binary.PutUvarint` / `Uvarint`. The signed variant (`PutVarint` / `Varint`, with zig-zag encoding) is not currently needed — keys and lengths are always non-negative.

## Consequences

**Enables.** Zero new code to maintain for varint logic. Battle-tested encoder/decoder used by the entire Go ecosystem. Easy to reason about (`uint64` in, byte slice out; byte slice in, `uint64` + bytes-consumed out).

The chosen encoding for variable-length integers is endianness-agnostic by construction, so it sits cleanly alongside the big-endian fixed-width encoding ([ADR-0002](0002-big-endian-on-disk.md)) without contradiction.

**Constrains.** GoDB databases cannot be opened by SQLite, even hypothetically — varint formats differ. This is fine because we already have [ADR-0004](0004-no-sqlite-compatibility.md). For very large numbers (near `uint64` max), LEB128 uses 10 bytes vs SQLite varint's 9 — a 1-byte difference at the extreme. Irrelevant in practice.

**Reversibility.** Changing varint encoding requires a file format major version bump. Not happening.

## Alternatives considered

**SQLite varint.** Slightly more compact at the very-large end. Rejected: requires custom implementation, no test corpus to borrow, no benefit in practice. SQLite compatibility is already not a goal.

**Fixed 8-byte big-endian everywhere.** Simpler than varint but wastes bytes for every small value (which is nearly all of them). Rejected: a typical row's cell key (rowid 1–10000) goes from 8 bytes to 1–2 bytes with varint; the savings add up.

**Protobuf or capnproto encoding for cells.** Overkill for fixed-shape leaf cells. Rejected: too much machinery for a single binary cell layout. We don't need self-describing schemas at the storage layer.

## Related

- Code: [internal/btree/cell.go](../../internal/btree/cell.go), [internal/record/codec.go](../../internal/record/codec.go)
- Book: [Chapter 04 — Records](../book/04-milestone-2-records.md), [Chapter 05 — Slotted Pages](../book/05-milestone-3-slotted-pages.md)
- See also: ADR-0004 (no SQLite compatibility) — frees us from matching SQLite varint.
- Go stdlib: [`encoding/binary` Uvarint docs](https://pkg.go.dev/encoding/binary#Uvarint)

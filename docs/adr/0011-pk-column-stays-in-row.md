# ADR-0011: Primary-key column stays in the row payload

- Status: Accepted
- Date: 2026-05-30
- Tags: record, catalog, optimization

## Context

When a table has an `INTEGER PRIMARY KEY` column, that column's value is used in two places:

1. As the **cell key** in the table's B+tree leaf (the "rowid").
2. As one of the values in the **row payload** alongside the other columns.

These two uses can store the same number twice — once as the varint key at the start of the cell, once as a fixed-width INTEGER inside the encoded row body. For a row with a small rowid (say, 1–10000) and few columns, that's ~9 bytes (1 byte kind + 8 bytes INTEGER) repeated, out of maybe 40-100 total bytes per row.

The optimization is obvious: strip the primary-key column from the encoded row payload, since the cell key already carries it. On read, the catalog re-inserts the rowid into the values array at the right position. This is what SQLite does.

But the optimization requires the encoder/decoder to know:

- Whether the table has an `INTEGER PRIMARY KEY` at all.
- Which column position it occupies.
- How to skip that position on encode and re-insert it on decode.

The `internal/record` package, at the layer it lives at in v0.1, has no view of "this is the table being inserted into" or "this is the schema." It encodes a `[]record.Value` and decodes a `[]byte` — it doesn't know which value, if any, is the rowid.

## Decision

**M2 (`internal/record`) encodes all values, including any that happen to be primary keys, into the row payload.** The decision of whether and how to strip a column from the payload is **deferred to the catalog/executor layer** (M6+), which has the schema context required to do it correctly.

In v0.1 this means a small amount of byte waste per row. Later versions can introduce the optimization in `internal/catalog` or `internal/exec` without changing `internal/record`'s API.

## Consequences

**Enables.** `internal/record` stays a pure data layer with no schema awareness — easy to test, easy to reason about, no implicit "do you have a rowid?" branching inside encode/decode. M6 (catalog) is the natural home for a strip-and-restore wrapper because the catalog has the schema.

**Constrains.** A few bytes wasted per row in v0.1 — typically 9 bytes (INTEGER kind + 8-byte payload) per row that has an INTEGER PRIMARY KEY. For 1000 rows, ~9 KB total. Irrelevant for v0.1; not irrelevant at scale.

**Reversibility.** Easy. When M6 or later adds catalog-aware encoding, the change is additive: a new function in `internal/catalog` or `internal/exec` that wraps `record.EncodeRow` / `DecodeRow` with PK stripping. Old `.godb` files that encoded full rows continue to work because we'd version that logic at the catalog level, not the record level.

## Alternatives considered

**Strip in `internal/record` by passing the schema in.** `EncodeRow(values, schema)`. Rejected: drags schema awareness into the lowest layer, which makes that layer harder to use for any non-table-row use case (e.g. catalog metadata, future index entries). Also forces the schema to be available at every encode site.

**Strip in `internal/record` by convention (always strip position 0 if it's an INTEGER).** Tempting because it's stateless. Rejected: brittle. A table can declare `INTEGER PRIMARY KEY` at any column position, or not at all, or have a non-integer primary key (in v0.2+).

**Don't strip at all, ever.** What v0.1 effectively does. Reconsidered when v0.2/v0.3 bring real data sizes into play. Rejected as a permanent decision because at scale the waste is real — but accepted as v0.1's behavior.

## Related

- Code: [internal/record/codec.go](../../internal/record/codec.go), [internal/record/schema.go](../../internal/record/schema.go)
- Book: [Chapter 04 — Records: Variable-Length Typed Values](../book/04-milestone-2-records.md)
- See also: ADR-0010 (slotted page layout) — the cell key already stores the rowid as a varint at the start of the cell.

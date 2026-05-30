# ADR-0014: Catalog object encoding uses a custom binary format

- Status: Accepted
- Date: 2026-05-30
- Tags: catalog, encoding, on-disk

## Context

The catalog (M6) needs to serialize one row per registered table: object type, name, root page id, original `CREATE` SQL, and a column list. The column list is structured — each column has a name, a `record.Kind`, NOT NULL / PRIMARY KEY flags, and a position — and the number of columns varies per row.

There were two realistic paths for serializing this:

1. **Reuse `record.EncodeRow`.** The catalog row becomes a flat record like `(type INTEGER, name TEXT, root INTEGER, sql TEXT, columns_blob TEXT)`. The variable-length column list gets hand-serialized into the `columns_blob` `TEXT` field.
2. **Define a small custom binary format** scoped to the catalog package.

Path (1) is appealing because it reuses an existing layer. But it has two real drawbacks: it requires stuffing structured data into a `TEXT` field (treating valid UTF-8 like a BLOB), and the schema describing the "catalog row schema" itself is a piece of constant lore that has to live somewhere. Both are mild forms of impedance mismatch — the record codec is genuinely designed for flat rows of typed scalars, not for nested lists.

Path (2) is more lines of code but every line is doing exactly the thing it looks like.

There's also a third concern this decision has to address: GoDB's `Header.CatalogRootPageID` field has been dual-purpose since M4 (used as a stash for the application's single primary-tree root id pre-catalog). When M6's `catalog.Open` reads a non-zero value, it could be pointing at a real catalog tree (post-M6) *or* a regular table leaf (pre-M6). The catalog codec needs a way to reject the second case cleanly, not crash on it.

A single version byte is not enough on its own: `record.EncodeRow` writes a row version byte at the start of every encoded row, and it happens to use the value `1` — exactly what a single-byte catalog version would also use. We need a more distinctive prefix.

## Decision

The catalog defines its own row encoding in [`internal/catalog/codec.go`](../../internal/catalog/codec.go). The format starts with a **two-byte prefix**:

- `0xCA` — catalog magic byte. Chosen specifically because it's a high-bit-set byte unlikely to appear as the leading byte of any other format the database holds (in particular, distinct from `record`'s row-version byte `0x01`).
- `0x01` — catalog format version. Bumped only on incompatible on-disk format changes.

Then the object type byte and the named fields in order. The two-byte prefix serves three jobs simultaneously:

- **Magic fences against accidentally walking a pre-M6 `.godb` file** whose `CatalogRootPageID` pointed at a regular table leaf. The leaf's first cell payload is a `record`-encoded row whose first byte is `0x01` — which does not match the catalog magic `0xCA`, so `DecodeObject` returns `ErrUnsupportedCatalogVersion` immediately. Without the magic byte this case would slip past a single version check and fail downstream with a confusing truncation error.
- **Version byte is a future format-evolution hook** (bump to `2` if/when the layout changes).
- **Both bytes are checked at decode**; mismatch on either returns `ErrUnsupportedCatalogVersion`.

The object id (the catalog btree's cell key) is **not** encoded in the payload — keeping it solely in the cell key saves a few bytes per object and removes a "key and payload id disagree" failure mode.

## Consequences

**Enables.** The catalog format evolves independently of the row format. Pre-M6 `.godb` files fail cleanly at the first catalog row decode (not silently misinterpret arbitrary bytes). The codec is small (~180 lines) and self-contained: it has its own tests, its own typed errors, and no entanglement with `internal/record`'s decoder.

**Constrains.** Two encoders now exist in the codebase (`record` and `catalog`). Future format-level work has to touch both when something cross-cutting changes (e.g. moving from LEB128 to a different varint encoding would have to land in both, since they both use LEB128). Mitigated by both formats being small.

**Reversibility.** Easy in principle (we could rewrite the catalog onto `record.EncodeRow` in a later milestone), but expensive in practice: any `.godb` file written before such a rewrite would need migration. We treat the choice as permanent within v0.1.

## Alternatives considered

**Path (1): `record.EncodeRow` with a `TEXT` blob for columns.** Workable. Rejected because (a) it forces a "valid UTF-8 but really a binary blob" convention into TEXT, which is a small but real footgun for anyone inspecting rows with `xxd`; (b) the column-list serializer would still have to be its own format, just buried inside a column rather than declared at the top level; (c) the "catalog row schema" itself becomes a piece of code constants that has to live somewhere reasonable.

**Add a `BLOB` kind to `record` early.** Would make path (1) cleaner. Rejected because adding `KindBlob` pulls v0.2 work forward (see [PRD §4](../prd.md), spec §7.1 lists BLOB as v0.2) and the catalog can be solved fully without it.

**Self-describing catalog format (e.g. a small schema descriptor in the database header).** Overkill for v0.1. The catalog format is fixed in code by the version byte; we'd revisit if the format grew to support, say, user-defined types.

## Related

- Code: [`internal/catalog/codec.go`](../../internal/catalog/codec.go), [`internal/catalog/errors.go`](../../internal/catalog/errors.go).
- Book: [Chapter 08 — The Catalog (M6)](../book/08-milestone-6-catalog.md).
- See also: [ADR-0007 (explicit `Kind` byte values)](0007-explicit-kind-byte-values.md) — the column-kind byte the catalog codec writes is the same value the record codec writes; reordering one would silently corrupt the other.
- See also: [ADR-0009 (LEB128 uvarint)](0009-leb128-uvarint.md) — the catalog format reuses LEB128 for all length prefixes.

# ADR-0002: Big-endian for all on-disk multi-byte integers

- Status: Accepted
- Date: 2026-05-30
- Tags: encoding, on-disk, debuggability

## Context

Multi-byte integers (page counts, page IDs, lengths, transaction IDs) are written to disk in every layer of the engine. Endianness has to be picked once and applied consistently — switching mid-format means readers and writers disagree.

The two real choices are big-endian (network byte order) and little-endian (native on every platform GoDB targets). LEB128 varints sit alongside this decision: they are byte-order-independent by construction, so they aren't subject to the same trade-off.

## Decision

All multi-byte numeric fields written to disk by GoDB use **big-endian byte order**, encoded with `encoding/binary.BigEndian`. This applies to:

- The database header (page size, page count, root page IDs, transaction IDs, checksums, flags).
- The slotted-page header (cell count, free space offset, sibling/parent page IDs, checksum).
- Fixed-width record values — specifically `INTEGER`, which is stored as 8 signed big-endian bytes after its kind byte.
- Cell-directory entries (u16 offsets into the page body).

Variable-length integers (cell keys, payload lengths, row column counts) use LEB128 — see [ADR-0009](0009-leb128-uvarint.md) — which is endianness-agnostic.

## Consequences

**Enables.** Hex dumps read in the obvious order: `00 00 10 00` is 4096, the way it would be written on a whiteboard. Tools like `xxd`, `od`, and `hexyl` make `inspect` output (M10) immediately interpretable without "byte-swap this in your head" mental arithmetic. Big-endian is the historical "network byte order" and matches every IP-network header, every BinaryHeap-from-bytes implementation, and SQLite's own choice. Future contributors and readers of the book will not be surprised.

**Constrains.** A microscopic per-call cost on little-endian hardware (all of GoDB's target platforms): each read/write does a byte-swap. This is irrelevant against the cost of the actual I/O.

**Reversibility.** Changing endianness would require a new file-format major version (per ADR-0001). Existing `.godb` files would not be readable. So this decision is effectively permanent within a format version.

## Alternatives considered

**Little-endian.** Matches CPU native order on x86, ARM, and Apple Silicon, removing the per-call swap. Rejected: the swap is cheaper than the I/O it accompanies by orders of magnitude, and hex dumps in LE are harder to read out loud. The aesthetic and pedagogical cost is not worth a benchmark difference no profile would ever show.

**Native (machine-dependent) byte order.** Some embedded databases (LMDB, BoltDB) write in the machine's native byte order, declaring that databases are not portable across endianness. Rejected: not portable across architectures means a `.godb` created on one machine can't necessarily be opened on another. That's a footgun for a tool meant to be shared.

**Sticking entirely with LEB128 even for fixed-width fields.** Would remove endianness from the discussion altogether. Rejected: fixed-width INTEGER values benefit from a fixed encoded size for layout calculation (cellSize) and from O(1) decoding. Mixing varint INTEGERs into row payloads complicates both.

## Related

- Code: [internal/storage/header.go](../../internal/storage/header.go), [internal/btree/page.go](../../internal/btree/page.go), [internal/record/codec.go](../../internal/record/codec.go)
- See also: ADR-0009 (LEB128 uvarint) — variable-length integers handled separately, endianness-agnostic.
- Book: [Chapter 03 — Pages, Files, and Durability](../book/03-milestone-1-pager.md)

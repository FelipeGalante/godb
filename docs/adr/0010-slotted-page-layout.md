# ADR-0010: Slotted page with sorted cell directory + payloads from page end

- Status: Accepted
- Date: 2026-05-30
- Tags: storage, layout, btree

## Context

A fixed-size page (4 KB, [ADR-0001](0001-single-file-fixed-pages.md)) has to hold many variable-length cells, each addressable by a sort key, with fast lookup (O(log n) within the page), reasonable space utilization, and a clear "page full" signal.

The naive layout — pack cells end to end from the start of the page — has three immediate problems:

- **Lookup is O(n)**: with no index, finding a cell by key requires scanning.
- **Insert in sorted order is O(n)**: existing cells have to shift to make room.
- **Deletes leave fragmentation**: holes inside the packed region force scanning over dead bytes.

The standard answer (used by SQLite, Postgres, MySQL InnoDB, and most page-based engines) is the **slotted page**: a fixed header at the top, a cell directory of small offsets growing forward, cell payloads growing backward from the page end, and free space in the middle.

The reason for "directory at top, cells at bottom" is the shape it gives free space — a single contiguous region between them. Inserting one cell takes one slot at the bottom of the directory and one cell at the top of the payload region; the free region in the middle shrinks from both sides.

## Decision

GoDB uses a slotted-page layout for table-leaf pages:

```
+---------------------------------------+
| Page header (28 bytes, offsets 0..27) |
+---------------------------------------+
| Cell directory: N × uint16 BE         |   ← grows forward from offset 28
|   each slot = offset of a cell        |
|   slots sorted by cell key (ascending)|
+---------------------------------------+
| Free space                            |
+---------------------------------------+
| Cell payloads, packed from the end    |   ← grows backward from PageSize
+---------------------------------------+
```

Key invariants (enforced by `btree.Validate`):

- `HeaderSize ≤ CellDirEnd ≤ FreeSpaceOffset ≤ PageSize`
- `CellDirEnd − HeaderSize == 2 × CellCount` (slot size is 2 bytes)
- Every directory entry points into the payload region
- Keys are strictly ascending across the directory (no duplicates)

Lookup is a binary search of the directory; each probe decodes only the cell's varint key, not its payload.

Insert is:

1. Binary-search for the insert position. Reject duplicate.
2. Check `cellSize + 2 ≤ FreeBytes`. Otherwise `ErrPageFull`.
3. Write the cell at `FreeSpaceOffset − cellSize`.
4. Shift directory entries from the insert position right by 2 bytes.
5. Update header (`CellCount++`, `FreeSpaceOffset`, `CellDirEnd`).

`ErrPageFull` leaves the page byte-identical to before the call — this is the explicit contract the B+tree split logic (M4/M5) will build on.

## Consequences

**Enables.** O(log n) lookup within a page. O(log n + n) insert (binary search + directory shift). Clean "page full" boundary for the B+tree split decision. Page is self-describing — `Validate` can prove it's well-formed without external context. Cell payloads can be variable-length without breaking the directory.

**Constrains.** Two-byte slot offsets cap the page size at 64 KB (any cell offset has to fit in a `uint16`). At 4 KB pages, this is irrelevant; if we ever want >64 KB pages we'd grow slots to 4 bytes (a format change). Deletion (when M? lands) will leave gaps unless we add in-page compaction; the design accepts that and defers the choice.

The layout is committed to disk in a specific byte sequence (header layout, slot endianness, cell encoding). Changing any of it requires a file format major version bump.

**Reversibility.** The cell directory format and the slotted layout are baked into every B+tree leaf forever once data exists. The header layout has a few reserved bytes that absorb modest extensions without bumping the major version (flags, checksum field), but the structural choice — directory at top, payloads at bottom — is permanent.

## Alternatives considered

**Linear packed layout (no directory).** Cells written end to end from offset `HeaderSize`. Rejected: O(n) lookup, hard to insert in sorted order, hard to delete cleanly. Only suitable for append-only logs.

**Directory of full key copies (not offsets).** Each directory entry stores the key plus an offset. Faster binary search (no varint decode per probe). Rejected: doubles directory size for short payloads; for typical small rowids the offset-only encoding is plenty fast. Could be reconsidered for read-heavy workloads later.

**Hash table per page.** O(1) lookup. Rejected: a hash on a single page is rarely worth it (the directory is so small a binary search is in L1 cache), and we'd lose ordered iteration — which the B+tree depends on for scans and splits.

**B-tree of cells within each page.** Some engines do nested B-trees. Rejected: enormously over-engineered for 4 KB pages with at most a few hundred cells.

## Related

- Code: [internal/btree/page.go](../../internal/btree/page.go), [internal/btree/leaf.go](../../internal/btree/leaf.go), [internal/btree/cell.go](../../internal/btree/cell.go)
- Book: [Chapter 05 — Slotted Pages](../book/05-milestone-3-slotted-pages.md)
- See also: ADR-0009 (LEB128 uvarint) — keys and payload lengths use uvarints inside the cell.
- Spec: §10 (slotted page layout).
- External: SQLite "Cell Storage" docs, [https://www.sqlite.org/fileformat.html](https://www.sqlite.org/fileformat.html) — same shape, different specifics.

# Chapter 05 — Slotted Pages: Many Records on One Page (M3)

## Where we are

By the end of [Chapter 04](04-milestone-2-records.md) we can encode a typed row into a byte slice and decode it back. By the end of [Chapter 03](03-milestone-1-pager.md) we can write any 4 KB byte slice into a page and read it back across a restart. What we *cannot* do yet is put more than one row in a page.

Today a page is opaque to us — 4 KB of bytes we treat as a single unit. To make a real database we need a structure that packs many variable-length rows into a single page, keeps them findable by sort key, leaves room for new rows, and tells us when it's full. That structure is the **slotted page**. It's the data structure inside every B+tree node in every page-based database we've ever heard of, and it's what M3 builds.

By the end of M3 you can: allocate a fresh page via the M1 pager, initialize it as a slotted leaf, insert M2-encoded rows as cells (with rowid keys) in sorted-key order, look them up by key in O(log n), iterate in key order, close, reopen, and read everything back. That's the first time the engine demonstrates end-to-end durability for typed rows.

## Foundation

### The variable-length packing problem

You have a 4 KB page (4096 bytes). You want to fill it with variable-length cells (let's say, an encoded row each), keyed by an `INTEGER PRIMARY KEY` rowid. Three things you need from this packing:

1. **Sorted lookup.** Given a rowid, find the corresponding row in O(log n) — without scanning every cell.
2. **Sorted insert.** A new cell goes in the right spot to maintain key order.
3. **A "full" signal.** When the next insert won't fit, return a specific error so the caller (a B+tree split, eventually) can react.

The naive layout — write cells end to end starting at offset 28 — is fast to write but has all three problems. Lookup is O(n): no index, so you scan cells comparing keys one at a time. Sorted insert is O(n) for both the search and the shift to make room. There's no clean way to declare "full" because the bytes might fit but the directory has nowhere to live (because there is no directory).

The standard answer — used by SQLite, Postgres, MySQL InnoDB, and roughly every B-tree-based engine — is the **slotted page layout**.

### The slotted-page idea

Imagine the page as two regions that grow toward each other:

```
+---------------------------------------+  offset 0
| Page header (28 bytes)                |
+---------------------------------------+  offset 28
| Cell directory: N × uint16 BE         |  ← grows forward
|   each slot = offset of a cell        |
|   slots sorted by cell key            |
+---------------------------------------+
| Free space                            |
+---------------------------------------+
| Cell payloads, packed from the end    |  ← grows backward
|   actual cell bytes live here         |
+---------------------------------------+  offset PageSize (4096)
```

The header is fixed-size (28 bytes for GoDB). The cell directory grows forward from offset 28 — each entry is a 2-byte offset pointing to the start of a cell. The cell payloads grow backward from the end of the page. Free space is whatever's left in the middle.

A few non-obvious consequences:

- **Inserting a new cell touches both regions.** A 2-byte slot is added to the directory; a variable-length cell is added to the payload region. Both regions shrink the free space in the middle.
- **The directory is sorted by cell key, not by insertion order.** The cell payloads, on the other hand, are in arbitrary physical order — they got written wherever they fit at the time. The "logical" order is the directory's order; the "physical" order doesn't matter.
- **A directory slot is just an offset.** It's a pointer into the same page. The cell itself encodes the key, the length, and the payload — but you don't have to decode the whole cell to find it. You decode the *key* (which is at the start of the cell) and that's it.

This gives you O(log n) lookup via binary search over the directory: a probe means "read the slot at index `mid`, dereference it to the cell, decode the cell's key prefix, compare to the search key, halve the range." For 100 cells, that's ~7 probes. Each probe is two memory accesses (slot, then cell key). It's extremely fast.

### The "page full" contract

When `InsertCell` returns `ErrPageFull`, the page is *unchanged* from before the call. This is a non-obvious contract that matters enormously to the B+tree code that will sit on top in M4/M5.

Why? Because the B+tree's split algorithm goes:

```
InsertCell(leaf, key, payload)
if err == ErrPageFull:
    split_leaf_into_two(leaf, key, payload)
```

If `InsertCell` left the page half-modified on failure, splitting would have to figure out which writes happened and which didn't. That's error-prone. The slotted-page layer's contract says: either the insert succeeded and the page is updated, or it didn't and the page is byte-identical to before. The B+tree gets to assume "page-full means try a different strategy" without bookkeeping.

This is the recurring pattern of clear-failure semantics in storage engines. Every layer that mutates state should be explicit about whether failure is *atomic* (no observable change) or *non-atomic* (some writes happened). Slotted-page mutations are atomic by design.

### Free-space accounting

Two integers in the page header — `FreeSpaceOffset` and `CellDirEnd` — bracket the free region. `FreeBytes` is `FreeSpaceOffset - CellDirEnd`. A new insert needs `cellSize(key, payload) + 2` (cell bytes plus a directory slot) ≤ `FreeBytes`.

The two integers also encode where new things go: a new cell goes at `FreeSpaceOffset - cellSize` (then `FreeSpaceOffset` decreases); a new directory slot goes at `CellDirEnd` (then `CellDirEnd` increases). Insert decrements free space from both sides.

### Cell format

A leaf cell in GoDB is:

```
[key: uvarint][payload length: uvarint][payload bytes]
```

The key is a `uint64` rowid encoded as a LEB128 uvarint (so small rowids encode in 1 byte). The payload length is a uvarint. The payload is the M2-encoded row.

Helpers:

- `cellSize(key, payloadLen)` — return the encoded byte length without actually writing the cell. Insert uses this for free-space checks.
- `writeCell(buf, key, payload)` — write the cell at the start of `buf`, return bytes written.
- `readCell(buf)` — decode a full cell, return key, payload (aliases buf), and bytes consumed.
- `readCellKey(buf)` — decode *only* the key. This is what binary search uses on each probe; decoding the full payload on every probe would be wasteful.

### What about internal pages?

A multi-page B+tree has two kinds of pages: leaves (which hold cells) and internal nodes (which hold keys + child pointers). Their cell formats differ — internal cells are `[child_page_id: u64][separator_key: uvarint]` with a special rightmost-child pointer.

M3 handles only **leaf-format cells**. Internal-page cell handling is M5's job, when the multi-page tree arrives. The `internal/btree/` package will grow a separate `internal.go` for those operations. M3's `leaf.go` is correctly leaf-specific; calling its functions on a page with the wrong type byte returns `ErrNotLeaf`.

## Decisions

| Decision | Why | Where |
|---|---|---|
| Slotted layout (directory forward, payloads backward) | Standard; gives O(log n) lookup, atomic insert, clean "full" boundary | [ADR-0010](../adr/0010-slotted-page-layout.md) |
| 28-byte page header | Enough for type, flags, counts, sibling/parent pointers, checksum slot | [`page.go`](../../internal/btree/page.go) |
| Cell key = uvarint rowid | Small rowids encode in 1 byte; matches M2's varint choice | [`cell.go`](../../internal/btree/cell.go), [ADR-0009](../adr/0009-leb128-uvarint.md) |
| Directory slots are 2-byte BE offsets | 64 KB max page size is fine for v0.1 (we use 4 KB) | [`leaf.go`](../../internal/btree/leaf.go), `slotSize` |
| Cells stored in sorted key order via binary search insertion | O(log n) lookup, O(log n + n) insert | [`leaf.go`](../../internal/btree/leaf.go), `search` + `InsertCell` |
| `ErrPageFull` leaves page byte-identical | The B+tree split logic depends on this | [`leaf.go`](../../internal/btree/leaf.go), `InsertCell` |
| `ErrDuplicateKey` likewise leaves page intact | Same contract reasoning | [`leaf.go`](../../internal/btree/leaf.go) |
| `Validate()` enforces all invariants | Property-style tests can check after every random insert | [`leaf.go`](../../internal/btree/leaf.go), `Validate` |
| `GetCell` returns a copy of the payload | Safe to retain after the page is reused; `Iterate` is the no-copy path | [`leaf.go`](../../internal/btree/leaf.go) |

## The code

Five files in [`internal/btree/`](../../internal/btree/).

### [`internal/btree/page.go`](../../internal/btree/page.go)

The 28-byte page header. Defines `HeaderSize = 28`, the `PageHeader` struct, and `ReadHeader` / `WriteHeader` to round-trip it through a `*storage.Page`'s byte array.

The header fields:

- `Type` — copied from byte 0 (which the M1 pager already wrote at allocation).
- `Flags` — reserved; always 0 in v0.1.
- `CellCount` — number of cells in the page.
- `FreeSpaceOffset` — first byte of the free region (also = end of free region, since cells grow backward).
- `CellDirEnd` — one past the last directory entry; grows forward.
- `RightSibling` — page ID of the next leaf in scan order (used by M4/M5 for full-table scans).
- `Parent` — page ID of the parent in the B+tree (debug-only; never trusted, because keeping it accurate during splits is brittle).
- `Checksum` — reserved; always 0 in v0.1.

All multi-byte fields are big-endian, per [ADR-0002](../adr/0002-big-endian-on-disk.md). The doc comment in `page.go` has the exact layout; that comment is the single source of truth for the on-disk header.

### [`internal/btree/cell.go`](../../internal/btree/cell.go)

The cell codec. Read it as a unit; it's ~70 lines and unambiguous.

- `cellSize(key, payloadLen)` uses an in-package `uvarintSize` helper to compute the byte width without actually encoding. The whole point is to do free-space math before writing.
- `writeCell(buf, key, payload)` encodes the cell directly into `buf`, returns bytes written.
- `readCell(buf)` returns the key, a payload slice that aliases `buf` (no copy), and bytes consumed.
- `readCellKey(buf)` returns only the key. This is what `search` uses on every probe — decoding the payload length and skipping over the payload would be wasted work just to compare keys.

The aliasing in `readCell` is deliberate: iteration doesn't need to copy. The wrapping `IterateCells` function documents that the payload slice is valid only for the duration of the callback. Callers that want to retain it copy it themselves; `GetCell` always returns a copy.

### [`internal/btree/leaf.go`](../../internal/btree/leaf.go)

The main file. The operations:

- `InitLeaf(pg)` — initialize a freshly-allocated page as an empty slotted leaf. Zeros the body past byte 0 (the type tag the M1 pager set), then writes a header with `CellCount = 0`, `FreeSpaceOffset = PageSize`, `CellDirEnd = HeaderSize`. Errors if the page's type byte is not `PageTypeTableLeaf` or `PageTypeIndexLeaf` (we reserved index leaves in the M1 page-type enum but don't use them yet).
- `requireLeaf(pg)` — internal helper that reads the header and verifies the page type. Called at the top of every public operation so the caller always gets a clear `ErrNotLeaf` instead of an obscure corruption error.
- `slotOffset(i)`, `readSlot(pg, i)`, `writeSlot(pg, i, off)` — convenience for the cell-directory indexing math. The directory starts at offset 28 and each slot is 2 bytes, so slot `i` lives at `28 + i*2`.
- `search(pg, h, key)` — binary search over the directory. Each probe reads the slot, dereferences to the cell offset, decodes the cell's key prefix, and compares. Returns the directory index (where `key` is, or where it would be inserted) and a `found` bool. On a corruption — slot pointing outside the page, key prefix not decodable — returns a `*storage.CorruptionError`.
- `InsertCell(pg, key, payload)` — the heart of the package. Sequence:
  1. Validate the page is a leaf.
  2. Reject cells that are too large to ever fit in an empty page (`ErrCellTooLarge`).
  3. Binary-search for the insert position. If found, return `ErrDuplicateKey` (page unchanged).
  4. Check free space. If insufficient, return `ErrPageFull` (page unchanged).
  5. Write the cell bytes at `FreeSpaceOffset - cellSize`.
  6. Shift directory entries `[idx, CellCount)` right by 2 bytes to make room for the new slot.
  7. Write the new slot at `idx`.
  8. Update header (`CellCount++`, `FreeSpaceOffset -= cellSize`, `CellDirEnd += 2`). Mark page dirty.
- `GetCell(pg, key)` — binary-search; if found, return a copy of the payload. The copy keeps the contract simple: callers don't have to think about "is this slice still valid after the next mutation?"
- `IterateCells(pg, fn)` — walk slots in order, decode each cell, call `fn(key, payload)`. Payload aliases the page; the callback contract says don't retain.
- `FreeBytes(pg)`, `CellCount(pg)` — accessors.
- `Validate(pg)` — read-only invariant check. Verifies the header bounds, the directory size matches `CellCount`, every slot points into the payload region, every cell is well-formed, and the keys are strictly ascending. Returns a `*storage.CorruptionError` on the first violation. This is the function the M3 test suite calls after every random insert — see "Tests as proof" below.

### [`internal/btree/errors.go`](../../internal/btree/errors.go)

Sentinel errors: `ErrPageFull`, `ErrDuplicateKey`, `ErrCellTooLarge`, `ErrNotLeaf`. Each has a doc comment that names the precondition that triggers it.

### [`internal/btree/leaf_test.go`](../../internal/btree/leaf_test.go)

12 tests. The two worth highlighting:

- **`TestValidateAfterEveryRandomInsert`** — a property-style test. Insert 200 random unique 8-byte-payload cells, calling `Validate(pg)` after every single insert. Any layout drift — slots pointing into header space, keys out of order, free-space underflow — fails the test immediately. This is the most rigorous test in the package: it would take an adversarial implementation to pass it without actually maintaining the invariants.
- **`TestPersistAcrossReopen`** — the end-to-end M1+M2+M3 integration test. Opens a real `.godb` file, allocates a leaf, encodes three rows with `record.EncodeRow`, inserts them as cells, writes the page, syncs, closes. Reopens, reads the page, decodes the rows, asserts they match. This is the first test in the project that exercises all three layers together. When it passes, you know storage + records + slotted page compose correctly.

The rest are unit tests for each operation: init zeros the body, init rejects non-leaf types, sorted-on-random-insert, dup-key rejection preserves state, page-full leaves the page re-iterable, oversized-cell rejection, free-bytes monotonicity, directory-doesn't-overlap-payloads, iterate stops on error.

## What this layer cannot do yet

- **No B+tree.** M3 operates on a single page. A B+tree on top of it (M4 for single-page, M5 for multi-page splits) is the next step.
- **No internal-page cells.** Internal-node cell format (`[child_id: u64][separator_key: uvarint]` plus a rightmost-child pointer) is M5.
- **No cross-page iteration.** `IterateCells` walks one page. A full-table scan that crosses leaf siblings via the `RightSibling` pointer is M4+.
- **No deletion.** No `DeleteCell`. Deletion requires in-page compaction (and eventually freelist reuse at the pager level); both v0.2 territory.
- **No cell update.** No `UpdateCell` — the path is delete-and-reinsert, which doesn't exist yet. Workaround for M3 users: rewrite the whole page.
- **No overflow pages.** A cell whose encoded size exceeds `PageSize - HeaderSize - slotSize` returns `ErrCellTooLarge`. Overflow chains (a cell whose payload spans multiple pages) are deferred.
- **No page checksum verification.** The header's checksum field is reserved but always 0. Detecting bit rot is v0.2.

Each of these is a milestone hook.

## Further reading

- The SQLite [B-Tree Pages](https://www.sqlite.org/fileformat.html#b_tree_pages) section. SQLite uses a richer cell layout (which encodes payload-on-overflow and serializes key/data together for index pages) but the slotted structure is recognizable.
- "Database System Concepts" (Silberschatz, Korth, Sudarshan), chapter on storage and indexing — the canonical slotted-page diagram lives there.
- The CMU 15-445 lectures on storage models (lecture 4-ish in the standard schedule). Andy Pavlo walks through slotted-page tradeoffs in detail.

## Where the next chapter picks up

You can now put many typed rows in one page, find them by primary key, and persist them. What you can't do is grow past one page. A real table needs *more than 4 KB of data*, and once you cross that threshold the slotted page returns `ErrPageFull` and you have nowhere to go.

The answer is a **B+tree**: a tree of pages where the root and the internal nodes guide a search to the right leaf, and inserts that overflow a leaf get handled by splitting the leaf into two and adding a new separator key to the parent.

M4 (the next milestone) builds the easy version: a B+tree that only ever has one leaf — exactly one page — so we never actually have to split. It's a tiny step up from M3, but it establishes the `Tree` type, gives the catalog (in the future) a place to store a root page ID, and lays out the `Insert / Get / Scan` API that M5's multi-page version will inherit. M5 then teaches the tree to split and grow.

That's where the next chapter picks up.

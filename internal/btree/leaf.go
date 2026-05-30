package btree

import (
	"encoding/binary"
	"fmt"

	"github.com/felipegalante/godb/internal/storage"
)

// slotSize is the on-disk size of one cell-directory entry: a u16 BE
// offset pointing at the start of the cell payload in the page body.
const slotSize = 2

// isLeafType returns true for page type bytes that are recognized leaves
// (table or index). M3 only writes table leaves; index leaves are
// reserved for v0.2.
func isLeafType(t storage.PageType) bool {
	return t == storage.PageTypeTableLeaf || t == storage.PageTypeIndexLeaf
}

// InitLeaf initializes pg as an empty slotted leaf. The page type byte
// must already be set (Pager.AllocatePage does this). The body is
// zero-filled.
func InitLeaf(pg *storage.Page) error {
	t := storage.PageType(pg.Data[0])
	if !isLeafType(t) {
		return fmt.Errorf("%w: page type 0x%02x", ErrNotLeaf, byte(t))
	}
	for i := 1; i < storage.PageSize; i++ {
		pg.Data[i] = 0
	}
	WriteHeader(pg, PageHeader{
		Type:            t,
		CellCount:       0,
		FreeSpaceOffset: storage.PageSize,
		CellDirEnd:      HeaderSize,
	})
	pg.Dirty = true
	return nil
}

// requireLeaf checks the type byte and returns the decoded header, or
// an error if pg is not a leaf.
func requireLeaf(pg *storage.Page) (PageHeader, error) {
	h := ReadHeader(pg)
	if !isLeafType(h.Type) {
		return h, fmt.Errorf("%w: page type 0x%02x", ErrNotLeaf, byte(h.Type))
	}
	return h, nil
}

// CellCount returns the number of cells currently stored.
func CellCount(pg *storage.Page) int {
	return int(ReadHeader(pg).CellCount)
}

// FreeBytes returns the number of unused bytes between the cell
// directory and the cell payload region. An insert must fit cellSize
// plus a 2-byte directory slot into this.
func FreeBytes(pg *storage.Page) int {
	h := ReadHeader(pg)
	if h.FreeSpaceOffset < h.CellDirEnd {
		return 0
	}
	return int(h.FreeSpaceOffset) - int(h.CellDirEnd)
}

// slotOffset returns the start byte offset of the i-th cell-directory
// entry in pg's body.
func slotOffset(i int) int {
	return HeaderSize + i*slotSize
}

// readSlot returns the cell offset stored at directory index i.
func readSlot(pg *storage.Page, i int) uint16 {
	return binary.BigEndian.Uint16(pg.Data[slotOffset(i) : slotOffset(i)+slotSize])
}

// writeSlot writes a cell offset into directory index i.
func writeSlot(pg *storage.Page, i int, off uint16) {
	binary.BigEndian.PutUint16(pg.Data[slotOffset(i):slotOffset(i)+slotSize], off)
}

// search returns the directory index where key is located (or where it
// would be inserted) and whether key was found. Uses binary search; for
// each probe it decodes only the cell's key prefix.
func search(pg *storage.Page, h PageHeader, key uint64) (int, bool, error) {
	lo, hi := 0, int(h.CellCount)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		off := readSlot(pg, mid)
		if int(off) >= storage.PageSize {
			return 0, false, &storage.CorruptionError{PageID: pg.ID, Reason: fmt.Sprintf("slot %d offset %d out of range", mid, off)}
		}
		k, _, err := readCellKey(pg.Data[off:])
		if err != nil {
			return 0, false, &storage.CorruptionError{PageID: pg.ID, Reason: err.Error()}
		}
		switch {
		case k < key:
			lo = mid + 1
		case k > key:
			hi = mid
		default:
			return mid, true, nil
		}
	}
	return lo, false, nil
}

// InsertCell adds (key, payload) to pg in key-sorted order. The page is
// mutated in place; the caller is responsible for Pager.WritePage.
func InsertCell(pg *storage.Page, key uint64, payload []byte) error {
	h, err := requireLeaf(pg)
	if err != nil {
		return err
	}
	cs := cellSize(key, len(payload))
	// A cell alone must fit in the body of an otherwise-empty page.
	maxBody := storage.PageSize - HeaderSize - slotSize
	if cs > maxBody {
		return fmt.Errorf("%w: cell is %d bytes, max body %d", ErrCellTooLarge, cs, maxBody)
	}

	idx, found, err := search(pg, h, key)
	if err != nil {
		return err
	}
	if found {
		return ErrDuplicateKey
	}

	need := cs + slotSize
	if need > FreeBytes(pg) {
		return ErrPageFull
	}

	// Write the cell into the bottom of the free region.
	newOffset := int(h.FreeSpaceOffset) - cs
	if _, err := writeCell(pg.Data[newOffset:int(h.FreeSpaceOffset)], key, payload); err != nil {
		return fmt.Errorf("btree: InsertCell: %w", err)
	}

	// Shift directory entries at [idx, CellCount) right by slotSize.
	from := slotOffset(idx)
	to := slotOffset(idx + 1)
	end := slotOffset(int(h.CellCount))
	if idx < int(h.CellCount) {
		copy(pg.Data[to:to+(end-from)], pg.Data[from:end])
	}

	// Write the new slot.
	writeSlot(pg, idx, uint16(newOffset))

	// Update header.
	h.CellCount++
	h.FreeSpaceOffset = uint16(newOffset)
	h.CellDirEnd += slotSize
	WriteHeader(pg, h)
	pg.Dirty = true
	return nil
}

// GetCell returns the payload for key as a freshly-allocated copy.
// found is true iff the key exists on the page.
func GetCell(pg *storage.Page, key uint64) ([]byte, bool, error) {
	h, err := requireLeaf(pg)
	if err != nil {
		return nil, false, err
	}
	idx, found, err := search(pg, h, key)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	off := readSlot(pg, idx)
	_, payload, _, err := readCell(pg.Data[off:])
	if err != nil {
		return nil, false, &storage.CorruptionError{PageID: pg.ID, Reason: err.Error()}
	}
	out := make([]byte, len(payload))
	copy(out, payload)
	return out, true, nil
}

// IterateCells calls fn for each (key, payload) in key order. The
// payload slice aliases page memory and is valid only for the duration
// of fn — copy if you need to retain it. Returning a non-nil error
// from fn stops iteration and propagates the error to the caller.
func IterateCells(pg *storage.Page, fn func(key uint64, payload []byte) error) error {
	h, err := requireLeaf(pg)
	if err != nil {
		return err
	}
	for i := 0; i < int(h.CellCount); i++ {
		off := readSlot(pg, i)
		k, payload, _, err := readCell(pg.Data[off:])
		if err != nil {
			return &storage.CorruptionError{PageID: pg.ID, Reason: err.Error()}
		}
		if err := fn(k, payload); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks the slotted-page invariants and returns a
// *storage.CorruptionError on the first violation. It is read-only.
func Validate(pg *storage.Page) error {
	h, err := requireLeaf(pg)
	if err != nil {
		return err
	}
	corrupt := func(reason string, args ...any) error {
		return &storage.CorruptionError{PageID: pg.ID, Reason: fmt.Sprintf(reason, args...)}
	}
	if h.CellDirEnd < HeaderSize {
		return corrupt("cell dir end %d < header size %d", h.CellDirEnd, HeaderSize)
	}
	if h.FreeSpaceOffset > storage.PageSize {
		return corrupt("free space offset %d > page size %d", h.FreeSpaceOffset, storage.PageSize)
	}
	if h.CellDirEnd > h.FreeSpaceOffset {
		return corrupt("cell dir end %d > free space offset %d", h.CellDirEnd, h.FreeSpaceOffset)
	}
	if int(h.CellDirEnd)-HeaderSize != int(h.CellCount)*slotSize {
		return corrupt("cell count %d inconsistent with dir end %d (header=%d, slot=%d)",
			h.CellCount, h.CellDirEnd, HeaderSize, slotSize)
	}

	// Walk every cell, check key ordering and offsets.
	var prevKey uint64
	havePrev := false
	for i := 0; i < int(h.CellCount); i++ {
		off := readSlot(pg, i)
		if int(off) < int(h.FreeSpaceOffset) || int(off) >= storage.PageSize {
			return corrupt("slot %d offset %d not in [%d, %d)", i, off, h.FreeSpaceOffset, storage.PageSize)
		}
		k, _, n, err := readCell(pg.Data[off:])
		if err != nil {
			return corrupt("cell %d at %d: %v", i, off, err)
		}
		if int(off)+n > storage.PageSize {
			return corrupt("cell %d at %d extends past end of page", i, off)
		}
		if havePrev && k <= prevKey {
			return corrupt("cells not strictly sorted at index %d: prev=%d cur=%d", i, prevKey, k)
		}
		prevKey, havePrev = k, true
	}
	return nil
}

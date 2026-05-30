package btree

import (
	"encoding/binary"
	"fmt"

	"github.com/felipegalante/godb/internal/storage"
)

// This file implements the slotted-page operations for *internal* B+tree
// pages. The structural concerns — page header, cell directory growing
// forward, payload region growing backward, free-space accounting — are
// identical to leaf pages (see leaf.go). What differs is the cell format
// and the meaning of PageHeader.RightSibling, which on an internal page
// holds the rightmost-child page id (see ADR-0013).

// requireInternal checks the type byte and returns the decoded header,
// or an error if pg is not an internal page.
func requireInternal(pg *storage.Page) (PageHeader, error) {
	h := ReadHeader(pg)
	if !isInternalType(h.Type) {
		return h, fmt.Errorf("%w: page type 0x%02x is not an internal page", ErrNotLeaf, byte(h.Type))
	}
	return h, nil
}

// InitInternal initializes pg as an internal page with exactly one
// separator cell and a rightmost child. This is the layout the root-grow
// path produces: a brand-new root with one cell and two children.
//
// The page type byte must already be set to PageTypeTableInternal (or
// PageTypeIndexInternal) by Pager.AllocatePage. The body is zero-filled
// before the cell is written.
func InitInternal(pg *storage.Page, leftChild storage.PageID, separator uint64, rightChild storage.PageID) error {
	t := storage.PageType(pg.Data[0])
	if !isInternalType(t) {
		return fmt.Errorf("%w: page type 0x%02x is not an internal page", ErrNotLeaf, byte(t))
	}
	// Zero the body.
	for i := 1; i < storage.PageSize; i++ {
		pg.Data[i] = 0
	}

	// Write the single cell at the bottom of the page.
	cs := internalCellSize(separator)
	if HeaderSize+slotSize+cs > storage.PageSize {
		return fmt.Errorf("%w: internal cell too large (%d bytes)", ErrCellTooLarge, cs)
	}
	cellOffset := storage.PageSize - cs
	if _, err := writeInternalCell(pg.Data[cellOffset:storage.PageSize], leftChild, separator); err != nil {
		return err
	}

	// Write the directory entry pointing at the cell.
	binary.BigEndian.PutUint16(pg.Data[HeaderSize:HeaderSize+slotSize], uint16(cellOffset))

	// Write the header. RightSibling holds the rightmost-child id.
	WriteHeader(pg, PageHeader{
		Type:            t,
		CellCount:       1,
		FreeSpaceOffset: uint16(cellOffset),
		CellDirEnd:      HeaderSize + slotSize,
		RightSibling:    rightChild,
	})
	pg.Dirty = true
	return nil
}

// InternalCellCount returns the number of separator cells on the page.
// (The rightmost-child pointer is not counted as a cell.)
func InternalCellCount(pg *storage.Page) int {
	return int(ReadHeader(pg).CellCount)
}

// InternalFreeBytes returns the number of unused bytes in the middle of
// the page. Mirrors FreeBytes for leaves.
func InternalFreeBytes(pg *storage.Page) int {
	h := ReadHeader(pg)
	if h.FreeSpaceOffset < h.CellDirEnd {
		return 0
	}
	return int(h.FreeSpaceOffset) - int(h.CellDirEnd)
}

// RightmostChild returns the rightmost-child page id (stored in
// PageHeader.RightSibling for internal pages, per ADR-0013).
func RightmostChild(pg *storage.Page) (storage.PageID, error) {
	h, err := requireInternal(pg)
	if err != nil {
		return 0, err
	}
	return h.RightSibling, nil
}

// SetRightmostChild updates the rightmost-child pointer in the header.
func SetRightmostChild(pg *storage.Page, id storage.PageID) error {
	h, err := requireInternal(pg)
	if err != nil {
		return err
	}
	h.RightSibling = id
	WriteHeader(pg, h)
	pg.Dirty = true
	return nil
}

// searchInternal returns the directory index where separator would be
// found or inserted, plus whether an exact match exists.
func searchInternal(pg *storage.Page, h PageHeader, separator uint64) (int, bool, error) {
	lo, hi := 0, int(h.CellCount)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		off := readSlot(pg, mid)
		if int(off) >= storage.PageSize {
			return 0, false, &storage.CorruptionError{PageID: pg.ID, Reason: fmt.Sprintf("internal slot %d offset %d out of range", mid, off)}
		}
		sep, _, err := readInternalCellSeparator(pg.Data[off:])
		if err != nil {
			return 0, false, &storage.CorruptionError{PageID: pg.ID, Reason: err.Error()}
		}
		switch {
		case sep < separator:
			lo = mid + 1
		case sep > separator:
			hi = mid
		default:
			return mid, true, nil
		}
	}
	return lo, false, nil
}

// InsertInternalCell adds a (childID, separator) cell to the page in
// sorted-separator order. Returns ErrPageFull if the cell plus its
// 2-byte directory slot does not fit, and ErrDuplicateKey if separator
// is already present (which would indicate a logic bug — internal-page
// separators are derived from leaf splits and should be unique).
func InsertInternalCell(pg *storage.Page, childID storage.PageID, separator uint64) error {
	h, err := requireInternal(pg)
	if err != nil {
		return err
	}
	cs := internalCellSize(separator)
	maxBody := storage.PageSize - HeaderSize - slotSize
	if cs > maxBody {
		return fmt.Errorf("%w: internal cell is %d bytes, max body %d", ErrCellTooLarge, cs, maxBody)
	}

	idx, found, err := searchInternal(pg, h, separator)
	if err != nil {
		return err
	}
	if found {
		return ErrDuplicateKey
	}

	need := cs + slotSize
	if need > InternalFreeBytes(pg) {
		return ErrPageFull
	}

	newOffset := int(h.FreeSpaceOffset) - cs
	if _, err := writeInternalCell(pg.Data[newOffset:int(h.FreeSpaceOffset)], childID, separator); err != nil {
		return err
	}

	// Shift directory entries [idx, CellCount) right by slotSize.
	from := slotOffset(idx)
	to := slotOffset(idx + 1)
	end := slotOffset(int(h.CellCount))
	if idx < int(h.CellCount) {
		copy(pg.Data[to:to+(end-from)], pg.Data[from:end])
	}
	writeSlot(pg, idx, uint16(newOffset))

	h.CellCount++
	h.FreeSpaceOffset = uint16(newOffset)
	h.CellDirEnd += slotSize
	WriteHeader(pg, h)
	pg.Dirty = true
	return nil
}

// FindChild applies the B+tree descent rule to pick which child of pg to
// follow for a search key:
//
//   - Find the smallest i such that key < separator[i].
//   - If such i exists, return childID[i].
//   - Otherwise (key >= all separators), return RightmostChild.
//
// Equality with a separator descends *right* of it (i.e. into the next
// child), which is the standard B+tree convention when all keys live in
// the leaves.
func FindChild(pg *storage.Page, key uint64) (storage.PageID, error) {
	h, err := requireInternal(pg)
	if err != nil {
		return 0, err
	}
	lo, hi := 0, int(h.CellCount)
	// Binary search for smallest i where separator[i] > key.
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		off := readSlot(pg, mid)
		sep, _, err := readInternalCellSeparator(pg.Data[off:])
		if err != nil {
			return 0, &storage.CorruptionError{PageID: pg.ID, Reason: err.Error()}
		}
		if sep > key {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if lo < int(h.CellCount) {
		off := readSlot(pg, lo)
		childID, _, _, err := readInternalCell(pg.Data[off:])
		if err != nil {
			return 0, &storage.CorruptionError{PageID: pg.ID, Reason: err.Error()}
		}
		return childID, nil
	}
	// key >= all separators → rightmost child
	return h.RightSibling, nil
}

// IterateInternalCells calls fn for each (childID, separator) in
// separator-ascending order. After the cell loop, fn is NOT called for
// the rightmost child — use RightmostChild for that. (This keeps the
// cell/rightmost asymmetry explicit; callers walking every child can
// loop then append rightmost themselves.)
func IterateInternalCells(pg *storage.Page, fn func(childID storage.PageID, separator uint64) error) error {
	h, err := requireInternal(pg)
	if err != nil {
		return err
	}
	for i := 0; i < int(h.CellCount); i++ {
		off := readSlot(pg, i)
		childID, sep, _, err := readInternalCell(pg.Data[off:])
		if err != nil {
			return &storage.CorruptionError{PageID: pg.ID, Reason: err.Error()}
		}
		if err := fn(childID, sep); err != nil {
			return err
		}
	}
	return nil
}

// ValidateInternal checks the slotted-page invariants for an internal
// page and that separators are strictly ascending. The rightmost-child
// pointer is required to be non-zero (an internal page with no
// rightmost is malformed).
func ValidateInternal(pg *storage.Page) error {
	h, err := requireInternal(pg)
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
		return corrupt("cell count %d inconsistent with dir end %d", h.CellCount, h.CellDirEnd)
	}
	if h.CellCount > 0 && h.RightSibling == 0 {
		return corrupt("internal page with %d cells has no rightmost child", h.CellCount)
	}

	var prevSep uint64
	havePrev := false
	for i := 0; i < int(h.CellCount); i++ {
		off := readSlot(pg, i)
		if int(off) < int(h.FreeSpaceOffset) || int(off) >= storage.PageSize {
			return corrupt("internal slot %d offset %d not in [%d, %d)", i, off, h.FreeSpaceOffset, storage.PageSize)
		}
		_, sep, n, err := readInternalCell(pg.Data[off:])
		if err != nil {
			return corrupt("internal cell %d at %d: %v", i, off, err)
		}
		if int(off)+n > storage.PageSize {
			return corrupt("internal cell %d at %d extends past page end", i, off)
		}
		if havePrev && sep <= prevSep {
			return corrupt("separators not strictly ascending at index %d: prev=%d cur=%d", i, prevSep, sep)
		}
		prevSep, havePrev = sep, true
	}
	return nil
}

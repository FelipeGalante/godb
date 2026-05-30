package btree

import "errors"

var (
	// ErrPageFull is returned by InsertCell when the new cell plus its
	// 2-byte directory slot would not fit in the page's free space.
	// The page state is unchanged on this error.
	ErrPageFull = errors.New("btree: page full")

	// ErrDuplicateKey is returned by InsertCell when key already exists
	// on the page. The page state is unchanged.
	ErrDuplicateKey = errors.New("btree: duplicate key")

	// ErrCellTooLarge is returned by InsertCell when the cell alone
	// (without directory slot) exceeds the body capacity of an empty
	// page. v0.1 has no overflow pages, so a single oversize cell is
	// unrecoverable here.
	ErrCellTooLarge = errors.New("btree: cell too large for page")

	// ErrNotLeaf is returned by leaf operations called on a page whose
	// type byte is not a recognized leaf type.
	ErrNotLeaf = errors.New("btree: not a leaf page")

	// ErrSizeChanged is returned by UpdateCellSameSize when the new
	// payload's encoded cell size differs from the existing cell's.
	// The page state is unchanged on this error.
	//
	// The constraint exists because the slotted page has no in-place
	// resize story in v0.1 — growing the cell would require either
	// finding new free space (uncertain) or delete + reinsert (no
	// delete primitive yet). The catalog uses this primitive to
	// update a table's RootPageID (a fixed-width field), so by
	// construction the new payload is the same size as the old; for
	// callers whose update changes size, the right answer in v0.1 is
	// not to call this primitive at all.
	ErrSizeChanged = errors.New("btree: cell size changed")

	// ErrKeyNotFound is returned by UpdateCellSameSize when no cell
	// exists at the given key. The page state is unchanged.
	ErrKeyNotFound = errors.New("btree: key not found")
)

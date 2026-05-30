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
)

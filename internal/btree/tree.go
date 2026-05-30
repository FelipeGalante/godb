package btree

import (
	"fmt"

	"github.com/felipegalante/godb/internal/storage"
)

// Tree is a single-root B+tree over the slotted-page primitives in this
// package. In M4 the tree's height is always zero: the root is also the
// only leaf, and Insert returns ErrPageFull when the leaf fills. M5
// teaches the tree to split a full leaf and grow taller.
//
// A Tree value holds a pager reference and a root page id. It does no
// caching — each operation reads the root page from the pager, performs
// the slotted-page operation, and writes the page back when mutated.
// Concurrency is the pager's responsibility (its mutex serializes the
// reads and writes).
type Tree struct {
	pager  *storage.Pager
	rootID storage.PageID
}

// Create allocates a fresh table-leaf page via the pager and initializes
// it as an empty slotted leaf. The returned Tree wraps that page as its
// root. Persisting the root page id across reopens is the caller's
// responsibility — see Pager.SetCatalogRoot for the v0.1 hook.
func Create(pager *storage.Pager) (*Tree, error) {
	if pager == nil {
		return nil, fmt.Errorf("btree.Create: nil pager")
	}
	pg, err := pager.AllocatePage(storage.PageTypeTableLeaf)
	if err != nil {
		return nil, fmt.Errorf("btree.Create: %w", err)
	}
	if err := InitLeaf(pg); err != nil {
		return nil, fmt.Errorf("btree.Create: %w", err)
	}
	if err := pager.WritePage(pg); err != nil {
		return nil, fmt.Errorf("btree.Create: writing root: %w", err)
	}
	return &Tree{pager: pager, rootID: pg.ID}, nil
}

// Open wraps an existing root page in a Tree. It does no I/O — the root
// page is only read on the first operation, so a wrong rootID surfaces
// as the underlying storage or btree error (ErrPageOutOfRange,
// ErrNotLeaf, etc.) at use time rather than at Open.
func Open(pager *storage.Pager, rootID storage.PageID) *Tree {
	return &Tree{pager: pager, rootID: rootID}
}

// RootPageID returns the page id of the tree's root. Stable for the
// lifetime of the Tree value in M4 (no splits, no root growth).
func (t *Tree) RootPageID() storage.PageID {
	return t.rootID
}

// Insert adds (key, payload) to the tree. M4 has a single-leaf tree, so
// this reads the root page, calls InsertCell, and writes the page back
// on success. ErrDuplicateKey, ErrPageFull, ErrCellTooLarge, and any
// storage error propagate unchanged. The page is left intact on every
// error path (InsertCell guarantees this).
func (t *Tree) Insert(key uint64, payload []byte) error {
	pg, err := t.pager.ReadPage(t.rootID)
	if err != nil {
		return fmt.Errorf("btree.Tree.Insert: read root: %w", err)
	}
	if err := InsertCell(pg, key, payload); err != nil {
		return err
	}
	if err := t.pager.WritePage(pg); err != nil {
		return fmt.Errorf("btree.Tree.Insert: write root: %w", err)
	}
	return nil
}

// Get returns a copy of the payload for key. found is false when key is
// absent; no error is returned in that case.
func (t *Tree) Get(key uint64) ([]byte, bool, error) {
	pg, err := t.pager.ReadPage(t.rootID)
	if err != nil {
		return nil, false, fmt.Errorf("btree.Tree.Get: read root: %w", err)
	}
	return GetCell(pg, key)
}

// Scan walks every cell in key order. The payload slice given to fn
// aliases page memory and is valid only for the duration of the call;
// copy it before retaining. A non-nil return from fn stops iteration
// and propagates to the caller.
func (t *Tree) Scan(fn func(key uint64, payload []byte) error) error {
	pg, err := t.pager.ReadPage(t.rootID)
	if err != nil {
		return fmt.Errorf("btree.Tree.Scan: read root: %w", err)
	}
	return IterateCells(pg, fn)
}

// Validate checks the structural invariants of the tree. In M4 that's
// just the root page's slotted-page invariants; M5 will walk every
// level. Returns a *storage.CorruptionError on the first violation.
func (t *Tree) Validate() error {
	pg, err := t.pager.ReadPage(t.rootID)
	if err != nil {
		return fmt.Errorf("btree.Tree.Validate: read root: %w", err)
	}
	return Validate(pg)
}

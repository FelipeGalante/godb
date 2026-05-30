package btree

import (
	"errors"
	"fmt"

	"github.com/felipegalante/godb/internal/storage"
)

// Tree is a multi-page B+tree over the slotted-page primitives in this
// package. The root is a leaf page in a brand-new tree and may grow into
// an internal page after the first root-overflow split. Every path from
// root to leaf has the same length (the standard B+tree invariant).
//
// A Tree value holds a pager reference and the current root page id.
// Operations re-read pages from the pager on every call — there is no
// caching layer yet (M5; the buffer pool arrives in v0.2). Concurrency
// is the pager's responsibility: each Tree operation calls one or more
// Pager methods, each of which is internally locked.
//
// The Tree does not auto-persist its root page id to the database
// header. Callers that want the root to survive Close/reopen call
// pager.SetCatalogRoot(tree.RootPageID()) themselves (typically before
// pager.Sync or pager.Close). When a root-grow split changes the tree's
// rootID mid-Insert, that change is visible to RootPageID() immediately,
// but the on-disk header still holds the *old* root until SetCatalogRoot
// is called. In a single-process v0.1 workflow this is fine; v0.2's
// rollback journal will close the remaining gap.
type Tree struct {
	pager  *storage.Pager
	rootID storage.PageID
}

// pathEntry records a single step of a descent: the internal page
// visited and which slot was taken. slotIdx in [0, CellCount-1) means
// "took cell[slotIdx].child"; slotIdx == CellCount means "took the
// rightmost child."
type pathEntry struct {
	pageID  storage.PageID
	slotIdx int
}

// leafCell is the in-memory materialization of a leaf cell during a
// split. payload is a fresh copy (the underlying page memory is reused
// during split).
type leafCell struct {
	key     uint64
	payload []byte
}

// internalCell is the in-memory materialization of an internal cell
// during a split or rewrite.
type internalCell struct {
	child     storage.PageID
	separator uint64
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
// as the underlying storage or btree error at use time rather than at
// Open.
func Open(pager *storage.Pager, rootID storage.PageID) *Tree {
	return &Tree{pager: pager, rootID: rootID}
}

// RootPageID returns the page id of the tree's current root. The root
// changes when an overflowing root split grows a new root over the
// previous one; callers that care about persistence should re-read this
// after operations that may have grown the tree.
func (t *Tree) RootPageID() storage.PageID {
	return t.rootID
}

// Insert adds (key, payload) to the tree. It descends from the root to
// the appropriate leaf, inserts the cell, and (on leaf overflow) splits
// the leaf and propagates a separator upward, splitting internal pages
// and growing the root as needed.
//
// Returns ErrDuplicateKey if key is already present (anywhere in the
// tree — the leaf's local check pins global uniqueness because each
// key has exactly one leaf home). Returns ErrCellTooLarge if the cell
// alone would not fit in a fresh page. ErrPageFull should not escape
// under normal use; if it does, treat it as a bug.
func (t *Tree) Insert(key uint64, payload []byte) error {
	pageID := t.rootID
	var path []pathEntry

	for {
		pg, err := t.pager.ReadPage(pageID)
		if err != nil {
			return fmt.Errorf("btree.Tree.Insert: read %d: %w", pageID, err)
		}
		ptype := storage.PageType(pg.Data[0])
		if isLeafType(ptype) {
			// Try the simple insert path first.
			err := InsertCell(pg, key, payload)
			if err == nil {
				return t.pager.WritePage(pg)
			}
			if !errors.Is(err, ErrPageFull) {
				return err
			}
			// Leaf overflowed: split and propagate.
			separator, newRightID, splitErr := t.splitLeaf(pg, key, payload)
			if splitErr != nil {
				return splitErr
			}
			return t.propagateSplit(path, separator, newRightID)
		}
		if !isInternalType(ptype) {
			return &storage.CorruptionError{PageID: pageID, Reason: fmt.Sprintf("unexpected page type 0x%02x during Insert descent", byte(ptype))}
		}
		slotIdx, childID, err := findChildWithSlot(pg, key)
		if err != nil {
			return err
		}
		path = append(path, pathEntry{pageID: pageID, slotIdx: slotIdx})
		pageID = childID
	}
}

// Get descends to the leaf that should contain key and returns its
// payload (as a copy). found is false when key is absent; no error is
// returned in that case.
func (t *Tree) Get(key uint64) ([]byte, bool, error) {
	pageID := t.rootID
	for {
		pg, err := t.pager.ReadPage(pageID)
		if err != nil {
			return nil, false, fmt.Errorf("btree.Tree.Get: read %d: %w", pageID, err)
		}
		ptype := storage.PageType(pg.Data[0])
		if isLeafType(ptype) {
			return GetCell(pg, key)
		}
		if !isInternalType(ptype) {
			return nil, false, &storage.CorruptionError{PageID: pageID, Reason: fmt.Sprintf("unexpected page type 0x%02x during Get descent", byte(ptype))}
		}
		childID, err := FindChild(pg, key)
		if err != nil {
			return nil, false, err
		}
		pageID = childID
	}
}

// Scan walks every cell in key order across all leaves. It descends to
// the leftmost leaf, then walks the leaf chain via RightSibling. The
// payload slice given to fn aliases page memory and is valid only for
// the duration of the call; copy it before retaining. Returning a
// non-nil error from fn stops iteration and propagates the error.
func (t *Tree) Scan(fn func(key uint64, payload []byte) error) error {
	pageID := t.rootID
	for {
		pg, err := t.pager.ReadPage(pageID)
		if err != nil {
			return fmt.Errorf("btree.Tree.Scan: read %d: %w", pageID, err)
		}
		ptype := storage.PageType(pg.Data[0])
		if isLeafType(ptype) {
			return t.walkLeafChain(pg, fn)
		}
		if !isInternalType(ptype) {
			return &storage.CorruptionError{PageID: pageID, Reason: fmt.Sprintf("unexpected page type 0x%02x during Scan descent", byte(ptype))}
		}
		// Leftmost child is cell[0].child, or rightmost if no cells.
		h := ReadHeader(pg)
		if h.CellCount == 0 {
			pageID = h.RightSibling
			continue
		}
		off := readSlot(pg, 0)
		childID, _, _, err := readInternalCell(pg.Data[off:])
		if err != nil {
			return &storage.CorruptionError{PageID: pg.ID, Reason: err.Error()}
		}
		pageID = childID
	}
}

func (t *Tree) walkLeafChain(start *storage.Page, fn func(key uint64, payload []byte) error) error {
	cur := start
	for {
		if err := IterateCells(cur, fn); err != nil {
			return err
		}
		nextID, err := RightSibling(cur)
		if err != nil {
			return err
		}
		if nextID == 0 {
			return nil
		}
		next, err := t.pager.ReadPage(nextID)
		if err != nil {
			return fmt.Errorf("btree.Tree.Scan: read next leaf %d: %w", nextID, err)
		}
		cur = next
	}
}

// Validate walks the tree from the root, checking slotted-page
// invariants at every page, separator/key consistency at every internal
// page, and that all leaves are at the same depth. Returns the first
// violation it finds as a *storage.CorruptionError.
func (t *Tree) Validate() error {
	leafDepthSeen := -1
	return t.validateNode(t.rootID, nil, nil, 0, &leafDepthSeen)
}

func (t *Tree) validateNode(pageID storage.PageID, lower, upper *uint64, depth int, leafDepthSeen *int) error {
	pg, err := t.pager.ReadPage(pageID)
	if err != nil {
		return fmt.Errorf("btree.Tree.Validate: read %d: %w", pageID, err)
	}
	ptype := storage.PageType(pg.Data[0])
	if isLeafType(ptype) {
		if err := Validate(pg); err != nil {
			return err
		}
		if *leafDepthSeen == -1 {
			*leafDepthSeen = depth
		} else if *leafDepthSeen != depth {
			return &storage.CorruptionError{
				PageID: pageID,
				Reason: fmt.Sprintf("leaf at depth %d but earlier leaves were at depth %d", depth, *leafDepthSeen),
			}
		}
		// Verify all keys are in (lower, upper).
		return IterateCells(pg, func(k uint64, _ []byte) error {
			if lower != nil && k < *lower {
				return &storage.CorruptionError{PageID: pageID, Reason: fmt.Sprintf("key %d below lower bound %d", k, *lower)}
			}
			if upper != nil && k >= *upper {
				return &storage.CorruptionError{PageID: pageID, Reason: fmt.Sprintf("key %d at or above upper bound %d", k, *upper)}
			}
			return nil
		})
	}
	if !isInternalType(ptype) {
		return &storage.CorruptionError{PageID: pageID, Reason: fmt.Sprintf("unexpected page type 0x%02x", byte(ptype))}
	}
	if err := ValidateInternal(pg); err != nil {
		return err
	}

	// Recurse into each cell-child, with the appropriate (lower, upper)
	// range derived from surrounding separators.
	h := ReadHeader(pg)
	var prevSep *uint64
	for i := 0; i < int(h.CellCount); i++ {
		off := readSlot(pg, i)
		childID, sep, _, err := readInternalCell(pg.Data[off:])
		if err != nil {
			return &storage.CorruptionError{PageID: pageID, Reason: err.Error()}
		}
		childLower := lower
		if prevSep != nil {
			childLower = prevSep
		}
		sepCopy := sep
		if err := t.validateNode(childID, childLower, &sepCopy, depth+1, leafDepthSeen); err != nil {
			return err
		}
		prevSep = &sepCopy
	}
	// Recurse into the rightmost child: range is (prevSep, upper).
	rmLower := lower
	if prevSep != nil {
		rmLower = prevSep
	}
	return t.validateNode(h.RightSibling, rmLower, upper, depth+1, leafDepthSeen)
}

// ---- split + propagation internals ----

// findChildWithSlot does the descent rule for internal pages but also
// returns the slot index taken (for the path stack). slotIdx ==
// CellCount means "rightmost was taken."
func findChildWithSlot(pg *storage.Page, key uint64) (slotIdx int, childID storage.PageID, err error) {
	h := ReadHeader(pg)
	lo, hi := 0, int(h.CellCount)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		off := readSlot(pg, mid)
		sep, _, e := readInternalCellSeparator(pg.Data[off:])
		if e != nil {
			return 0, 0, &storage.CorruptionError{PageID: pg.ID, Reason: e.Error()}
		}
		if sep > key {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if lo < int(h.CellCount) {
		off := readSlot(pg, lo)
		cid, _, _, e := readInternalCell(pg.Data[off:])
		if e != nil {
			return 0, 0, &storage.CorruptionError{PageID: pg.ID, Reason: e.Error()}
		}
		return lo, cid, nil
	}
	return int(h.CellCount), h.RightSibling, nil
}

// splitLeaf splits leaf, which overflowed when trying to insert
// (newKey, newPayload). Returns the separator (smallest key on the new
// right leaf) and the new right leaf's page id. The leaf chain is kept
// consistent: original leaf's RightSibling -> new leaf -> the leaf
// previously pointed at by original's RightSibling.
func (t *Tree) splitLeaf(leaf *storage.Page, newKey uint64, newPayload []byte) (uint64, storage.PageID, error) {
	// Materialize the desired sorted cell list = existing cells + new
	// cell inserted at the right position. Payloads are copied because
	// the page is about to be reset.
	var desired []leafCell
	inserted := false
	if err := IterateCells(leaf, func(k uint64, p []byte) error {
		if k == newKey {
			return ErrDuplicateKey
		}
		if !inserted && k > newKey {
			desired = append(desired, leafCell{newKey, append([]byte(nil), newPayload...)})
			inserted = true
		}
		desired = append(desired, leafCell{k, append([]byte(nil), p...)})
		return nil
	}); err != nil {
		return 0, 0, err
	}
	if !inserted {
		desired = append(desired, leafCell{newKey, append([]byte(nil), newPayload...)})
	}

	if len(desired) < 2 {
		return 0, 0, fmt.Errorf("btree: splitLeaf: only %d cells after split (internal bug)", len(desired))
	}

	// Median by count.
	mid := len(desired) / 2
	lowerHalf := desired[:mid]
	upperHalf := desired[mid:]

	// Capture the leaf's existing right-sibling pointer so the new leaf
	// can inherit it.
	origRight, err := RightSibling(leaf)
	if err != nil {
		return 0, 0, err
	}

	// Allocate the new right leaf.
	newLeaf, err := t.pager.AllocatePage(storage.PageTypeTableLeaf)
	if err != nil {
		return 0, 0, err
	}
	if err := InitLeaf(newLeaf); err != nil {
		return 0, 0, err
	}
	for _, c := range upperHalf {
		if err := InsertCell(newLeaf, c.key, c.payload); err != nil {
			return 0, 0, fmt.Errorf("btree: splitLeaf: populating new leaf: %w", err)
		}
	}
	if err := SetRightSibling(newLeaf, origRight); err != nil {
		return 0, 0, err
	}

	// Rebuild the original leaf with the lower half. InitLeaf zeros the
	// body and resets the header; we then re-insert.
	if err := InitLeaf(leaf); err != nil {
		return 0, 0, err
	}
	for _, c := range lowerHalf {
		if err := InsertCell(leaf, c.key, c.payload); err != nil {
			return 0, 0, fmt.Errorf("btree: splitLeaf: repopulating leaf: %w", err)
		}
	}
	if err := SetRightSibling(leaf, newLeaf.ID); err != nil {
		return 0, 0, err
	}

	if err := t.pager.WritePage(newLeaf); err != nil {
		return 0, 0, err
	}
	if err := t.pager.WritePage(leaf); err != nil {
		return 0, 0, err
	}

	return upperHalf[0].key, newLeaf.ID, nil
}

// propagateSplit walks the path stack from leaf-parent up toward the
// root, inserting (separator, newRightID) into each level. If a parent
// overflows, it splits and the propagation continues; if the root
// itself splits, a new root is grown above it.
func (t *Tree) propagateSplit(path []pathEntry, separator uint64, newRightID storage.PageID) error {
	for len(path) > 0 {
		entry := path[len(path)-1]
		path = path[:len(path)-1]

		parent, err := t.pager.ReadPage(entry.pageID)
		if err != nil {
			return fmt.Errorf("btree.Tree.Insert: read parent %d: %w", entry.pageID, err)
		}

		cells, rightmost, err := buildParentDesired(parent, entry.slotIdx, separator, newRightID)
		if err != nil {
			return err
		}

		if internalCellsFit(cells) {
			if err := rewriteInternalPage(parent, cells, rightmost); err != nil {
				return err
			}
			return t.pager.WritePage(parent)
		}

		// Parent overflowed too. Split it.
		newSep, newRightInternal, err := t.splitInternal(parent, cells, rightmost)
		if err != nil {
			return err
		}
		separator = newSep
		newRightID = newRightInternal
		// Loop continues up the path.
	}

	// Path is empty: we just split the (current) root. Grow.
	return t.growRoot(separator, newRightID)
}

// buildParentDesired returns the desired (cells, rightmost) for parent
// after a child at slot splitJ has been split into (oldChild, m_key,
// newRightID). It does not mutate parent.
func buildParentDesired(parent *storage.Page, splitJ int, m_key uint64, newRightID storage.PageID) ([]internalCell, storage.PageID, error) {
	var cells []internalCell
	if err := IterateInternalCells(parent, func(child storage.PageID, sep uint64) error {
		cells = append(cells, internalCell{child, sep})
		return nil
	}); err != nil {
		return nil, 0, err
	}
	rightmost, err := RightmostChild(parent)
	if err != nil {
		return nil, 0, err
	}

	if splitJ < len(cells) {
		// Child at cell index splitJ was split.
		// Original cells[splitJ] = (oldChild, oldKey). After split:
		//   cells[splitJ]   = (oldChild, m_key)
		//   insert at splitJ+1 = (newRightID, oldKey)
		oldKey := cells[splitJ].separator
		cells[splitJ].separator = m_key
		// Grow + shift.
		cells = append(cells, internalCell{})
		copy(cells[splitJ+2:], cells[splitJ+1:])
		cells[splitJ+1] = internalCell{newRightID, oldKey}
	} else {
		// Rightmost was split. splitJ == len(cells).
		// New cell appended: (rightmost, m_key); new rightmost = newRightID.
		cells = append(cells, internalCell{rightmost, m_key})
		rightmost = newRightID
	}
	return cells, rightmost, nil
}

// internalCellsFit reports whether the given cells + the page header
// fit on a single page (slot directory size included). Rightmost
// pointer is in the header, so it doesn't affect this check.
func internalCellsFit(cells []internalCell) bool {
	body := HeaderSize
	for _, c := range cells {
		body += slotSize + internalCellSize(c.separator)
	}
	return body <= storage.PageSize
}

// rewriteInternalPage resets pg and writes the given cells +
// rightmost-child pointer. The page type byte is preserved.
func rewriteInternalPage(pg *storage.Page, cells []internalCell, rightmost storage.PageID) error {
	t := storage.PageType(pg.Data[0])
	if !isInternalType(t) {
		return ErrNotLeaf
	}
	for i := 1; i < storage.PageSize; i++ {
		pg.Data[i] = 0
	}
	WriteHeader(pg, PageHeader{
		Type:            t,
		CellCount:       0,
		FreeSpaceOffset: storage.PageSize,
		CellDirEnd:      HeaderSize,
		RightSibling:    rightmost,
	})
	for _, c := range cells {
		if err := InsertInternalCell(pg, c.child, c.separator); err != nil {
			return err
		}
	}
	pg.Dirty = true
	return nil
}

// splitInternal splits left so that the desired (cells, rightmost)
// configuration fits across two pages. The median cell's separator is
// pulled up (returned) and its child becomes the left page's new
// rightmost-child. The right half goes to a newly allocated page.
func (t *Tree) splitInternal(left *storage.Page, cells []internalCell, rightmost storage.PageID) (uint64, storage.PageID, error) {
	if len(cells) < 3 {
		return 0, 0, fmt.Errorf("btree: splitInternal: only %d cells (degenerate)", len(cells))
	}
	mid := len(cells) / 2
	medianSep := cells[mid].separator
	medianChild := cells[mid].child

	leftCells := cells[:mid]
	rightCells := cells[mid+1:]

	leftRightmost := medianChild
	rightRightmost := rightmost

	newRight, err := t.pager.AllocatePage(storage.PageTypeTableInternal)
	if err != nil {
		return 0, 0, err
	}
	if err := InitInternal(newRight, rightCells[0].child, rightCells[0].separator, rightRightmost); err != nil {
		return 0, 0, err
	}
	for i := 1; i < len(rightCells); i++ {
		if err := InsertInternalCell(newRight, rightCells[i].child, rightCells[i].separator); err != nil {
			return 0, 0, fmt.Errorf("btree: splitInternal: populating right page: %w", err)
		}
	}

	if err := rewriteInternalPage(left, leftCells, leftRightmost); err != nil {
		return 0, 0, err
	}

	if err := t.pager.WritePage(newRight); err != nil {
		return 0, 0, err
	}
	if err := t.pager.WritePage(left); err != nil {
		return 0, 0, err
	}
	return medianSep, newRight.ID, nil
}

// growRoot creates a new internal page above the current root, with the
// old root as its left child and newRightID as its rightmost child,
// separated by separator. The tree's rootID is updated; the caller is
// responsible for persisting the new id via pager.SetCatalogRoot.
func (t *Tree) growRoot(separator uint64, newRightID storage.PageID) error {
	newRoot, err := t.pager.AllocatePage(storage.PageTypeTableInternal)
	if err != nil {
		return err
	}
	if err := InitInternal(newRoot, t.rootID, separator, newRightID); err != nil {
		return err
	}
	if err := t.pager.WritePage(newRoot); err != nil {
		return err
	}
	t.rootID = newRoot.ID
	return nil
}

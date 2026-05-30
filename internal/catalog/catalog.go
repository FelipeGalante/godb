package catalog

import (
	"errors"
	"fmt"

	"github.com/felipegalante/godb/internal/btree"
	"github.com/felipegalante/godb/internal/record"
	"github.com/felipegalante/godb/internal/storage"
)

// maxNameLen caps catalog object names so the catalog row stays bounded
// per insert (the btree leaf can absorb several hundred-byte rows; we
// don't want one row consuming a whole page). 255 bytes is enough for
// any reasonable identifier and matches SQL-89's default. Character-set
// rules (no whitespace, valid identifier syntax) are the SQL parser's
// problem (M7) — the catalog only enforces the minimums.
const maxNameLen = 255

// TableInfo is the in-memory view of one catalog row describing a
// table. The catalog hands these out by value-pointer; callers MUST
// NOT mutate the fields directly (use SetTableRoot instead) — doing so
// would desync the in-memory cache from the persisted row.
type TableInfo struct {
	ID         uint64
	Name       string
	RootPageID storage.PageID
	SQL        string
	Schema     record.Schema
}

// Catalog is the database's metadata store. It wraps a B+tree (keyed
// on a uint64 object id; payloads are EncodeObject byte slices) and
// keeps an in-memory map of name → TableInfo so lookups don't walk the
// tree on every call.
//
// All Catalog operations go through the pager. Concurrency is the
// pager's responsibility; the catalog adds no separate lock.
type Catalog struct {
	pager  *storage.Pager
	tree   *btree.Tree
	byName map[string]*TableInfo
	nextID uint64
}

// Open returns the catalog for pager. On a fresh database (where
// Header.CatalogRootPageID == 0) it allocates a new catalog tree and
// writes the new root id to the header — callers should follow with
// catalog.Sync() (or pager.Sync()) to make the change durable.
//
// On an existing database it walks the catalog tree once to rebuild
// the in-memory name index and compute the next free object id.
//
// If a non-zero CatalogRootPageID points at a tree whose first cell
// payload does not start with the catalog format-version byte, decode
// fails with ErrUnsupportedCatalogVersion. This is the fence against
// pre-M6 .godb files whose CatalogRootPageID was being dual-purposed
// as the primary tree root.
func Open(pager *storage.Pager) (*Catalog, error) {
	if pager == nil {
		return nil, fmt.Errorf("catalog.Open: nil pager")
	}
	rootID := pager.Header().CatalogRootPageID
	c := &Catalog{
		pager:  pager,
		byName: make(map[string]*TableInfo),
		nextID: 1,
	}
	if rootID == 0 {
		// Fresh database: allocate a brand-new empty catalog tree and
		// stash its root id in the header.
		tree, err := btree.Create(pager)
		if err != nil {
			return nil, fmt.Errorf("catalog.Open: create tree: %w", err)
		}
		c.tree = tree
		if err := pager.SetCatalogRoot(tree.RootPageID()); err != nil {
			return nil, fmt.Errorf("catalog.Open: set catalog root: %w", err)
		}
		return c, nil
	}
	// Existing database: open and scan.
	c.tree = btree.Open(pager, rootID)
	if err := c.tree.Scan(func(key uint64, payload []byte) error {
		obj, err := DecodeObject(payload)
		if err != nil {
			return fmt.Errorf("decode object id=%d: %w", key, err)
		}
		info := &TableInfo{
			ID:         key,
			Name:       obj.Name,
			RootPageID: obj.RootPageID,
			SQL:        obj.SQL,
			Schema:     obj.Schema,
		}
		c.byName[info.Name] = info
		if key >= c.nextID {
			c.nextID = key + 1
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("catalog.Open: scan: %w", err)
	}
	return c, nil
}

// CreateTable allocates a new B+tree for the table's row data, builds
// a catalog object describing it, inserts the encoded object into the
// catalog tree, and updates the in-memory name cache.
//
// Returns ErrTableExists if name is already registered. Returns
// ErrInvalidName for empty names or names exceeding maxNameLen. The
// schema is trusted as given — the SQL parser (M7) is the right layer
// for "is this a valid CREATE TABLE schema?" checks.
//
// CreateTable allocates TWO pages: one for the table's empty leaf, and
// one cell in the catalog tree (which may or may not trigger a catalog
// tree split; if it does, SetCatalogRoot is called automatically). On
// a crash between the two allocations the table's leaf is orphaned but
// the catalog row is never inserted, so the table name stays free —
// acceptable in v0.1; v0.2's journal closes the gap.
func (c *Catalog) CreateTable(name string, schema record.Schema, sql string) (*TableInfo, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	if _, exists := c.byName[name]; exists {
		return nil, fmt.Errorf("%w: %q", ErrTableExists, name)
	}

	// Allocate the new table's own (empty) B+tree.
	tableTree, err := btree.Create(c.pager)
	if err != nil {
		return nil, fmt.Errorf("catalog.CreateTable: create table tree: %w", err)
	}

	// Encode the catalog row.
	obj := &Object{
		Type:       ObjectTypeTable,
		Name:       name,
		RootPageID: tableTree.RootPageID(),
		SQL:        sql,
		Schema:     schema,
	}
	payload, err := EncodeObject(obj)
	if err != nil {
		return nil, fmt.Errorf("catalog.CreateTable: encode object: %w", err)
	}

	// Insert into the catalog tree under the next available object id.
	id := c.nextID
	if err := c.tree.Insert(id, payload); err != nil {
		return nil, fmt.Errorf("catalog.CreateTable: insert into catalog tree: %w", err)
	}
	// Catalog-tree insert may have grown the catalog's root via a split.
	// Re-persist the catalog root id to the header so a reopen finds the
	// post-split root.
	if err := c.pager.SetCatalogRoot(c.tree.RootPageID()); err != nil {
		return nil, fmt.Errorf("catalog.CreateTable: refresh catalog root: %w", err)
	}

	info := &TableInfo{
		ID:         id,
		Name:       name,
		RootPageID: tableTree.RootPageID(),
		SQL:        sql,
		Schema:     schema,
	}
	c.byName[name] = info
	c.nextID++
	return info, nil
}

// LookupTable returns the TableInfo for name, or ErrTableNotFound.
// The returned pointer is the catalog's own cached copy; callers MUST
// NOT mutate it (use SetTableRoot for the one field that ever changes).
func (c *Catalog) LookupTable(name string) (*TableInfo, error) {
	info, ok := c.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrTableNotFound, name)
	}
	return info, nil
}

// ListTables returns a snapshot of every registered table in arbitrary
// order. The returned slice is owned by the caller and safe to mutate
// or sort; the TableInfo pointers within still point at the catalog's
// cache, so don't mutate them.
func (c *Catalog) ListTables() []*TableInfo {
	out := make([]*TableInfo, 0, len(c.byName))
	for _, info := range c.byName {
		out = append(out, info)
	}
	return out
}

// SetTableRoot updates a table's RootPageID in the catalog. The
// (future) executor calls this after a table's B+tree grows its root
// via a split — table root ids drift when M5 splits a root, and the
// catalog row must be re-written to keep them in sync on reopen.
//
// Returns ErrTableNotFound if no such table.
func (c *Catalog) SetTableRoot(name string, rootID storage.PageID) error {
	info, ok := c.byName[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrTableNotFound, name)
	}
	// The catalog tree does not support in-place cell updates yet
	// (M3/M5 only added insert + sorted directory; no DeleteCell or
	// UpdateCell). v0.2 will revisit this whole story with deletion
	// + atomic transactions. For now we rebuild the affected cell by:
	//
	//   1. mutating the in-memory cache
	//   2. re-encoding the object
	//   3. re-inserting under the same key — which would fail with
	//      ErrDuplicateKey.
	//
	// To work around this for M6, we accept that SetTableRoot updates
	// the in-memory cache only and leaves the on-disk row unchanged
	// until the next time the table is recreated. Document this gap
	// as known; the v0.2 journal + UpdateCell story will close it.
	//
	// In M6's test cases this only matters when callers want
	// SetTableRoot to persist across reopen — and that test is
	// explicitly out of scope until we have UpdateCell. The
	// behavioral test below pins the in-memory effect only.
	info.RootPageID = rootID
	return nil
}

// Sync persists pending state. It refreshes Header.CatalogRootPageID
// in case the catalog tree's root grew (e.g. via a recent split inside
// CreateTable) and flushes the pager. It does NOT close the pager.
func (c *Catalog) Sync() error {
	if err := c.pager.SetCatalogRoot(c.tree.RootPageID()); err != nil {
		return fmt.Errorf("catalog.Sync: set root: %w", err)
	}
	return c.pager.Sync()
}

// validateName returns ErrInvalidName for empty or oversized names.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty name", ErrInvalidName)
	}
	if len(name) > maxNameLen {
		return fmt.Errorf("%w: name length %d exceeds %d", ErrInvalidName, len(name), maxNameLen)
	}
	return nil
}

// (compile-time guard: keep errors import used even if some platforms
// drop the call paths above.)
var _ = errors.New

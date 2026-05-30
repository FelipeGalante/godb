// Package storage owns the on-disk database file and its page abstraction.
// It knows nothing about tables, rows, or SQL — only fixed-size pages.
package storage

// PageSize is the fixed page size for all GoDB v0.1 databases.
// It is not configurable yet; future versions may persist the chosen size
// in the database header (which already reserves space for it).
const PageSize = 4096

// PageID identifies a page within a database file. Page 0 is always the
// database header. Page numbering is zero-based and contiguous.
type PageID uint64

// PageType discriminates page payloads. Most types are reserved for later
// milestones (B+tree leaves/internals, overflow, freelist) but the enum is
// fixed in v0.1 so that on-disk type bytes remain stable as features land.
type PageType uint8

const (
	PageTypeInvalid PageType = iota
	PageTypeHeader
	PageTypeCatalogLeaf
	PageTypeCatalogInternal
	PageTypeTableLeaf
	PageTypeTableInternal
	PageTypeIndexLeaf
	PageTypeIndexInternal
	PageTypeOverflow
	PageTypeFree
)

// Page is an in-memory representation of one on-disk page. Callers mutate
// Data directly; Dirty tracks whether the page needs to be written back.
// In M1 the buffer pool does not exist yet, so the pager returns fresh
// Page values from ReadPage and the caller is responsible for handing the
// same value to WritePage.
type Page struct {
	ID    PageID
	Data  [PageSize]byte
	Dirty bool
}

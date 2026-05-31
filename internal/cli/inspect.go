package cli

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/felipegalante/godb/internal/btree"
	"github.com/felipegalante/godb/internal/catalog"
	"github.com/felipegalante/godb/internal/storage"
)

// openPager opens the database file read-only-ish for the inspect and
// check commands. CreateIfMissing is false so a missing file errors
// rather than fabricating an empty database.
func openPager(path string) (*storage.Pager, error) {
	return storage.OpenPager(path, storage.PagerOptions{CreateIfMissing: false})
}

func runInspect(dbPath string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return usagef("inspect: want a target: header | page <n> | tree")
	}
	switch args[0] {
	case "header":
		return inspectHeader(dbPath, out)
	case "page":
		if len(args) != 2 {
			return usagef("inspect page: want a page number")
		}
		n, err := strconv.ParseUint(args[1], 10, 64)
		if err != nil {
			return usagef("inspect page: invalid page number %q", args[1])
		}
		return inspectPage(dbPath, storage.PageID(n), out)
	case "tree":
		return inspectTree(dbPath, out)
	default:
		return usagef("inspect: unknown target %q (want header | page <n> | tree)", args[0])
	}
}

func inspectHeader(dbPath string, out io.Writer) error {
	pager, err := openPager(dbPath)
	if err != nil {
		return err
	}
	defer pager.Close()
	writeHeader(out, pager.Header())
	return nil
}

func writeHeader(out io.Writer, h storage.Header) {
	fmt.Fprintf(out, "magic:              %s\n", string(storage.Magic[:]))
	fmt.Fprintf(out, "format version:     %d.%d\n", h.FormatMajor, h.FormatMinor)
	fmt.Fprintf(out, "page size:          %d\n", h.PageSize)
	fmt.Fprintf(out, "page count:         %d\n", h.PageCount)
	fmt.Fprintf(out, "catalog root page:  %d\n", h.CatalogRootPageID)
	fmt.Fprintf(out, "freelist head page: %d\n", h.FreelistHeadPage)
	fmt.Fprintf(out, "change counter:     %d\n", h.ChangeCounter)
	fmt.Fprintf(out, "last txn id:        %d\n", h.LastTxnID)
	fmt.Fprintf(out, "checksum algo:      %d\n", h.ChecksumAlgo)
	fmt.Fprintf(out, "flags:              %d\n", h.Flags)
}

func inspectPage(dbPath string, id storage.PageID, out io.Writer) error {
	pager, err := openPager(dbPath)
	if err != nil {
		return err
	}
	defer pager.Close()

	if id == 0 {
		fmt.Fprintln(out, "page 0 is the database file header:")
		writeHeader(out, pager.Header())
		return nil
	}

	pg, err := pager.ReadPage(id)
	if err != nil {
		return err
	}
	ptype := storage.PageType(pg.Data[0])
	h := btree.ReadHeader(pg)
	fmt.Fprintf(out, "page %d\n", id)
	fmt.Fprintf(out, "  type:             %s (0x%02x)\n", pageTypeName(ptype), byte(ptype))

	switch {
	case isLeafType(ptype):
		fmt.Fprintf(out, "  cells:            %d\n", h.CellCount)
		fmt.Fprintf(out, "  free bytes:       %d\n", btree.FreeBytes(pg))
		fmt.Fprintf(out, "  free space off:   %d\n", h.FreeSpaceOffset)
		fmt.Fprintf(out, "  cell dir end:     %d\n", h.CellDirEnd)
		fmt.Fprintf(out, "  right sibling:    %d\n", h.RightSibling)
	case isInternalType(ptype):
		fmt.Fprintf(out, "  separators:       %d\n", h.CellCount)
		fmt.Fprintf(out, "  free bytes:       %d\n", btree.InternalFreeBytes(pg))
		fmt.Fprintf(out, "  free space off:   %d\n", h.FreeSpaceOffset)
		fmt.Fprintf(out, "  cell dir end:     %d\n", h.CellDirEnd)
		rm, err := btree.RightmostChild(pg)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "  rightmost child:  %d\n", rm)
	}
	return nil
}

func inspectTree(dbPath string, out io.Writer) error {
	pager, err := openPager(dbPath)
	if err != nil {
		return err
	}
	defer pager.Close()
	cat, err := catalog.Open(pager)
	if err != nil {
		return err
	}
	tables := sortedTables(cat)
	if len(tables) == 0 {
		fmt.Fprintln(out, "(no tables)")
		return nil
	}
	for _, t := range tables {
		fmt.Fprintf(out, "table %q (root page %d):\n", t.Name, t.RootPageID)
		if err := walkPage(pager, t.RootPageID, out, 1); err != nil {
			return err
		}
	}
	return nil
}

func walkPage(pager *storage.Pager, id storage.PageID, out io.Writer, depth int) error {
	pg, err := pager.ReadPage(id)
	if err != nil {
		return err
	}
	ptype := storage.PageType(pg.Data[0])
	h := btree.ReadHeader(pg)
	indent := strings.Repeat("  ", depth)

	switch {
	case isLeafType(ptype):
		fmt.Fprintf(out, "%sleaf page %d: %d cell%s\n", indent, id, h.CellCount, plural(int(h.CellCount)))
		return nil
	case isInternalType(ptype):
		fmt.Fprintf(out, "%sinternal page %d: %d separator%s\n", indent, id, h.CellCount, plural(int(h.CellCount)))
		var children []storage.PageID
		if err := btree.IterateInternalCells(pg, func(childID storage.PageID, _ uint64) error {
			children = append(children, childID)
			return nil
		}); err != nil {
			return err
		}
		rm, err := btree.RightmostChild(pg)
		if err != nil {
			return err
		}
		children = append(children, rm)
		for _, c := range children {
			if err := walkPage(pager, c, out, depth+1); err != nil {
				return err
			}
		}
		return nil
	default:
		fmt.Fprintf(out, "%spage %d: unexpected type %s\n", indent, id, pageTypeName(ptype))
		return nil
	}
}

func sortedTables(cat *catalog.Catalog) []*catalog.TableInfo {
	tables := cat.ListTables()
	sort.Slice(tables, func(i, j int) bool { return tables[i].Name < tables[j].Name })
	return tables
}

func isLeafType(t storage.PageType) bool {
	return t == storage.PageTypeTableLeaf || t == storage.PageTypeCatalogLeaf || t == storage.PageTypeIndexLeaf
}

func isInternalType(t storage.PageType) bool {
	return t == storage.PageTypeTableInternal || t == storage.PageTypeCatalogInternal || t == storage.PageTypeIndexInternal
}

func pageTypeName(t storage.PageType) string {
	switch t {
	case storage.PageTypeInvalid:
		return "invalid"
	case storage.PageTypeHeader:
		return "header"
	case storage.PageTypeCatalogLeaf:
		return "catalog-leaf"
	case storage.PageTypeCatalogInternal:
		return "catalog-internal"
	case storage.PageTypeTableLeaf:
		return "table-leaf"
	case storage.PageTypeTableInternal:
		return "table-internal"
	case storage.PageTypeIndexLeaf:
		return "index-leaf"
	case storage.PageTypeIndexInternal:
		return "index-internal"
	case storage.PageTypeOverflow:
		return "overflow"
	case storage.PageTypeFree:
		return "free"
	default:
		return "unknown"
	}
}

package cli

import (
	"fmt"
	"io"

	"github.com/felipegalante/godb/internal/btree"
	"github.com/felipegalante/godb/internal/catalog"
)

// runCheck validates the catalog tree and every table's B+tree. Each
// tree is reported as OK or CORRUPT; the command returns a non-nil
// error (non-zero exit) if any tree is corrupt.
func runCheck(dbPath string, out io.Writer) error {
	pager, err := openPager(dbPath)
	if err != nil {
		return err
	}
	defer pager.Close()
	cat, err := catalog.Open(pager)
	if err != nil {
		return err
	}

	problems := 0
	report := func(label string, err error) {
		if err != nil {
			problems++
			fmt.Fprintf(out, "%s: CORRUPT: %v\n", label, err)
		} else {
			fmt.Fprintf(out, "%s: OK\n", label)
		}
	}

	catRoot := pager.Header().CatalogRootPageID
	report("catalog tree", btree.Open(pager, catRoot).Validate())

	for _, t := range sortedTables(cat) {
		report(fmt.Sprintf("table %q", t.Name), btree.Open(pager, t.RootPageID).Validate())
	}

	if problems > 0 {
		return fmt.Errorf("%d corrupt tree%s found", problems, plural(problems))
	}
	return nil
}

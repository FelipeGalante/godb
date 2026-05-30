package catalog

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/felipegalante/godb/internal/btree"
	"github.com/felipegalante/godb/internal/record"
	"github.com/felipegalante/godb/internal/storage"
)

func newPager(t *testing.T) *storage.Pager {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.godb")
	p, err := storage.OpenPager(path, storage.PagerOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("OpenPager: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func openPagerAt(t *testing.T, path string, create bool) *storage.Pager {
	t.Helper()
	p, err := storage.OpenPager(path, storage.PagerOptions{CreateIfMissing: create})
	if err != nil {
		t.Fatalf("OpenPager(%s): %v", path, err)
	}
	return p
}

func usersSchema() record.Schema {
	return record.Schema{Columns: []record.Column{
		{Name: "id", Kind: record.KindInteger, NotNull: true, PrimaryKey: true, Position: 0},
		{Name: "name", Kind: record.KindText, NotNull: true, Position: 1},
		{Name: "active", Kind: record.KindBoolean, Position: 2},
	}}
}

func postsSchema() record.Schema {
	return record.Schema{Columns: []record.Column{
		{Name: "id", Kind: record.KindInteger, NotNull: true, PrimaryKey: true, Position: 0},
		{Name: "title", Kind: record.KindText, NotNull: true, Position: 1},
		{Name: "body", Kind: record.KindText, Position: 2},
	}}
}

func TestOpenOnFreshDatabaseInitializesCatalog(t *testing.T) {
	p := newPager(t)
	if got := p.Header().CatalogRootPageID; got != 0 {
		t.Fatalf("fresh CatalogRootPageID = %d, want 0", got)
	}
	cat, err := Open(p)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := p.Header().CatalogRootPageID; got == 0 {
		t.Errorf("CatalogRootPageID after Open = 0, want non-zero")
	}
	if tables := cat.ListTables(); len(tables) != 0 {
		t.Errorf("ListTables on fresh catalog = %d entries, want 0", len(tables))
	}
}

func TestCreateTableRoundTrip(t *testing.T) {
	p := newPager(t)
	cat, _ := Open(p)
	info, err := cat.CreateTable("users", usersSchema(), "CREATE TABLE users (...)")
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	if info.ID == 0 {
		t.Errorf("ID = 0, want >= 1")
	}
	if info.Name != "users" {
		t.Errorf("Name = %q, want %q", info.Name, "users")
	}
	if info.RootPageID == 0 {
		t.Errorf("RootPageID = 0, want non-zero (a fresh leaf was allocated)")
	}
	if info.SQL != "CREATE TABLE users (...)" {
		t.Errorf("SQL mismatch")
	}
	if len(info.Schema.Columns) != 3 {
		t.Errorf("Schema column count = %d, want 3", len(info.Schema.Columns))
	}

	got, err := cat.LookupTable("users")
	if err != nil {
		t.Fatalf("LookupTable: %v", err)
	}
	if got != info {
		t.Errorf("LookupTable returned a different pointer than CreateTable (catalog should hand out cached info)")
	}

	tables := cat.ListTables()
	if len(tables) != 1 || tables[0].Name != "users" {
		t.Errorf("ListTables = %+v, want [users]", tables)
	}
}

func TestCreateTableRejectsDuplicateName(t *testing.T) {
	p := newPager(t)
	cat, _ := Open(p)
	if _, err := cat.CreateTable("users", usersSchema(), ""); err != nil {
		t.Fatalf("first CreateTable: %v", err)
	}
	_, err := cat.CreateTable("users", postsSchema(), "")
	if !errors.Is(err, ErrTableExists) {
		t.Fatalf("err = %v, want ErrTableExists", err)
	}
	// State unchanged: only one table, original schema.
	tables := cat.ListTables()
	if len(tables) != 1 {
		t.Errorf("ListTables count = %d, want 1", len(tables))
	}
	if got, _ := cat.LookupTable("users"); got.Schema.Columns[1].Name != "name" {
		t.Errorf("original schema not preserved after rejected duplicate")
	}
}

func TestCreateTableRejectsEmptyName(t *testing.T) {
	p := newPager(t)
	cat, _ := Open(p)
	_, err := cat.CreateTable("", usersSchema(), "")
	if !errors.Is(err, ErrInvalidName) {
		t.Fatalf("err = %v, want ErrInvalidName", err)
	}
}

func TestCreateTableRejectsOversizedName(t *testing.T) {
	p := newPager(t)
	cat, _ := Open(p)
	huge := strings.Repeat("a", maxNameLen+1)
	_, err := cat.CreateTable(huge, usersSchema(), "")
	if !errors.Is(err, ErrInvalidName) {
		t.Fatalf("err = %v, want ErrInvalidName", err)
	}
}

func TestLookupUnknownTableReturnsErrTableNotFound(t *testing.T) {
	p := newPager(t)
	cat, _ := Open(p)
	_, err := cat.LookupTable("nope")
	if !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("err = %v, want ErrTableNotFound", err)
	}
}

func TestPersistAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.godb")

	p := openPagerAt(t, path, true)
	cat, err := Open(p)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := cat.CreateTable("users", usersSchema(), "CREATE TABLE users (...)"); err != nil {
		t.Fatalf("CreateTable users: %v", err)
	}
	if _, err := cat.CreateTable("posts", postsSchema(), "CREATE TABLE posts (...)"); err != nil {
		t.Fatalf("CreateTable posts: %v", err)
	}
	if _, err := cat.CreateTable("comments", postsSchema(), ""); err != nil {
		t.Fatalf("CreateTable comments: %v", err)
	}
	if err := cat.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	wantIDs := map[string]uint64{
		"users":    cat.byName["users"].ID,
		"posts":    cat.byName["posts"].ID,
		"comments": cat.byName["comments"].ID,
	}
	wantRoots := map[string]storage.PageID{
		"users":    cat.byName["users"].RootPageID,
		"posts":    cat.byName["posts"].RootPageID,
		"comments": cat.byName["comments"].RootPageID,
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	p2 := openPagerAt(t, path, false)
	t.Cleanup(func() { _ = p2.Close() })
	cat2, err := Open(p2)
	if err != nil {
		t.Fatalf("Open after reopen: %v", err)
	}
	got := cat2.ListTables()
	if len(got) != 3 {
		t.Fatalf("ListTables after reopen = %d, want 3", len(got))
	}
	for _, name := range []string{"users", "posts", "comments"} {
		info, err := cat2.LookupTable(name)
		if err != nil {
			t.Fatalf("LookupTable(%q) after reopen: %v", name, err)
		}
		if info.ID != wantIDs[name] {
			t.Errorf("%s.ID = %d, want %d", name, info.ID, wantIDs[name])
		}
		if info.RootPageID != wantRoots[name] {
			t.Errorf("%s.RootPageID = %d, want %d", name, info.RootPageID, wantRoots[name])
		}
	}
	// The users schema should round-trip column-for-column.
	users, _ := cat2.LookupTable("users")
	if len(users.Schema.Columns) != 3 {
		t.Errorf("users schema columns = %d, want 3", len(users.Schema.Columns))
	}
	if users.Schema.Columns[0].PrimaryKey != true {
		t.Errorf("users.id PrimaryKey = false, want true")
	}
	if users.SQL != "CREATE TABLE users (...)" {
		t.Errorf("users.SQL mismatch")
	}
}

func TestSetTableRootPersistsAcrossReopen(t *testing.T) {
	// Closed in M8 (commit feat(btree+catalog): same-size cell update).
	// Persistence works via btree.Tree.UpdateCellSameSize — the
	// re-encoded catalog object has identical encoded length because
	// only the fixed-width 8-byte RootPageID field changes.
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.godb")

	p := openPagerAt(t, path, true)
	cat, _ := Open(p)
	info, err := cat.CreateTable("users", usersSchema(), "")
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	origRoot := info.RootPageID
	if origRoot == 999 {
		t.Fatalf("test setup confused: initial root happened to be the test sentinel 999")
	}
	if err := cat.SetTableRoot("users", storage.PageID(999)); err != nil {
		t.Fatalf("SetTableRoot: %v", err)
	}
	// In-memory effect is immediate.
	if got, _ := cat.LookupTable("users"); got.RootPageID != 999 {
		t.Errorf("RootPageID immediately after Set = %d, want 999", got.RootPageID)
	}
	if err := cat.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen and confirm the new RootPageID survived.
	p2 := openPagerAt(t, path, false)
	t.Cleanup(func() { _ = p2.Close() })
	cat2, err := Open(p2)
	if err != nil {
		t.Fatalf("reopen Open: %v", err)
	}
	got, err := cat2.LookupTable("users")
	if err != nil {
		t.Fatalf("LookupTable after reopen: %v", err)
	}
	if got.RootPageID != 999 {
		t.Errorf("RootPageID after reopen = %d, want 999", got.RootPageID)
	}
}

func TestSetTableRootRejectsUnknown(t *testing.T) {
	p := newPager(t)
	cat, _ := Open(p)
	err := cat.SetTableRoot("nope", 42)
	if !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("err = %v, want ErrTableNotFound", err)
	}
}

func TestListTablesReturnsAll(t *testing.T) {
	p := newPager(t)
	cat, _ := Open(p)
	names := []string{"a", "b", "c", "d", "e"}
	for _, n := range names {
		if _, err := cat.CreateTable(n, usersSchema(), ""); err != nil {
			t.Fatalf("CreateTable(%q): %v", n, err)
		}
	}
	got := cat.ListTables()
	if len(got) != len(names) {
		t.Fatalf("ListTables = %d, want %d", len(got), len(names))
	}
	gotNames := make([]string, 0, len(got))
	for _, info := range got {
		gotNames = append(gotNames, info.Name)
	}
	sort.Strings(gotNames)
	for i, name := range names {
		if gotNames[i] != name {
			t.Errorf("[sorted] gotNames[%d] = %q, want %q", i, gotNames[i], name)
		}
	}
}

func TestMultipleTablesEachHaveDistinctMonotonicIDs(t *testing.T) {
	p := newPager(t)
	cat, _ := Open(p)
	prev := uint64(0)
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		info, err := cat.CreateTable(n, usersSchema(), "")
		if err != nil {
			t.Fatalf("CreateTable(%q): %v", n, err)
		}
		if info.ID <= prev {
			t.Errorf("CreateTable(%q) ID = %d, not strictly greater than previous %d", n, info.ID, prev)
		}
		prev = info.ID
	}
}

func TestNextIDSurvivesReopenAndExtends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.godb")

	p := openPagerAt(t, path, true)
	cat, _ := Open(p)
	if _, err := cat.CreateTable("a", usersSchema(), ""); err != nil {
		t.Fatalf("CreateTable a: %v", err)
	}
	if _, err := cat.CreateTable("b", usersSchema(), ""); err != nil {
		t.Fatalf("CreateTable b: %v", err)
	}
	wantNext := cat.nextID
	if err := cat.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	p2 := openPagerAt(t, path, false)
	t.Cleanup(func() { _ = p2.Close() })
	cat2, _ := Open(p2)
	if cat2.nextID != wantNext {
		t.Errorf("nextID after reopen = %d, want %d", cat2.nextID, wantNext)
	}
	// New table gets the expected next id.
	info, err := cat2.CreateTable("c", usersSchema(), "")
	if err != nil {
		t.Fatalf("CreateTable c after reopen: %v", err)
	}
	if info.ID != wantNext {
		t.Errorf("new table ID after reopen = %d, want %d", info.ID, wantNext)
	}
}

func TestOpenRejectsPreM6FileWithRegularTreeAtCatalogRoot(t *testing.T) {
	// Simulate a pre-M6 .godb file where Header.CatalogRootPageID points
	// at a regular table tree containing record-encoded rows (not catalog
	// rows). Open should refuse cleanly via ErrUnsupportedCatalogVersion.
	dir := t.TempDir()
	path := filepath.Join(dir, "pre-m6.godb")

	p := openPagerAt(t, path, true)
	// This is exactly what an M4/M5 user did pre-M6:
	//   btree.Create + tree.Insert(some_id, record.EncodeRow(...))
	//   pager.SetCatalogRoot(tree.RootPageID())
	tree, err := btree.Create(p)
	if err != nil {
		t.Fatalf("setup btree.Create: %v", err)
	}
	row, _ := record.EncodeRow([]record.Value{record.Int(1), record.Text("hi")})
	if err := tree.Insert(1, row); err != nil {
		t.Fatalf("setup tree.Insert: %v", err)
	}
	if err := p.SetCatalogRoot(tree.RootPageID()); err != nil {
		t.Fatalf("setup SetCatalogRoot: %v", err)
	}
	if err := p.Sync(); err != nil {
		t.Fatalf("setup Sync: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	p2 := openPagerAt(t, path, false)
	t.Cleanup(func() { _ = p2.Close() })
	_, err = Open(p2)
	if !errors.Is(err, ErrUnsupportedCatalogVersion) {
		t.Fatalf("Open on pre-M6 file: err = %v, want ErrUnsupportedCatalogVersion", err)
	}
}

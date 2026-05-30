package btree

import (
	"bytes"
	"errors"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/felipegalante/godb/internal/record"
	"github.com/felipegalante/godb/internal/storage"
)

func newPager(t *testing.T) *storage.Pager {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tree.godb")
	p, err := storage.OpenPager(path, storage.PagerOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("OpenPager: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// newPagerAt opens a pager at a specific path so reopen tests can use
// the same file under two pagers.
func newPagerAt(t *testing.T, path string, create bool) *storage.Pager {
	t.Helper()
	opts := storage.PagerOptions{CreateIfMissing: create}
	p, err := storage.OpenPager(path, opts)
	if err != nil {
		t.Fatalf("OpenPager(%s): %v", path, err)
	}
	return p
}

func encodeRow(t *testing.T, id int64, name string, active bool) []byte {
	t.Helper()
	buf, err := record.EncodeRow([]record.Value{
		record.Int(id),
		record.Text(name),
		record.Bool(active),
	})
	if err != nil {
		t.Fatalf("EncodeRow: %v", err)
	}
	return buf
}

func TestCreateProducesValidEmptyTree(t *testing.T) {
	p := newPager(t)
	tr, err := Create(p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tr.RootPageID() == 0 {
		t.Fatalf("RootPageID() = 0, want a non-header page id")
	}
	if err := tr.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	got, found, err := tr.Get(42)
	if err != nil || found || got != nil {
		t.Errorf("Get on empty tree: got=%v found=%v err=%v", got, found, err)
	}
	calls := 0
	if err := tr.Scan(func(uint64, []byte) error { calls++; return nil }); err != nil {
		t.Errorf("Scan: %v", err)
	}
	if calls != 0 {
		t.Errorf("Scan called fn %d times on empty tree, want 0", calls)
	}
}

func TestInsertAndGet(t *testing.T) {
	p := newPager(t)
	tr, err := Create(p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := map[uint64][]byte{}
	for _, k := range []uint64{10, 5, 20, 1, 15} {
		payload := encodeRow(t, int64(k), "user", k%2 == 0)
		want[k] = payload
		if err := tr.Insert(k, payload); err != nil {
			t.Fatalf("Insert(%d): %v", k, err)
		}
	}
	for k, payload := range want {
		got, found, err := tr.Get(k)
		if err != nil || !found {
			t.Fatalf("Get(%d): found=%v err=%v", k, found, err)
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("Get(%d) payload mismatch", k)
		}
	}
	if _, found, _ := tr.Get(999); found {
		t.Errorf("Get(999) on absent key returned found=true")
	}
}

func TestScanReturnsSortedKeys(t *testing.T) {
	p := newPager(t)
	tr, err := Create(p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	inserted := []uint64{50, 10, 90, 30, 70, 20, 80, 40, 60}
	for _, k := range inserted {
		if err := tr.Insert(k, []byte{byte(k)}); err != nil {
			t.Fatalf("Insert(%d): %v", k, err)
		}
	}
	var got []uint64
	if err := tr.Scan(func(k uint64, _ []byte) error {
		got = append(got, k)
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	want := []uint64{10, 20, 30, 40, 50, 60, 70, 80, 90}
	if len(got) != len(want) {
		t.Fatalf("got %d keys, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestTreeInsertRejectsDuplicateKey(t *testing.T) {
	p := newPager(t)
	tr, _ := Create(p)
	if err := tr.Insert(42, []byte("first")); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	err := tr.Insert(42, []byte("second"))
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("err = %v, want ErrDuplicateKey", err)
	}
	if err := tr.Validate(); err != nil {
		t.Errorf("Validate after dup-key reject: %v", err)
	}
	got, found, err := tr.Get(42)
	if err != nil || !found {
		t.Fatalf("Get(42): found=%v err=%v", found, err)
	}
	if !bytes.Equal(got, []byte("first")) {
		t.Errorf("payload = %q, want %q (original preserved)", got, "first")
	}
}

func TestInsertReportsPageFullWhenLeafFull(t *testing.T) {
	p := newPager(t)
	tr, _ := Create(p)
	payload := bytes.Repeat([]byte{0xAB}, 500)
	var inserted []uint64
	var k uint64
	for {
		k++
		if err := tr.Insert(k, payload); err != nil {
			if !errors.Is(err, ErrPageFull) {
				t.Fatalf("Insert(%d): %v", k, err)
			}
			break
		}
		inserted = append(inserted, k)
		if len(inserted) > 100 {
			t.Fatalf("expected ErrPageFull eventually; inserted %d cells", len(inserted))
		}
	}
	if len(inserted) == 0 {
		t.Fatalf("could not insert any cells before page full")
	}
	if err := tr.Validate(); err != nil {
		t.Errorf("Validate after page-full: %v", err)
	}
	for _, k := range inserted {
		got, found, err := tr.Get(k)
		if err != nil || !found {
			t.Errorf("Get(%d) after page-full: found=%v err=%v", k, found, err)
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("Get(%d) payload corrupted", k)
		}
	}
}

func TestInsertRejectsOversizedPayload(t *testing.T) {
	p := newPager(t)
	tr, _ := Create(p)
	huge := make([]byte, storage.PageSize)
	err := tr.Insert(1, huge)
	if !errors.Is(err, ErrCellTooLarge) {
		t.Fatalf("err = %v, want ErrCellTooLarge", err)
	}
}

func TestTreePersistAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tree.godb")

	p := newPagerAt(t, path, true)
	tr, err := Create(p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := p.SetCatalogRoot(tr.RootPageID()); err != nil {
		t.Fatalf("SetCatalogRoot: %v", err)
	}
	rows := []struct {
		id     int64
		name   string
		active bool
	}{
		{1, "Felipe", true},
		{2, "MG", true},
		{3, "Jane", false},
	}
	for _, r := range rows {
		if err := tr.Insert(uint64(r.id), encodeRow(t, r.id, r.name, r.active)); err != nil {
			t.Fatalf("Insert(%d): %v", r.id, err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	p2 := newPagerAt(t, path, false)
	t.Cleanup(func() { _ = p2.Close() })

	rootID := p2.Header().CatalogRootPageID
	if rootID == 0 {
		t.Fatalf("CatalogRootPageID after reopen = 0, want non-zero")
	}
	tr2 := Open(p2, rootID)
	if err := tr2.Validate(); err != nil {
		t.Fatalf("Validate after reopen: %v", err)
	}

	var seenNames []string
	if err := tr2.Scan(func(k uint64, payload []byte) error {
		values, _, err := record.DecodeRow(payload)
		if err != nil {
			return err
		}
		if int64(k) != values[0].Int {
			t.Errorf("cell key %d != row id %d", k, values[0].Int)
		}
		seenNames = append(seenNames, values[1].Text)
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	want := []string{"Felipe", "MG", "Jane"}
	if len(seenNames) != len(want) {
		t.Fatalf("got %d rows, want %d", len(seenNames), len(want))
	}
	for i := range want {
		if seenNames[i] != want[i] {
			t.Errorf("row[%d] name = %q, want %q", i, seenNames[i], want[i])
		}
	}
}

func TestScanStopsOnCallbackError(t *testing.T) {
	p := newPager(t)
	tr, _ := Create(p)
	for k := uint64(1); k <= 5; k++ {
		if err := tr.Insert(k, []byte{byte(k)}); err != nil {
			t.Fatalf("Insert(%d): %v", k, err)
		}
	}
	sentinel := errors.New("stop")
	var seen []uint64
	err := tr.Scan(func(k uint64, _ []byte) error {
		seen = append(seen, k)
		if k == 3 {
			return sentinel
		}
		return nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
	if len(seen) != 3 || seen[0] != 1 || seen[1] != 2 || seen[2] != 3 {
		t.Errorf("seen = %v, want [1 2 3]", seen)
	}
}

func TestRoundTripWithEncodedRows(t *testing.T) {
	schema := &record.Schema{Columns: []record.Column{
		{Name: "id", Kind: record.KindInteger, NotNull: true, PrimaryKey: true, Position: 0},
		{Name: "name", Kind: record.KindText, NotNull: true, Position: 1},
		{Name: "active", Kind: record.KindBoolean, Position: 2},
	}}
	p := newPager(t)
	tr, _ := Create(p)

	rows := []struct {
		id     int64
		name   string
		active bool
	}{
		{1, "Felipe", true},
		{2, "MG", true},
		{3, "Jane", false},
	}
	for _, r := range rows {
		values := []record.Value{record.Int(r.id), record.Text(r.name), record.Bool(r.active)}
		if err := schema.Validate(values); err != nil {
			t.Fatalf("schema validate id=%d: %v", r.id, err)
		}
		payload, _ := record.EncodeRow(values)
		if err := tr.Insert(uint64(r.id), payload); err != nil {
			t.Fatalf("Insert(%d): %v", r.id, err)
		}
	}
	idx := 0
	if err := tr.Scan(func(_ uint64, payload []byte) error {
		values, _, err := record.DecodeRow(payload)
		if err != nil {
			return err
		}
		if err := schema.Validate(values); err != nil {
			return err
		}
		want := rows[idx]
		if values[0].Int != want.id || values[1].Text != want.name || values[2].Bool != want.active {
			t.Errorf("row %d: got (%d,%q,%v), want (%d,%q,%v)",
				idx, values[0].Int, values[1].Text, values[2].Bool,
				want.id, want.name, want.active)
		}
		idx++
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if idx != len(rows) {
		t.Errorf("scanned %d rows, want %d", idx, len(rows))
	}
}

func TestRootPageIDIsStable(t *testing.T) {
	p := newPager(t)
	tr, _ := Create(p)
	root := tr.RootPageID()
	for k := uint64(1); k <= 5; k++ {
		if err := tr.Insert(k, []byte{byte(k)}); err != nil {
			t.Fatalf("Insert(%d): %v", k, err)
		}
		if tr.RootPageID() != root {
			t.Fatalf("RootPageID changed after Insert(%d): %d -> %d", k, root, tr.RootPageID())
		}
	}
}

func TestOpenWrongPageTypeReturnsErrNotLeaf(t *testing.T) {
	p := newPager(t)
	// Page 0 is the database header (type = PageTypeHeader), not a leaf.
	tr := Open(p, storage.PageID(0))
	if err := tr.Validate(); !errors.Is(err, ErrNotLeaf) {
		t.Errorf("Validate on header page: err = %v, want ErrNotLeaf", err)
	}
	if err := tr.Insert(1, []byte("x")); !errors.Is(err, ErrNotLeaf) {
		t.Errorf("Insert on header page: err = %v, want ErrNotLeaf", err)
	}
}

func TestValidateAfterRandomInserts(t *testing.T) {
	// Property-style: many random unique inserts; Validate clean throughout.
	p := newPager(t)
	tr, _ := Create(p)
	rng := rand.New(rand.NewSource(0xBEEF))
	seen := make(map[uint64]bool)
	payload := bytes.Repeat([]byte{0x99}, 8)
	const target = 150
	var inserted int
	for attempt := 0; attempt < 5000 && inserted < target; attempt++ {
		k := uint64(rng.Int63n(1_000_000)) + 1
		if seen[k] {
			continue
		}
		if err := tr.Insert(k, payload); err != nil {
			if errors.Is(err, ErrPageFull) {
				t.Fatalf("ErrPageFull after %d inserts; expected target=%d to fit", inserted, target)
			}
			t.Fatalf("Insert(%d): %v", k, err)
		}
		seen[k] = true
		inserted++
		if err := tr.Validate(); err != nil {
			t.Fatalf("Validate after insert #%d (key=%d): %v", inserted, k, err)
		}
	}
	if inserted < target {
		t.Fatalf("only inserted %d/%d unique keys", inserted, target)
	}
}

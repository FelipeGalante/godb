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

// TestInsertBeyondOneLeafSplitsAutomatically replaces the M4-era
// TestInsertReportsPageFullWhenLeafFull: now that the Tree handles leaf
// overflow by splitting, inserting past the capacity of one leaf must
// succeed silently and every prior cell must remain retrievable.
func TestInsertBeyondOneLeafSplitsAutomatically(t *testing.T) {
	p := newPager(t)
	tr, _ := Create(p)
	originalRoot := tr.RootPageID()

	payload := bytes.Repeat([]byte{0xAB}, 500)
	const target = 200 // far more than fits in one 4KB leaf with 500-byte payloads
	for k := uint64(1); k <= target; k++ {
		if err := tr.Insert(k, payload); err != nil {
			t.Fatalf("Insert(%d): %v (expected silent split, not error)", k, err)
		}
	}
	if err := tr.Validate(); err != nil {
		t.Errorf("Validate after %d inserts: %v", target, err)
	}
	if tr.RootPageID() == originalRoot {
		t.Errorf("RootPageID unchanged after %d inserts — expected at least one root grow", target)
	}
	for k := uint64(1); k <= target; k++ {
		got, found, err := tr.Get(k)
		if err != nil || !found {
			t.Errorf("Get(%d) after splits: found=%v err=%v", k, found, err)
			continue
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

// ---- M5 multi-page tree tests ----

// makeBigPayload returns a payload sized to force splits quickly: at
// ~500 bytes each, ~7 cells fit in a 4 KB leaf, so splits happen with
// modest insert counts.
func makeBigPayload(seed byte) []byte {
	return bytes.Repeat([]byte{seed}, 500)
}

func TestInsertGrowsTreePastOnePage(t *testing.T) {
	p := newPager(t)
	tr, _ := Create(p)
	origRoot := tr.RootPageID()
	// 20 inserts of ~500-byte payloads → comfortably forces several
	// splits + at least one root grow.
	for k := uint64(1); k <= 20; k++ {
		if err := tr.Insert(k, makeBigPayload(byte(k))); err != nil {
			t.Fatalf("Insert(%d): %v", k, err)
		}
	}
	if tr.RootPageID() == origRoot {
		t.Fatalf("RootPageID unchanged after 20 splits — expected at least one root grow")
	}
	if err := tr.Validate(); err != nil {
		t.Errorf("Validate after splits: %v", err)
	}
}

func TestInsertManyInOrder(t *testing.T) {
	p := newPager(t)
	tr, _ := Create(p)
	const target = 500
	payload := bytes.Repeat([]byte{0x55}, 200)
	for k := uint64(1); k <= target; k++ {
		if err := tr.Insert(k, payload); err != nil {
			t.Fatalf("Insert(%d): %v", k, err)
		}
		if k%100 == 0 {
			if err := tr.Validate(); err != nil {
				t.Fatalf("Validate at k=%d: %v", k, err)
			}
		}
	}
	// Every key retrievable.
	for k := uint64(1); k <= target; k++ {
		got, found, err := tr.Get(k)
		if err != nil || !found {
			t.Errorf("Get(%d): found=%v err=%v", k, found, err)
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("Get(%d): payload mismatch", k)
		}
	}
	// Scan yields all keys in ascending order.
	var got []uint64
	if err := tr.Scan(func(k uint64, _ []byte) error {
		got = append(got, k)
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != target {
		t.Fatalf("Scan returned %d keys, want %d", len(got), target)
	}
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("Scan keys not strictly ascending at index %d: %d then %d", i, got[i-1], got[i])
		}
	}
}

func TestInsertManyReverseOrder(t *testing.T) {
	p := newPager(t)
	tr, _ := Create(p)
	const target = 500
	payload := bytes.Repeat([]byte{0xAA}, 200)
	for k := target; k >= 1; k-- {
		if err := tr.Insert(uint64(k), payload); err != nil {
			t.Fatalf("Insert(%d): %v", k, err)
		}
	}
	if err := tr.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	var got []uint64
	if err := tr.Scan(func(k uint64, _ []byte) error {
		got = append(got, k)
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != target {
		t.Fatalf("Scan returned %d keys, want %d", len(got), target)
	}
	for i, k := range got {
		want := uint64(i + 1)
		if k != want {
			t.Fatalf("Scan got[%d] = %d, want %d", i, k, want)
		}
	}
}

func TestInsertManyRandomOrder(t *testing.T) {
	p := newPager(t)
	tr, _ := Create(p)
	rng := rand.New(rand.NewSource(0xDEAD))
	seen := make(map[uint64]bool)
	const target = 1000
	payload := bytes.Repeat([]byte{0x33}, 100)
	var keys []uint64
	for attempt := 0; attempt < 50000 && len(seen) < target; attempt++ {
		k := uint64(rng.Int63n(10_000_000)) + 1
		if seen[k] {
			continue
		}
		if err := tr.Insert(k, payload); err != nil {
			t.Fatalf("Insert(%d): %v", k, err)
		}
		seen[k] = true
		keys = append(keys, k)
		if len(keys)%100 == 0 {
			if err := tr.Validate(); err != nil {
				t.Fatalf("Validate at %d inserts: %v", len(keys), err)
			}
		}
	}
	if len(seen) < target {
		t.Fatalf("only inserted %d/%d unique keys", len(seen), target)
	}
	// Every key retrievable.
	for k := range seen {
		_, found, err := tr.Get(k)
		if err != nil || !found {
			t.Errorf("Get(%d): found=%v err=%v", k, found, err)
		}
	}
	// Scan yields all keys in ascending order.
	var scanned []uint64
	if err := tr.Scan(func(k uint64, _ []byte) error {
		scanned = append(scanned, k)
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(scanned) != target {
		t.Fatalf("Scan returned %d keys, want %d", len(scanned), target)
	}
	for i := 1; i < len(scanned); i++ {
		if scanned[i] <= scanned[i-1] {
			t.Fatalf("Scan order broken at %d", i)
		}
	}
}

func TestScanCrossesLeafBoundaries(t *testing.T) {
	p := newPager(t)
	tr, _ := Create(p)
	// Force at least a couple of leaf splits.
	for k := uint64(1); k <= 50; k++ {
		if err := tr.Insert(k, makeBigPayload(byte(k))); err != nil {
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
	if len(got) != 50 {
		t.Fatalf("Scan got %d keys, want 50", len(got))
	}
	for i, k := range got {
		want := uint64(i + 1)
		if k != want {
			t.Fatalf("got[%d] = %d, want %d", i, k, want)
		}
	}
}

func TestPersistAcrossReopenWithSplits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tree_m5.godb")

	p := newPagerAt(t, path, true)
	tr, err := Create(p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	const target = 300
	for k := uint64(1); k <= target; k++ {
		if err := tr.Insert(k, makeBigPayload(byte(k%256))); err != nil {
			t.Fatalf("Insert(%d): %v", k, err)
		}
	}
	if err := p.SetCatalogRoot(tr.RootPageID()); err != nil {
		t.Fatalf("SetCatalogRoot: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	p2 := newPagerAt(t, path, false)
	t.Cleanup(func() { _ = p2.Close() })
	tr2 := Open(p2, p2.Header().CatalogRootPageID)
	if err := tr2.Validate(); err != nil {
		t.Fatalf("Validate after reopen: %v", err)
	}
	var count int
	if err := tr2.Scan(func(k uint64, _ []byte) error {
		count++
		return nil
	}); err != nil {
		t.Fatalf("Scan after reopen: %v", err)
	}
	if count != target {
		t.Fatalf("after reopen: scanned %d, want %d", count, target)
	}
	// Spot-check a few Gets.
	for _, k := range []uint64{1, target / 2, target} {
		got, found, err := tr2.Get(k)
		if err != nil || !found {
			t.Errorf("Get(%d) after reopen: found=%v err=%v", k, found, err)
		}
		want := makeBigPayload(byte(k % 256))
		if !bytes.Equal(got, want) {
			t.Errorf("Get(%d) payload mismatch", k)
		}
	}
}

func TestRootSplitChangesRootID(t *testing.T) {
	p := newPager(t)
	tr, _ := Create(p)
	originalRoot := tr.RootPageID()
	if err := p.SetCatalogRoot(originalRoot); err != nil {
		t.Fatalf("SetCatalogRoot: %v", err)
	}
	// Stuff enough into the tree to force a root grow.
	for k := uint64(1); k <= 30; k++ {
		if err := tr.Insert(k, makeBigPayload(byte(k))); err != nil {
			t.Fatalf("Insert(%d): %v", k, err)
		}
	}
	if tr.RootPageID() == originalRoot {
		t.Fatalf("RootPageID unchanged after 30 inserts — expected root grow")
	}
	// The tree's API does not auto-update the catalog field. Confirm
	// that contract: the header still points at the original (now-stale)
	// root until the caller re-syncs.
	if got := p.Header().CatalogRootPageID; got != originalRoot {
		t.Errorf("CatalogRootPageID auto-changed to %d; M5 contract says caller must explicitly call SetCatalogRoot", got)
	}
	// Now caller does the right thing.
	if err := p.SetCatalogRoot(tr.RootPageID()); err != nil {
		t.Fatalf("SetCatalogRoot: %v", err)
	}
	if got := p.Header().CatalogRootPageID; got != tr.RootPageID() {
		t.Errorf("after SetCatalogRoot: header = %d, want %d", got, tr.RootPageID())
	}
}

func TestInsertDuplicateAcrossLeavesStillRejected(t *testing.T) {
	p := newPager(t)
	tr, _ := Create(p)
	// Build a multi-leaf tree.
	for k := uint64(1); k <= 50; k++ {
		if err := tr.Insert(k, makeBigPayload(byte(k))); err != nil {
			t.Fatalf("Insert(%d): %v", k, err)
		}
	}
	// Pick a key that is now buried in some leaf (not the leftmost or
	// rightmost). Re-insert; must fail.
	target := uint64(25)
	err := tr.Insert(target, []byte("dup"))
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("Insert(dup %d): err = %v, want ErrDuplicateKey", target, err)
	}
	if err := tr.Validate(); err != nil {
		t.Errorf("Validate after dup-key reject: %v", err)
	}
}

func TestScanStopsOnCallbackErrorAcrossLeaves(t *testing.T) {
	p := newPager(t)
	tr, _ := Create(p)
	for k := uint64(1); k <= 40; k++ {
		if err := tr.Insert(k, makeBigPayload(byte(k))); err != nil {
			t.Fatalf("Insert(%d): %v", k, err)
		}
	}
	// Stop iteration at key=20 — which is deliberately *past* the first
	// leaf boundary in the tree above.
	sentinel := errors.New("stop")
	var lastSeen uint64
	err := tr.Scan(func(k uint64, _ []byte) error {
		lastSeen = k
		if k == 20 {
			return sentinel
		}
		return nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
	if lastSeen != 20 {
		t.Errorf("lastSeen = %d, want 20", lastSeen)
	}
}

func TestPropertyInsertGetSubset(t *testing.T) {
	p := newPager(t)
	tr, _ := Create(p)
	rng := rand.New(rand.NewSource(0xCAFE))
	const target = 600
	payload := bytes.Repeat([]byte{0x77}, 80)
	seen := make(map[uint64][]byte)
	for len(seen) < target {
		k := uint64(rng.Int63n(5_000_000)) + 1
		if _, dup := seen[k]; dup {
			continue
		}
		if err := tr.Insert(k, payload); err != nil {
			t.Fatalf("Insert(%d): %v", k, err)
		}
		seen[k] = payload
		// After every insert, do 3 random lookups on past keys.
		if len(seen) > 5 {
			i := 0
			for past := range seen {
				_, found, err := tr.Get(past)
				if err != nil || !found {
					t.Fatalf("Get(%d) after %d inserts: found=%v err=%v", past, len(seen), found, err)
				}
				i++
				if i >= 3 {
					break
				}
			}
		}
	}
}

func TestOpenWrongPageTypeReturnsTypedError(t *testing.T) {
	p := newPager(t)
	// Page 0 is the database header — neither a leaf nor an internal page.
	// Tree operations should refuse it with a typed error (either ErrNotLeaf
	// from a leaf-asserting helper or a *storage.CorruptionError flagging
	// the unexpected page type byte).
	tr := Open(p, storage.PageID(0))

	wantTypedError := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: nil error, want a typed error", name)
			return
		}
		var ce *storage.CorruptionError
		if errors.Is(err, ErrNotLeaf) || errors.As(err, &ce) {
			return
		}
		t.Errorf("%s: err = %v, want ErrNotLeaf or *storage.CorruptionError", name, err)
	}

	wantTypedError("Validate", tr.Validate())
	wantTypedError("Insert", tr.Insert(1, []byte("x")))
	_, _, getErr := tr.Get(1)
	wantTypedError("Get", getErr)
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

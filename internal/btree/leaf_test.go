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

// newEmptyLeaf returns a freshly-allocated, initialized leaf page that
// is *not* backed by a pager — fine for the pure-page tests. Page ID is
// arbitrary.
func newEmptyLeaf(t *testing.T) *storage.Page {
	t.Helper()
	pg := &storage.Page{ID: 1}
	pg.Data[0] = byte(storage.PageTypeTableLeaf)
	if err := InitLeaf(pg); err != nil {
		t.Fatalf("InitLeaf: %v", err)
	}
	return pg
}

// encodeUserRow makes a realistic row payload exercising the M2 codec.
func encodeUserRow(t *testing.T, id int64, name string, active bool) []byte {
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

func TestInitLeafZeroesBody(t *testing.T) {
	pg := newEmptyLeaf(t)
	if CellCount(pg) != 0 {
		t.Errorf("CellCount = %d, want 0", CellCount(pg))
	}
	if FreeBytes(pg) != storage.PageSize-HeaderSize {
		t.Errorf("FreeBytes = %d, want %d", FreeBytes(pg), storage.PageSize-HeaderSize)
	}
	if err := Validate(pg); err != nil {
		t.Errorf("Validate: %v", err)
	}
	// Body should be zero past the header.
	for i := HeaderSize; i < storage.PageSize; i++ {
		if pg.Data[i] != 0 {
			t.Fatalf("body byte %d = 0x%02x, want 0", i, pg.Data[i])
		}
	}
}

func TestInitLeafRejectsBadType(t *testing.T) {
	for _, badType := range []storage.PageType{storage.PageTypeInvalid, storage.PageTypeFree, storage.PageTypeTableInternal} {
		pg := &storage.Page{ID: 1}
		pg.Data[0] = byte(badType)
		if err := InitLeaf(pg); !errors.Is(err, ErrNotLeaf) {
			t.Errorf("type %d: err = %v, want ErrNotLeaf", badType, err)
		}
	}
}

func TestInsertGetRoundTrip(t *testing.T) {
	pg := newEmptyLeaf(t)
	want := map[uint64][]byte{
		1: encodeUserRow(t, 1, "Felipe", true),
		2: encodeUserRow(t, 2, "MG", true),
		3: encodeUserRow(t, 3, "Jane", false),
	}
	for k, v := range want {
		if err := InsertCell(pg, k, v); err != nil {
			t.Fatalf("InsertCell(%d): %v", k, err)
		}
	}
	for k, v := range want {
		got, found, err := GetCell(pg, k)
		if err != nil || !found {
			t.Fatalf("GetCell(%d): found=%v err=%v", k, found, err)
		}
		if !bytes.Equal(got, v) {
			t.Errorf("GetCell(%d) = %x, want %x", k, got, v)
		}
	}
	if _, found, _ := GetCell(pg, 999); found {
		t.Errorf("GetCell(999): expected not found")
	}
}

func TestInsertKeepsKeysSorted(t *testing.T) {
	pg := newEmptyLeaf(t)
	insertOrder := []uint64{50, 10, 90, 30, 70, 20, 80, 40, 60}
	for _, k := range insertOrder {
		if err := InsertCell(pg, k, []byte{byte(k)}); err != nil {
			t.Fatalf("InsertCell(%d): %v", k, err)
		}
	}
	var got []uint64
	if err := IterateCells(pg, func(k uint64, payload []byte) error {
		got = append(got, k)
		return nil
	}); err != nil {
		t.Fatalf("IterateCells: %v", err)
	}
	want := []uint64{10, 20, 30, 40, 50, 60, 70, 80, 90}
	if len(got) != len(want) {
		t.Fatalf("iterated %d keys, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("iterated[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestInsertRejectsDuplicateKey(t *testing.T) {
	pg := newEmptyLeaf(t)
	if err := InsertCell(pg, 42, []byte("first")); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	freeBefore := FreeBytes(pg)
	countBefore := CellCount(pg)

	err := InsertCell(pg, 42, []byte("second"))
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("err = %v, want ErrDuplicateKey", err)
	}
	if FreeBytes(pg) != freeBefore {
		t.Errorf("FreeBytes changed: %d -> %d", freeBefore, FreeBytes(pg))
	}
	if CellCount(pg) != countBefore {
		t.Errorf("CellCount changed: %d -> %d", countBefore, CellCount(pg))
	}

	got, found, err := GetCell(pg, 42)
	if err != nil || !found {
		t.Fatalf("GetCell(42): found=%v err=%v", found, err)
	}
	if !bytes.Equal(got, []byte("first")) {
		t.Errorf("payload = %q, want %q (original payload should be preserved)", got, "first")
	}
}

func TestInsertReportsPageFullAndLeavesPageIntact(t *testing.T) {
	pg := newEmptyLeaf(t)
	// Use ~500-byte payloads so we hit "full" in a small handful of inserts.
	payload := bytes.Repeat([]byte{0xAB}, 500)
	var k uint64
	var inserted []uint64
	for {
		k++
		if err := InsertCell(pg, k, payload); err != nil {
			if !errors.Is(err, ErrPageFull) {
				t.Fatalf("InsertCell(%d): err = %v, want ErrPageFull", k, err)
			}
			break
		}
		inserted = append(inserted, k)
		if len(inserted) > 100 {
			t.Fatalf("expected ErrPageFull eventually; inserted %d cells", len(inserted))
		}
	}
	if len(inserted) == 0 {
		t.Fatal("could not insert any cells before full")
	}
	if err := Validate(pg); err != nil {
		t.Errorf("Validate after page-full: %v", err)
	}
	// All previously-inserted cells should still be retrievable.
	for _, k := range inserted {
		got, found, err := GetCell(pg, k)
		if err != nil || !found {
			t.Errorf("GetCell(%d) after page-full: found=%v err=%v", k, found, err)
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("GetCell(%d) payload corrupted", k)
		}
	}
}

func TestInsertRejectsOversizedCell(t *testing.T) {
	pg := newEmptyLeaf(t)
	// A payload of exactly PageSize is far too big for any cell.
	huge := make([]byte, storage.PageSize)
	err := InsertCell(pg, 1, huge)
	if !errors.Is(err, ErrCellTooLarge) {
		t.Fatalf("err = %v, want ErrCellTooLarge", err)
	}
}

func TestFreeBytesShrinksOnInsert(t *testing.T) {
	pg := newEmptyLeaf(t)
	prev := FreeBytes(pg)
	for k := uint64(1); k <= 20; k++ {
		payload := bytes.Repeat([]byte{byte(k)}, 50)
		if err := InsertCell(pg, k, payload); err != nil {
			t.Fatalf("InsertCell(%d): %v", k, err)
		}
		cur := FreeBytes(pg)
		if cur >= prev {
			t.Errorf("FreeBytes did not decrease at key %d: %d -> %d", k, prev, cur)
		}
		prev = cur
	}
}

func TestValidateAfterEveryRandomInsert(t *testing.T) {
	pg := newEmptyLeaf(t)
	rng := rand.New(rand.NewSource(0xC0FFEE))
	seen := make(map[uint64]bool)
	payload := bytes.Repeat([]byte{0x42}, 8)
	const target = 200

	var inserted int
	for attempt := 0; attempt < 4000 && inserted < target; attempt++ {
		k := uint64(rng.Int63n(1_000_000)) + 1
		if seen[k] {
			continue
		}
		err := InsertCell(pg, k, payload)
		switch {
		case errors.Is(err, ErrPageFull):
			// 200 8-byte cells should fit in 4KB easily; if not, the cell
			// math is wrong.
			t.Fatalf("ErrPageFull after only %d inserts", inserted)
		case err != nil:
			t.Fatalf("InsertCell(%d): %v", k, err)
		}
		seen[k] = true
		inserted++
		if err := Validate(pg); err != nil {
			t.Fatalf("Validate after insert #%d (key=%d): %v", inserted, k, err)
		}
	}
	if inserted < target {
		t.Fatalf("only inserted %d/%d unique keys after 4000 attempts", inserted, target)
	}

	// Iteration should yield exactly the inserted keys, in order.
	var got []uint64
	if err := IterateCells(pg, func(k uint64, _ []byte) error {
		got = append(got, k)
		return nil
	}); err != nil {
		t.Fatalf("IterateCells: %v", err)
	}
	if len(got) != target {
		t.Fatalf("iterated %d keys, want %d", len(got), target)
	}
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("keys not sorted at %d: %d then %d", i, got[i-1], got[i])
		}
		if !seen[got[i]] {
			t.Fatalf("iterated key %d not in inserted set", got[i])
		}
	}
}

func TestPersistAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "leaf.godb")

	p, err := storage.OpenPager(path, storage.PagerOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("OpenPager: %v", err)
	}
	pg, err := p.AllocatePage(storage.PageTypeTableLeaf)
	if err != nil {
		t.Fatalf("AllocatePage: %v", err)
	}
	if err := InitLeaf(pg); err != nil {
		t.Fatalf("InitLeaf: %v", err)
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
		payload := encodeUserRow(t, r.id, r.name, r.active)
		if err := InsertCell(pg, uint64(r.id), payload); err != nil {
			t.Fatalf("InsertCell(%d): %v", r.id, err)
		}
	}
	if err := p.WritePage(pg); err != nil {
		t.Fatalf("WritePage: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen and read back.
	p2, err := storage.OpenPager(path, storage.PagerOptions{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = p2.Close() })

	pg2, err := p2.ReadPage(pg.ID)
	if err != nil {
		t.Fatalf("ReadPage: %v", err)
	}
	if err := Validate(pg2); err != nil {
		t.Fatalf("Validate after reopen: %v", err)
	}
	if CellCount(pg2) != len(rows) {
		t.Fatalf("CellCount = %d, want %d", CellCount(pg2), len(rows))
	}

	got := make([]string, 0, len(rows))
	if err := IterateCells(pg2, func(k uint64, payload []byte) error {
		values, n, err := record.DecodeRow(payload)
		if err != nil {
			return err
		}
		if n != len(payload) {
			t.Errorf("DecodeRow consumed %d/%d bytes", n, len(payload))
		}
		got = append(got, values[1].Text) // name column
		return nil
	}); err != nil {
		t.Fatalf("IterateCells: %v", err)
	}
	want := []string{"Felipe", "MG", "Jane"}
	if len(got) != len(want) {
		t.Fatalf("got %d names, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCellDirectoryDoesNotOverlapPayloads(t *testing.T) {
	pg := newEmptyLeaf(t)
	for k := uint64(1); k <= 30; k++ {
		payload := bytes.Repeat([]byte{byte(k)}, 32)
		if err := InsertCell(pg, k, payload); err != nil {
			t.Fatalf("InsertCell(%d): %v", k, err)
		}
		h := ReadHeader(pg)
		if h.CellDirEnd > h.FreeSpaceOffset {
			t.Fatalf("after key %d: cell dir end %d overlaps free space offset %d",
				k, h.CellDirEnd, h.FreeSpaceOffset)
		}
	}
}

func TestIterateStopsOnError(t *testing.T) {
	pg := newEmptyLeaf(t)
	for k := uint64(1); k <= 5; k++ {
		if err := InsertCell(pg, k, []byte("x")); err != nil {
			t.Fatalf("InsertCell(%d): %v", k, err)
		}
	}
	sentinel := errors.New("stop")
	var seen []uint64
	err := IterateCells(pg, func(k uint64, _ []byte) error {
		seen = append(seen, k)
		if k == 3 {
			return sentinel
		}
		return nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
	if !equalUints(seen, []uint64{1, 2, 3}) {
		t.Errorf("seen = %v, want [1 2 3]", seen)
	}
}

func equalUints(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

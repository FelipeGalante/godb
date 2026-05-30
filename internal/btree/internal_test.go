package btree

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/felipegalante/godb/internal/storage"
)

// newInternal returns a fresh internal page with a single seed cell
// (childA, separator, childB-as-rightmost). Page id is arbitrary.
func newInternal(t *testing.T, childA storage.PageID, sep uint64, rightmost storage.PageID) *storage.Page {
	t.Helper()
	pg := &storage.Page{ID: 7}
	pg.Data[0] = byte(storage.PageTypeTableInternal)
	if err := InitInternal(pg, childA, sep, rightmost); err != nil {
		t.Fatalf("InitInternal: %v", err)
	}
	return pg
}

func TestInitInternalProducesValidPage(t *testing.T) {
	pg := newInternal(t, 11, 100, 22)
	if err := ValidateInternal(pg); err != nil {
		t.Fatalf("ValidateInternal: %v", err)
	}
	if got := InternalCellCount(pg); got != 1 {
		t.Errorf("InternalCellCount = %d, want 1", got)
	}
	rm, err := RightmostChild(pg)
	if err != nil {
		t.Fatalf("RightmostChild: %v", err)
	}
	if rm != 22 {
		t.Errorf("RightmostChild = %d, want 22", rm)
	}
	// Iterate the single seed cell.
	var seen []struct {
		child storage.PageID
		sep   uint64
	}
	if err := IterateInternalCells(pg, func(child storage.PageID, sep uint64) error {
		seen = append(seen, struct {
			child storage.PageID
			sep   uint64
		}{child, sep})
		return nil
	}); err != nil {
		t.Fatalf("IterateInternalCells: %v", err)
	}
	if len(seen) != 1 || seen[0].child != 11 || seen[0].sep != 100 {
		t.Errorf("seen = %+v, want [{11 100}]", seen)
	}
}

func TestInitInternalRejectsLeafPageType(t *testing.T) {
	pg := &storage.Page{ID: 1}
	pg.Data[0] = byte(storage.PageTypeTableLeaf)
	if err := InitInternal(pg, 1, 10, 2); !errors.Is(err, ErrNotLeaf) {
		t.Errorf("err = %v, want ErrNotLeaf", err)
	}
}

func TestInsertInternalCellKeepsSorted(t *testing.T) {
	pg := newInternal(t, 100, 50, 999) // start with sep=50, child=100, rightmost=999
	inserts := []struct {
		child storage.PageID
		sep   uint64
	}{
		{200, 20},
		{300, 80},
		{400, 35},
		{500, 65},
	}
	for _, in := range inserts {
		if err := InsertInternalCell(pg, in.child, in.sep); err != nil {
			t.Fatalf("InsertInternalCell(%d, %d): %v", in.child, in.sep, err)
		}
	}
	var seps []uint64
	if err := IterateInternalCells(pg, func(_ storage.PageID, sep uint64) error {
		seps = append(seps, sep)
		return nil
	}); err != nil {
		t.Fatalf("IterateInternalCells: %v", err)
	}
	want := []uint64{20, 35, 50, 65, 80}
	if len(seps) != len(want) {
		t.Fatalf("iterated %d separators, want %d", len(seps), len(want))
	}
	for i := range want {
		if seps[i] != want[i] {
			t.Errorf("seps[%d] = %d, want %d", i, seps[i], want[i])
		}
	}
	if err := ValidateInternal(pg); err != nil {
		t.Errorf("ValidateInternal after inserts: %v", err)
	}
}

func TestFindChildDescendsCorrectly(t *testing.T) {
	// Build an internal page with separators [10, 20, 30] mapping to
	// children [A=101, B=102, C=103] and rightmost R=199.
	pg := newInternal(t, 101, 10, 199) // seed: (101, 10), rightmost=199
	// Add (102, 20) and (103, 30).
	if err := InsertInternalCell(pg, 102, 20); err != nil {
		t.Fatalf("insert (102, 20): %v", err)
	}
	if err := InsertInternalCell(pg, 103, 30); err != nil {
		t.Fatalf("insert (103, 30): %v", err)
	}

	cases := []struct {
		key  uint64
		want storage.PageID
		note string
	}{
		{5, 101, "key < first separator -> first child"},
		{10, 102, "key == first separator -> right of it (child indexed by next sep)"},
		{15, 102, "key in (first, second) -> child at second"},
		{20, 103, "key == second separator -> right of it"},
		{25, 103, "key in (second, third) -> child at third"},
		{30, 199, "key == third separator -> rightmost"},
		{35, 199, "key > last separator -> rightmost"},
	}
	for _, tc := range cases {
		got, err := FindChild(pg, tc.key)
		if err != nil {
			t.Fatalf("FindChild(%d): %v", tc.key, err)
		}
		if got != tc.want {
			t.Errorf("FindChild(%d) = %d, want %d (%s)", tc.key, got, tc.want, tc.note)
		}
	}
}

func TestInsertInternalCellReportsPageFull(t *testing.T) {
	pg := newInternal(t, 1, 0, 999) // seed
	// Insert many distinct separators until full.
	var inserted int
	for sep := uint64(1); sep < 10_000; sep++ {
		err := InsertInternalCell(pg, storage.PageID(sep+100), sep)
		if err != nil {
			if !errors.Is(err, ErrPageFull) {
				t.Fatalf("InsertInternalCell(%d): %v", sep, err)
			}
			break
		}
		inserted++
		if inserted > 5000 {
			t.Fatalf("expected ErrPageFull eventually; inserted %d", inserted)
		}
	}
	if inserted == 0 {
		t.Fatal("no internal cells inserted before page full")
	}
	if err := ValidateInternal(pg); err != nil {
		t.Errorf("ValidateInternal after page full: %v", err)
	}
}

func TestInsertInternalCellRejectsDuplicateSeparator(t *testing.T) {
	pg := newInternal(t, 100, 42, 999)
	err := InsertInternalCell(pg, 200, 42)
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("err = %v, want ErrDuplicateKey", err)
	}
	// Original cell intact.
	if err := ValidateInternal(pg); err != nil {
		t.Errorf("Validate after dup reject: %v", err)
	}
}

func TestSetRightmostChildRoundTrip(t *testing.T) {
	pg := newInternal(t, 1, 10, 99)
	if err := SetRightmostChild(pg, 12345); err != nil {
		t.Fatalf("SetRightmostChild: %v", err)
	}
	got, err := RightmostChild(pg)
	if err != nil {
		t.Fatalf("RightmostChild: %v", err)
	}
	if got != 12345 {
		t.Errorf("RightmostChild after set = %d, want 12345", got)
	}
	// Persist via WriteHeader/ReadHeader round trip.
	h := ReadHeader(pg)
	WriteHeader(pg, h)
	got2, _ := RightmostChild(pg)
	if got2 != 12345 {
		t.Errorf("RightmostChild after header round trip = %d, want 12345", got2)
	}
}

func TestValidateInternalRejectsUnsortedSeparators(t *testing.T) {
	pg := newInternal(t, 1, 10, 99)
	if err := InsertInternalCell(pg, 2, 20); err != nil {
		t.Fatalf("InsertInternalCell: %v", err)
	}
	// Manually swap the two directory slots so separators are
	// out of order in the directory while the cell payloads stay valid.
	slot0 := binary.BigEndian.Uint16(pg.Data[HeaderSize : HeaderSize+slotSize])
	slot1 := binary.BigEndian.Uint16(pg.Data[HeaderSize+slotSize : HeaderSize+2*slotSize])
	binary.BigEndian.PutUint16(pg.Data[HeaderSize:HeaderSize+slotSize], slot1)
	binary.BigEndian.PutUint16(pg.Data[HeaderSize+slotSize:HeaderSize+2*slotSize], slot0)

	err := ValidateInternal(pg)
	if err == nil {
		t.Fatalf("ValidateInternal did not detect unsorted separators")
	}
	var ce *storage.CorruptionError
	if !errors.As(err, &ce) {
		t.Errorf("err type = %T, want *storage.CorruptionError; err=%v", err, err)
	}
}

func TestInternalCellCodecRoundTrip(t *testing.T) {
	// Pure codec test — make sure writeInternalCell/readInternalCell
	// and the separator-only reader agree.
	var buf [64]byte
	n, err := writeInternalCell(buf[:], 0xDEADBEEF, 12345)
	if err != nil {
		t.Fatalf("writeInternalCell: %v", err)
	}
	if n != internalCellSize(12345) {
		t.Errorf("writeInternalCell wrote %d, internalCellSize says %d", n, internalCellSize(12345))
	}
	gotChild, gotSep, gotN, err := readInternalCell(buf[:n])
	if err != nil {
		t.Fatalf("readInternalCell: %v", err)
	}
	if gotChild != 0xDEADBEEF || gotSep != 12345 || gotN != n {
		t.Errorf("readInternalCell = (%d, %d, %d), want (%d, %d, %d)",
			gotChild, gotSep, gotN, storage.PageID(0xDEADBEEF), uint64(12345), n)
	}
	sepOnly, sepN, err := readInternalCellSeparator(buf[:n])
	if err != nil {
		t.Fatalf("readInternalCellSeparator: %v", err)
	}
	if sepOnly != 12345 || sepN != n {
		t.Errorf("readInternalCellSeparator = (%d, %d), want (12345, %d)", sepOnly, sepN, n)
	}
	// writeInternalCell into too-small buffer should error.
	if _, err := writeInternalCell(buf[:1], 1, 1); err == nil {
		t.Errorf("writeInternalCell into short buf did not error")
	}
}

func TestLeafRightSiblingRoundTrip(t *testing.T) {
	pg := &storage.Page{ID: 1}
	pg.Data[0] = byte(storage.PageTypeTableLeaf)
	if err := InitLeaf(pg); err != nil {
		t.Fatalf("InitLeaf: %v", err)
	}
	sib, err := RightSibling(pg)
	if err != nil {
		t.Fatalf("RightSibling: %v", err)
	}
	if sib != 0 {
		t.Errorf("fresh RightSibling = %d, want 0", sib)
	}
	if err := SetRightSibling(pg, 42); err != nil {
		t.Fatalf("SetRightSibling: %v", err)
	}
	sib2, _ := RightSibling(pg)
	if sib2 != 42 {
		t.Errorf("after Set, RightSibling = %d, want 42", sib2)
	}
	// Verify other leaf state untouched.
	if err := Validate(pg); err != nil {
		t.Errorf("leaf.Validate after SetRightSibling: %v", err)
	}
}


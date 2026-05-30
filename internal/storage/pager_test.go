package storage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func tmpDBPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.godb")
}

func TestOpenCreatesDatabaseFile(t *testing.T) {
	path := tmpDBPath(t)
	p, err := OpenPager(path, PagerOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("OpenPager: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != PageSize {
		t.Fatalf("new file size = %d, want %d", info.Size(), PageSize)
	}

	h := p.Header()
	if h.PageSize != PageSize {
		t.Errorf("Header.PageSize = %d, want %d", h.PageSize, PageSize)
	}
	if h.PageCount != 1 {
		t.Errorf("Header.PageCount = %d, want 1", h.PageCount)
	}
	if h.FormatMajor != FormatMajor || h.FormatMinor != FormatMinor {
		t.Errorf("version = %d.%d, want %d.%d", h.FormatMajor, h.FormatMinor, FormatMajor, FormatMinor)
	}
	if p.PageCount() != 1 {
		t.Errorf("PageCount() = %d, want 1", p.PageCount())
	}
}

func TestOpenRejectsMissingFileWithoutCreate(t *testing.T) {
	path := tmpDBPath(t)
	_, err := OpenPager(path, PagerOptions{CreateIfMissing: false})
	if err == nil {
		t.Fatalf("expected error opening missing file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want wraps os.ErrNotExist", err)
	}
}

func TestOpenRejectsInvalidMagic(t *testing.T) {
	path := tmpDBPath(t)
	garbage := make([]byte, PageSize)
	copy(garbage, []byte("XXXX"))
	if err := os.WriteFile(path, garbage, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := OpenPager(path, PagerOptions{CreateIfMissing: true})
	if !errors.Is(err, ErrInvalidMagic) {
		t.Fatalf("err = %v, want ErrInvalidMagic", err)
	}
}

func TestOpenRejectsTruncatedFile(t *testing.T) {
	path := tmpDBPath(t)
	if err := os.WriteFile(path, []byte("GODB"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := OpenPager(path, PagerOptions{CreateIfMissing: true})
	if !errors.Is(err, ErrTruncatedFile) {
		t.Fatalf("err = %v, want ErrTruncatedFile", err)
	}
}

func TestOpenRejectsUnsupportedVersion(t *testing.T) {
	path := tmpDBPath(t)
	buf := make([]byte, PageSize)
	h := NewHeader()
	if err := h.Encode(buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// Bump major version directly in the encoded bytes.
	binary.BigEndian.PutUint16(buf[4:6], FormatMajor+1)
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := OpenPager(path, PagerOptions{CreateIfMissing: true})
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("err = %v, want ErrUnsupportedVersion", err)
	}
}

func TestReadWritePageRoundTrip(t *testing.T) {
	path := tmpDBPath(t)
	p, err := OpenPager(path, PagerOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("OpenPager: %v", err)
	}

	pg, err := p.AllocatePage(PageTypeTableLeaf)
	if err != nil {
		t.Fatalf("AllocatePage: %v", err)
	}
	wantID := PageID(1)
	if pg.ID != wantID {
		t.Fatalf("pg.ID = %d, want %d", pg.ID, wantID)
	}

	// Write a marker payload (skip byte 0 which carries the type tag).
	marker := []byte("hello world from page 1")
	copy(pg.Data[1:1+len(marker)], marker)
	if err := p.WritePage(pg); err != nil {
		t.Fatalf("WritePage: %v", err)
	}
	if pg.Dirty {
		t.Errorf("Page.Dirty = true after WritePage, want false")
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen and read back.
	p2, err := OpenPager(path, PagerOptions{CreateIfMissing: false})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = p2.Close() })

	got, err := p2.ReadPage(wantID)
	if err != nil {
		t.Fatalf("ReadPage: %v", err)
	}
	if got.Data[0] != byte(PageTypeTableLeaf) {
		t.Errorf("type byte = %d, want %d", got.Data[0], PageTypeTableLeaf)
	}
	if !bytes.Equal(got.Data[1:1+len(marker)], marker) {
		t.Errorf("marker bytes do not round-trip: got %q", got.Data[1:1+len(marker)])
	}
}

func TestAllocatePageIncrementsPageCount(t *testing.T) {
	path := tmpDBPath(t)
	p, err := OpenPager(path, PagerOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("OpenPager: %v", err)
	}

	const N = 10
	ids := make([]PageID, 0, N)
	for i := 0; i < N; i++ {
		pg, err := p.AllocatePage(PageTypeTableLeaf)
		if err != nil {
			t.Fatalf("AllocatePage(%d): %v", i, err)
		}
		ids = append(ids, pg.ID)
	}

	// IDs should be 1..N (page 0 is the header).
	for i, id := range ids {
		want := PageID(i + 1)
		if id != want {
			t.Errorf("ids[%d] = %d, want %d", i, id, want)
		}
	}
	if got := p.PageCount(); got != uint64(N+1) {
		t.Errorf("PageCount() = %d, want %d", got, N+1)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen and confirm the count persisted.
	p2, err := OpenPager(path, PagerOptions{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = p2.Close() })
	if got := p2.PageCount(); got != uint64(N+1) {
		t.Errorf("PageCount() after reopen = %d, want %d", got, N+1)
	}
}

func TestHeaderSurvivesReopen(t *testing.T) {
	path := tmpDBPath(t)
	p, err := OpenPager(path, PagerOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("OpenPager: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := p.AllocatePage(PageTypeTableLeaf); err != nil {
			t.Fatalf("AllocatePage: %v", err)
		}
	}
	want := p.Header()
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	p2, err := OpenPager(path, PagerOptions{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = p2.Close() })

	got := p2.Header()
	if got != want {
		t.Errorf("header after reopen = %+v, want %+v", got, want)
	}
}

func TestReadPageOutOfRangeReturnsError(t *testing.T) {
	path := tmpDBPath(t)
	p, err := OpenPager(path, PagerOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("OpenPager: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	if _, err := p.ReadPage(99); !errors.Is(err, ErrPageOutOfRange) {
		t.Errorf("ReadPage(99) err = %v, want ErrPageOutOfRange", err)
	}
}

func TestWritePageOutOfRangeReturnsError(t *testing.T) {
	path := tmpDBPath(t)
	p, err := OpenPager(path, PagerOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("OpenPager: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	pg := &Page{ID: 42}
	if err := p.WritePage(pg); !errors.Is(err, ErrPageOutOfRange) {
		t.Errorf("WritePage(42) err = %v, want ErrPageOutOfRange", err)
	}
}

func TestSyncDurabilityBasic(t *testing.T) {
	path := tmpDBPath(t)
	p, err := OpenPager(path, PagerOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("OpenPager: %v", err)
	}
	pg, err := p.AllocatePage(PageTypeTableLeaf)
	if err != nil {
		t.Fatalf("AllocatePage: %v", err)
	}
	copy(pg.Data[1:], []byte("durable"))
	if err := p.WritePage(pg); err != nil {
		t.Fatalf("WritePage: %v", err)
	}
	if err := p.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	// Drop the pager handle without Close (simulate process death after Sync).
	// We can't kill the process inside a unit test, but reopening another
	// handle exercises the same crash-safe code path.
	p2, err := OpenPager(path, PagerOptions{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = p2.Close() })

	got, err := p2.ReadPage(1)
	if err != nil {
		t.Fatalf("ReadPage: %v", err)
	}
	if string(got.Data[1:1+len("durable")]) != "durable" {
		t.Errorf("durable marker missing after sync+reopen")
	}
	_ = p.Close()
}

func TestCloseIsIdempotent(t *testing.T) {
	path := tmpDBPath(t)
	p, err := OpenPager(path, PagerOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("OpenPager: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestOpsAfterCloseReturnError(t *testing.T) {
	path := tmpDBPath(t)
	p, err := OpenPager(path, PagerOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("OpenPager: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := p.ReadPage(0); !errors.Is(err, ErrClosed) {
		t.Errorf("ReadPage after close: err = %v, want ErrClosed", err)
	}
	if _, err := p.AllocatePage(PageTypeTableLeaf); !errors.Is(err, ErrClosed) {
		t.Errorf("AllocatePage after close: err = %v, want ErrClosed", err)
	}
	if err := p.Sync(); !errors.Is(err, ErrClosed) {
		t.Errorf("Sync after close: err = %v, want ErrClosed", err)
	}
}

func TestHeaderEncodeDecodeRoundTrip(t *testing.T) {
	h := &Header{
		FormatMajor:       FormatMajor,
		FormatMinor:       FormatMinor,
		PageSize:          PageSize,
		PageCount:         42,
		CatalogRootPageID: 1,
		FreelistHeadPage:  7,
		ChangeCounter:     123,
		LastTxnID:         9999,
		ChecksumAlgo:      0,
		Flags:             0,
	}
	var buf [PageSize]byte
	if err := h.Encode(buf[:]); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := DecodeHeader(buf[:])
	if err != nil {
		t.Fatalf("DecodeHeader: %v", err)
	}
	if *got != *h {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", *got, *h)
	}
}

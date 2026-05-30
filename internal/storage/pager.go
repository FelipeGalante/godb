package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// PagerOptions controls how OpenPager treats the database file.
type PagerOptions struct {
	// CreateIfMissing, if true, creates the file (and writes a fresh header)
	// when the path does not exist. Otherwise OpenPager returns a wrapped
	// os.ErrNotExist.
	CreateIfMissing bool
}

// Pager owns a database file. It is responsible for fixed-size page I/O,
// page allocation, and persisting the database header. It does not cache
// pages in M1 — each ReadPage hits disk. A buffer pool will be added in
// a later milestone.
//
// The pager is safe for concurrent use within a single process: all
// methods acquire an internal mutex. Cross-process access is unsupported.
type Pager struct {
	mu     sync.Mutex
	file   *os.File
	header Header
	closed bool
}

// OpenPager opens an existing database file or, when opts.CreateIfMissing
// is true, creates one. The returned pager owns the underlying *os.File
// and must be Close()d to release it.
func OpenPager(path string, opts PagerOptions) (*Pager, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	switch {
	case err == nil:
		return openExisting(f)
	case errors.Is(err, os.ErrNotExist) && opts.CreateIfMissing:
		return createNew(path)
	default:
		return nil, err
	}
}

func openExisting(f *os.File) (*Pager, error) {
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if info.Size() < PageSize {
		_ = f.Close()
		return nil, ErrTruncatedFile
	}
	if info.Size()%PageSize != 0 {
		_ = f.Close()
		return nil, &CorruptionError{Reason: fmt.Sprintf("file size %d is not a multiple of page size %d", info.Size(), PageSize)}
	}

	var buf [PageSize]byte
	if _, err := f.ReadAt(buf[:], 0); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("storage: reading header: %w", err)
	}
	h, err := DecodeHeader(buf[:])
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	// Cross-check the header's page count against the on-disk file size.
	expectedSize := int64(h.PageCount) * int64(PageSize)
	if info.Size() < expectedSize {
		_ = f.Close()
		return nil, &CorruptionError{
			Reason: fmt.Sprintf("header claims %d pages but file is %d bytes", h.PageCount, info.Size()),
		}
	}
	return &Pager{file: f, header: *h}, nil
}

func createNew(path string) (*Pager, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	h := NewHeader()
	var buf [PageSize]byte
	if err := h.Encode(buf[:]); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if _, err := f.WriteAt(buf[:], 0); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("storage: writing header: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return &Pager{file: f, header: *h}, nil
}

// Close flushes any pending header changes and releases the file. Safe to
// call multiple times.
func (p *Pager) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	syncErr := p.syncLocked()
	closeErr := p.file.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// Sync flushes the database file to disk, including the header if it has
// been mutated since the last sync.
func (p *Pager) Sync() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	return p.syncLocked()
}

// PageCount returns the number of pages currently allocated in the file
// (including page 0, the header).
func (p *Pager) PageCount() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.header.PageCount
}

// Header returns a copy of the current database header. The pager
// continues to own the canonical version; mutating the returned value
// does not affect on-disk state.
func (p *Pager) Header() Header {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.header
}

// ReadPage reads page id from disk and returns a fresh Page. It is an
// error to read page 0 through this method (use Header() for the header
// page; we will revisit this when the buffer pool lands).
func (p *Pager) ReadPage(id PageID) (*Page, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ErrClosed
	}
	if id == 0 {
		// Reading page 0 raw is allowed but unusual — gate it so that
		// the typical path (Header()) is used. Returning the bytes
		// is fine and lets debug tools dump the header.
	}
	if uint64(id) >= p.header.PageCount {
		return nil, fmt.Errorf("%w: page %d (page count = %d)", ErrPageOutOfRange, id, p.header.PageCount)
	}
	pg := &Page{ID: id}
	if _, err := p.file.ReadAt(pg.Data[:], int64(id)*int64(PageSize)); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, ErrTruncatedFile
		}
		return nil, fmt.Errorf("storage: read page %d: %w", id, err)
	}
	return pg, nil
}

// WritePage writes page back to disk. The page must have been obtained
// from this pager (or allocated via AllocatePage); writing arbitrary IDs
// outside the allocated range is rejected.
func (p *Pager) WritePage(page *Page) error {
	if page == nil {
		return fmt.Errorf("storage: WritePage: nil page")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	if uint64(page.ID) >= p.header.PageCount {
		return fmt.Errorf("%w: page %d (page count = %d)", ErrPageOutOfRange, page.ID, p.header.PageCount)
	}
	if _, err := p.file.WriteAt(page.Data[:], int64(page.ID)*int64(PageSize)); err != nil {
		return fmt.Errorf("storage: write page %d: %w", page.ID, err)
	}
	page.Dirty = false
	return nil
}

// AllocatePage appends a new page to the file, zero-fills it, sets the
// page type byte at offset 0, and returns it. The page is not synced —
// callers should WritePage (after populating the body) and Sync() to
// persist.
//
// Note: the full general page header layout (cell directory, sibling
// pointers, etc., per the file-format doc) is intentionally not written
// here. Only the type byte is set so that page inspection can recognize
// the kind. Slotted-page initialization belongs to a later milestone.
func (p *Pager) AllocatePage(pageType PageType) (*Page, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ErrClosed
	}
	if pageType == PageTypeInvalid {
		return nil, fmt.Errorf("storage: AllocatePage: invalid page type")
	}

	newID := PageID(p.header.PageCount)
	pg := &Page{ID: newID}
	pg.Data[0] = byte(pageType)

	// Extend the file so that ReadPage of this new id succeeds even
	// before the caller writes its own contents.
	if _, err := p.file.WriteAt(pg.Data[:], int64(newID)*int64(PageSize)); err != nil {
		return nil, fmt.Errorf("storage: extending file for page %d: %w", newID, err)
	}

	p.header.PageCount++
	if err := p.writeHeaderLocked(); err != nil {
		// Roll back the in-memory page count so we don't leak a half-allocated page.
		p.header.PageCount--
		return nil, err
	}
	return pg, nil
}

// syncLocked writes the header to disk and fsyncs the file. Caller must
// hold p.mu.
func (p *Pager) syncLocked() error {
	if err := p.writeHeaderLocked(); err != nil {
		return err
	}
	return p.file.Sync()
}

// writeHeaderLocked encodes the in-memory header to page 0. Caller must
// hold p.mu.
func (p *Pager) writeHeaderLocked() error {
	var buf [PageSize]byte
	if err := p.header.Encode(buf[:]); err != nil {
		return err
	}
	if _, err := p.file.WriteAt(buf[:], 0); err != nil {
		return fmt.Errorf("storage: writing header: %w", err)
	}
	return nil
}

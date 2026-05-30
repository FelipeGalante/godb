package storage

import (
	"encoding/binary"
	"fmt"
)

// Magic is the four-byte identifier at the start of every .godb file.
var Magic = [4]byte{'G', 'O', 'D', 'B'}

// Format version of the database file. Bumped when the on-disk layout
// changes in a way that older binaries cannot read.
const (
	FormatMajor uint16 = 0
	FormatMinor uint16 = 1
)

// Header is the in-memory view of the database header page (page 0).
// Layout on disk (big-endian, all offsets in bytes):
//
//	0   [4]  Magic "GODB"
//	4   u16  Format major version
//	6   u16  Format minor version
//	8   u32  Page size
//	12  u64  Page count
//	20  u64  Catalog root page id     — 0 until milestone 6
//	28  u64  Freelist head page id    — 0 until v0.2
//	36  u64  Database change counter
//	44  u64  Last transaction id
//	52  u32  Checksum algorithm id    — 0 = none
//	56  u32  Reserved flags
//	60+ ...  Reserved (zeroed)
type Header struct {
	FormatMajor       uint16
	FormatMinor       uint16
	PageSize          uint32
	PageCount         uint64
	CatalogRootPageID PageID
	FreelistHeadPage  PageID
	ChangeCounter     uint64
	LastTxnID         uint64
	ChecksumAlgo      uint32
	Flags             uint32
}

// NewHeader returns a Header populated with v0.1 defaults for a brand-new
// database file. PageCount is 1 because page 0 (the header itself) is
// always allocated.
func NewHeader() *Header {
	return &Header{
		FormatMajor: FormatMajor,
		FormatMinor: FormatMinor,
		PageSize:    PageSize,
		PageCount:   1,
	}
}

// Encode writes the header into buf, which must be at least PageSize bytes.
// Any trailing bytes of the page are zeroed.
func (h *Header) Encode(buf []byte) error {
	if len(buf) < PageSize {
		return fmt.Errorf("storage: encode buffer too small: %d < %d", len(buf), PageSize)
	}
	for i := range buf[:PageSize] {
		buf[i] = 0
	}
	copy(buf[0:4], Magic[:])
	binary.BigEndian.PutUint16(buf[4:6], h.FormatMajor)
	binary.BigEndian.PutUint16(buf[6:8], h.FormatMinor)
	binary.BigEndian.PutUint32(buf[8:12], h.PageSize)
	binary.BigEndian.PutUint64(buf[12:20], h.PageCount)
	binary.BigEndian.PutUint64(buf[20:28], uint64(h.CatalogRootPageID))
	binary.BigEndian.PutUint64(buf[28:36], uint64(h.FreelistHeadPage))
	binary.BigEndian.PutUint64(buf[36:44], h.ChangeCounter)
	binary.BigEndian.PutUint64(buf[44:52], h.LastTxnID)
	binary.BigEndian.PutUint32(buf[52:56], h.ChecksumAlgo)
	binary.BigEndian.PutUint32(buf[56:60], h.Flags)
	return nil
}

// DecodeHeader reads a header from buf. It validates magic, page size, and
// the major version; mismatches return typed errors so callers can react
// appropriately (e.g. refuse to open).
func DecodeHeader(buf []byte) (*Header, error) {
	if len(buf) < PageSize {
		return nil, ErrTruncatedFile
	}
	if [4]byte{buf[0], buf[1], buf[2], buf[3]} != Magic {
		return nil, ErrInvalidMagic
	}
	h := &Header{
		FormatMajor:       binary.BigEndian.Uint16(buf[4:6]),
		FormatMinor:       binary.BigEndian.Uint16(buf[6:8]),
		PageSize:          binary.BigEndian.Uint32(buf[8:12]),
		PageCount:         binary.BigEndian.Uint64(buf[12:20]),
		CatalogRootPageID: PageID(binary.BigEndian.Uint64(buf[20:28])),
		FreelistHeadPage:  PageID(binary.BigEndian.Uint64(buf[28:36])),
		ChangeCounter:     binary.BigEndian.Uint64(buf[36:44]),
		LastTxnID:         binary.BigEndian.Uint64(buf[44:52]),
		ChecksumAlgo:      binary.BigEndian.Uint32(buf[52:56]),
		Flags:             binary.BigEndian.Uint32(buf[56:60]),
	}
	if h.FormatMajor != FormatMajor {
		return nil, fmt.Errorf("%w: file is v%d.%d, this binary supports v%d.x",
			ErrUnsupportedVersion, h.FormatMajor, h.FormatMinor, FormatMajor)
	}
	if h.PageSize != PageSize {
		return nil, fmt.Errorf("%w: file has %d, expected %d",
			ErrPageSizeMismatch, h.PageSize, PageSize)
	}
	if h.PageCount < 1 {
		return nil, &CorruptionError{PageID: 0, Reason: "page count is zero"}
	}
	return h, nil
}

// Package btree provides the slotted-page primitive that table B+tree
// leaves are built on. In M3 only leaf-style operations exist; internal
// pages and the multi-page B+tree arrive in M4+.
package btree

import (
	"encoding/binary"

	"github.com/felipegalante/godb/internal/storage"
)

// HeaderSize is the on-disk size of the per-page header (spec §6.6).
// Cell directory entries begin at this offset.
const HeaderSize = 28

// PageHeader is the decoded form of the per-page header. The encoded
// layout (all multi-byte fields big-endian) is:
//
//	0   u8   Page type            (already set by Pager.AllocatePage)
//	1   u8   Flags                — reserved; 0 in v0.1
//	2   u16  Cell count
//	4   u16  Free space offset    — first free byte in the middle region
//	6   u16  Cell dir end offset  — one past the last directory entry
//	8   u64  Right sibling page   — 0 if none (used in M4/M5)
//	16  u64  Parent page id       — 0 = unknown; debug-only
//	24  u32  Checksum             — 0 in v0.1
//	28+ ...  Body: cell directory + free + cell payloads
type PageHeader struct {
	Type            storage.PageType
	Flags           uint8
	CellCount       uint16
	FreeSpaceOffset uint16
	CellDirEnd      uint16
	RightSibling    storage.PageID
	Parent          storage.PageID
	Checksum        uint32
}

// ReadHeader decodes the header bytes from pg into a PageHeader value.
func ReadHeader(pg *storage.Page) PageHeader {
	return PageHeader{
		Type:            storage.PageType(pg.Data[0]),
		Flags:           pg.Data[1],
		CellCount:       binary.BigEndian.Uint16(pg.Data[2:4]),
		FreeSpaceOffset: binary.BigEndian.Uint16(pg.Data[4:6]),
		CellDirEnd:      binary.BigEndian.Uint16(pg.Data[6:8]),
		RightSibling:    storage.PageID(binary.BigEndian.Uint64(pg.Data[8:16])),
		Parent:          storage.PageID(binary.BigEndian.Uint64(pg.Data[16:24])),
		Checksum:        binary.BigEndian.Uint32(pg.Data[24:28]),
	}
}

// isInternalType returns true for page type bytes that are recognized
// internal nodes (table or index). Mirrors isLeafType in leaf.go.
func isInternalType(t storage.PageType) bool {
	return t == storage.PageTypeTableInternal || t == storage.PageTypeIndexInternal
}

// WriteHeader encodes h into pg's header bytes. It does not touch the
// page body.
func WriteHeader(pg *storage.Page, h PageHeader) {
	pg.Data[0] = byte(h.Type)
	pg.Data[1] = h.Flags
	binary.BigEndian.PutUint16(pg.Data[2:4], h.CellCount)
	binary.BigEndian.PutUint16(pg.Data[4:6], h.FreeSpaceOffset)
	binary.BigEndian.PutUint16(pg.Data[6:8], h.CellDirEnd)
	binary.BigEndian.PutUint64(pg.Data[8:16], uint64(h.RightSibling))
	binary.BigEndian.PutUint64(pg.Data[16:24], uint64(h.Parent))
	binary.BigEndian.PutUint32(pg.Data[24:28], h.Checksum)
}

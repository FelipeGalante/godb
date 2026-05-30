package btree

import (
	"encoding/binary"
	"fmt"

	"github.com/felipegalante/godb/internal/storage"
)

// Two cell formats live in this package.
//
// A table-LEAF cell (spec §10.3):
//
//	[key: uvarint][payload length: uvarint][payload bytes]
//
// A table-INTERNAL cell (spec §10.4):
//
//	[left_child_page_id: uint64 BE][separator_key: uvarint]
//
// Leaves point at row payloads; internal pages point at child pages and
// carry only separator keys. Both formats use the same slotted-page
// directory + free-space scheme — only the cell encoding differs.
// uvarints are LEB128 from encoding/binary.

// cellSize returns the encoded byte size of a cell with the given key
// and payload length.
func cellSize(key uint64, payloadLen int) int {
	return uvarintSize(key) + uvarintSize(uint64(payloadLen)) + payloadLen
}

// uvarintSize returns the number of bytes binary.PutUvarint writes for v.
func uvarintSize(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// writeCell encodes a (key, payload) cell into the start of buf and
// returns the number of bytes written. Returns an error if buf is too
// small.
func writeCell(buf []byte, key uint64, payload []byte) (int, error) {
	need := cellSize(key, len(payload))
	if len(buf) < need {
		return 0, fmt.Errorf("btree: writeCell: need %d bytes, have %d", need, len(buf))
	}
	pos := binary.PutUvarint(buf, key)
	pos += binary.PutUvarint(buf[pos:], uint64(len(payload)))
	pos += copy(buf[pos:], payload)
	return pos, nil
}

// readCellKey decodes only the key prefix of a cell at buf. It is used
// during binary search where reading the full payload is wasteful.
func readCellKey(buf []byte) (key uint64, n int, err error) {
	k, ln := binary.Uvarint(buf)
	if ln <= 0 {
		return 0, 0, fmt.Errorf("btree: readCellKey: truncated key")
	}
	return k, ln, nil
}

// readCell decodes a full cell at buf, returning the key, a slice
// referencing the payload bytes (no copy), and the total bytes consumed.
// The returned payload slice aliases buf — callers that need to retain
// it must copy.
func readCell(buf []byte) (key uint64, payload []byte, n int, err error) {
	k, kn := binary.Uvarint(buf)
	if kn <= 0 {
		return 0, nil, 0, fmt.Errorf("btree: readCell: truncated key")
	}
	length, ln := binary.Uvarint(buf[kn:])
	if ln <= 0 {
		return 0, nil, 0, fmt.Errorf("btree: readCell: truncated length")
	}
	start := kn + ln
	end := start + int(length)
	if end < start || end > len(buf) {
		return 0, nil, 0, fmt.Errorf("btree: readCell: payload truncated (want %d bytes, have %d)", length, len(buf)-start)
	}
	return k, buf[start:end], end, nil
}

// internalCellSize returns the encoded byte size of an internal-page cell
// with the given separator key. The child page id is always 8 bytes; the
// separator varies with its uvarint encoding.
func internalCellSize(separator uint64) int {
	return 8 + uvarintSize(separator)
}

// writeInternalCell encodes (childID, separator) into the start of buf
// and returns the number of bytes written.
func writeInternalCell(buf []byte, childID storage.PageID, separator uint64) (int, error) {
	need := internalCellSize(separator)
	if len(buf) < need {
		return 0, fmt.Errorf("btree: writeInternalCell: need %d bytes, have %d", need, len(buf))
	}
	binary.BigEndian.PutUint64(buf[:8], uint64(childID))
	pos := 8 + binary.PutUvarint(buf[8:], separator)
	return pos, nil
}

// readInternalCellSeparator decodes only the separator key of an internal
// cell at buf. It is used during binary search of an internal page where
// the child id isn't needed for the comparison itself.
func readInternalCellSeparator(buf []byte) (separator uint64, n int, err error) {
	if len(buf) < 8 {
		return 0, 0, fmt.Errorf("btree: readInternalCellSeparator: short cell (need 8 bytes for child id, have %d)", len(buf))
	}
	sep, ln := binary.Uvarint(buf[8:])
	if ln <= 0 {
		return 0, 0, fmt.Errorf("btree: readInternalCellSeparator: truncated separator")
	}
	return sep, 8 + ln, nil
}

// readInternalCell decodes a full internal cell at buf, returning the
// child page id, the separator key, and the total bytes consumed.
func readInternalCell(buf []byte) (childID storage.PageID, separator uint64, n int, err error) {
	if len(buf) < 8 {
		return 0, 0, 0, fmt.Errorf("btree: readInternalCell: short cell (need 8 bytes for child id, have %d)", len(buf))
	}
	childID = storage.PageID(binary.BigEndian.Uint64(buf[:8]))
	sep, ln := binary.Uvarint(buf[8:])
	if ln <= 0 {
		return 0, 0, 0, fmt.Errorf("btree: readInternalCell: truncated separator")
	}
	return childID, sep, 8 + ln, nil
}

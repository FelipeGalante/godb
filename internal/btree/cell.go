package btree

import (
	"encoding/binary"
	"fmt"
)

// A table-leaf cell on disk is:
//
//	[key: uvarint][payload length: uvarint][payload bytes]
//
// uvarints use LEB128 (encoding/binary). Cells live in the bottom of
// the page; the cell directory at the top points to their offsets.

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

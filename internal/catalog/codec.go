package catalog

import (
	"encoding/binary"
	"fmt"
	"unicode/utf8"

	"github.com/felipegalante/godb/internal/record"
	"github.com/felipegalante/godb/internal/storage"
)

// catalogFormatVersion is the leading byte of every encoded catalog
// row. It is bumped only on incompatible on-disk format changes; the
// decoder rejects anything else with ErrUnsupportedCatalogVersion.
const catalogFormatVersion byte = 1

// ObjectType tags the kind of catalog object. Byte values are on-disk
// and must not be reordered.
type ObjectType uint8

const (
	// ObjectTypeTable is the only catalog object kind M6 creates.
	ObjectTypeTable ObjectType = 1

	// ObjectTypeIndex is reserved for v0.2 secondary indexes. M6 does
	// not create or decode these; the constant exists so the on-disk
	// type byte stays stable when v0.2 lands.
	ObjectTypeIndex ObjectType = 2
)

// flag bits packed into the column flags byte.
const (
	colFlagNotNull    uint8 = 1 << 0
	colFlagPrimaryKey uint8 = 1 << 1
)

// Object is the in-memory shape encoded into one catalog row. The
// object ID is the cell key in the catalog's btree; it is NOT encoded
// in the payload (the key holds it).
type Object struct {
	Type       ObjectType
	Name       string
	RootPageID storage.PageID
	SQL        string
	Schema     record.Schema
}

// EncodeObject serializes obj into a fresh byte slice.
//
// Layout (uvarint = LEB128 via encoding/binary):
//
//	[catalog format version: u8 = 1]
//	[object type: u8]
//	[name length: uvarint] [name bytes]
//	[root page id: u64 BE]
//	[sql length: uvarint]  [sql bytes]   (length may be zero)
//	[column count: uvarint]
//	for each column:
//	  [name length: uvarint] [name bytes]
//	  [kind: u8]                          // record.Kind on-disk byte
//	  [flags: u8]                          // bit 0 = NotNull, bit 1 = PrimaryKey
//	  [position: uvarint]
func EncodeObject(obj *Object) ([]byte, error) {
	if obj == nil {
		return nil, fmt.Errorf("catalog.EncodeObject: nil object")
	}
	if obj.Type != ObjectTypeTable && obj.Type != ObjectTypeIndex {
		return nil, fmt.Errorf("%w: 0x%02x", ErrInvalidObjectType, byte(obj.Type))
	}

	// Pre-size optimistically; appends grow the slice as needed.
	buf := make([]byte, 0, 64+len(obj.Name)+len(obj.SQL)+32*len(obj.Schema.Columns))
	buf = append(buf, catalogFormatVersion)
	buf = append(buf, byte(obj.Type))

	buf = appendVarBytes(buf, []byte(obj.Name))

	var rootBuf [8]byte
	binary.BigEndian.PutUint64(rootBuf[:], uint64(obj.RootPageID))
	buf = append(buf, rootBuf[:]...)

	buf = appendVarBytes(buf, []byte(obj.SQL))

	var u [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(u[:], uint64(len(obj.Schema.Columns)))
	buf = append(buf, u[:n]...)

	for _, col := range obj.Schema.Columns {
		buf = appendVarBytes(buf, []byte(col.Name))
		buf = append(buf, byte(col.Kind))
		var flags uint8
		if col.NotNull {
			flags |= colFlagNotNull
		}
		if col.PrimaryKey {
			flags |= colFlagPrimaryKey
		}
		buf = append(buf, flags)
		n := binary.PutUvarint(u[:], uint64(col.Position))
		buf = append(buf, u[:n]...)
	}

	return buf, nil
}

// DecodeObject parses a catalog row produced by EncodeObject. Returns
// ErrUnsupportedCatalogVersion if the leading byte is unexpected
// (which also catches pre-M6 .godb files whose CatalogRootPageID
// pointed at a regular leaf).
func DecodeObject(src []byte) (*Object, error) {
	pos := 0
	if len(src) < 1 {
		return nil, ErrShortBuffer
	}
	if src[0] != catalogFormatVersion {
		return nil, fmt.Errorf("%w: 0x%02x (this binary supports 0x%02x)",
			ErrUnsupportedCatalogVersion, src[0], catalogFormatVersion)
	}
	pos++

	if pos >= len(src) {
		return nil, ErrShortBuffer
	}
	otype := ObjectType(src[pos])
	if otype != ObjectTypeTable && otype != ObjectTypeIndex {
		return nil, fmt.Errorf("%w: 0x%02x", ErrInvalidObjectType, byte(otype))
	}
	pos++

	name, n, err := readVarBytes(src[pos:])
	if err != nil {
		return nil, fmt.Errorf("decode name: %w", err)
	}
	if !utf8.Valid(name) {
		return nil, fmt.Errorf("%w: object name", ErrInvalidUTF8)
	}
	pos += n

	if pos+8 > len(src) {
		return nil, ErrShortBuffer
	}
	root := storage.PageID(binary.BigEndian.Uint64(src[pos : pos+8]))
	pos += 8

	sqlBytes, n, err := readVarBytes(src[pos:])
	if err != nil {
		return nil, fmt.Errorf("decode sql: %w", err)
	}
	if !utf8.Valid(sqlBytes) {
		return nil, fmt.Errorf("%w: sql", ErrInvalidUTF8)
	}
	pos += n

	colCount, ln := binary.Uvarint(src[pos:])
	if ln <= 0 {
		return nil, ErrShortBuffer
	}
	pos += ln

	cols := make([]record.Column, 0, colCount)
	for i := uint64(0); i < colCount; i++ {
		colName, n, err := readVarBytes(src[pos:])
		if err != nil {
			return nil, fmt.Errorf("decode column %d name: %w", i, err)
		}
		if !utf8.Valid(colName) {
			return nil, fmt.Errorf("%w: column %d name", ErrInvalidUTF8, i)
		}
		pos += n

		if pos+2 > len(src) {
			return nil, ErrShortBuffer
		}
		kind := record.Kind(src[pos])
		pos++
		flags := src[pos]
		pos++

		position, ln := binary.Uvarint(src[pos:])
		if ln <= 0 {
			return nil, ErrShortBuffer
		}
		pos += ln

		cols = append(cols, record.Column{
			Name:       string(colName),
			Kind:       kind,
			NotNull:    flags&colFlagNotNull != 0,
			PrimaryKey: flags&colFlagPrimaryKey != 0,
			Position:   int(position),
		})
	}

	return &Object{
		Type:       otype,
		Name:       string(name),
		RootPageID: root,
		SQL:        string(sqlBytes),
		Schema:     record.Schema{Columns: cols},
	}, nil
}

// appendVarBytes writes [uvarint length][bytes] into dst and returns
// the grown slice.
func appendVarBytes(dst, b []byte) []byte {
	var u [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(u[:], uint64(len(b)))
	dst = append(dst, u[:n]...)
	dst = append(dst, b...)
	return dst
}

// readVarBytes reads [uvarint length][bytes] from src. Returns the byte
// slice (aliasing src — callers must copy if retaining) and the total
// number of bytes consumed.
func readVarBytes(src []byte) ([]byte, int, error) {
	length, ln := binary.Uvarint(src)
	if ln <= 0 {
		return nil, 0, ErrShortBuffer
	}
	end := ln + int(length)
	if end < ln || end > len(src) {
		return nil, 0, ErrShortBuffer
	}
	return src[ln:end], end, nil
}

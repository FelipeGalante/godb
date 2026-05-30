package record

import (
	"encoding/binary"
	"fmt"
	"unicode/utf8"
)

// rowVersion is the layout version byte written at the start of every
// encoded row. Bumped when the row layout changes incompatibly.
const rowVersion byte = 1

// EncodeValue appends the on-disk encoding of v to dst and returns the
// extended slice. The format is:
//
//	NULL     : [kind]
//	BOOLEAN  : [kind][0x00 | 0x01]
//	INTEGER  : [kind][int64 BE, 8 bytes]
//	TEXT     : [kind][uvarint length][utf-8 bytes]
//
// NULL and empty TEXT are distinct encodings: NULL is 1 byte, empty TEXT
// is 2 bytes (kind + uvarint 0).
func EncodeValue(dst []byte, v Value) ([]byte, error) {
	switch v.Kind {
	case KindNull:
		return append(dst, byte(KindNull)), nil
	case KindBoolean:
		b := byte(0)
		if v.Bool {
			b = 1
		}
		return append(dst, byte(KindBoolean), b), nil
	case KindInteger:
		var buf [9]byte
		buf[0] = byte(KindInteger)
		binary.BigEndian.PutUint64(buf[1:], uint64(v.Int))
		return append(dst, buf[:]...), nil
	case KindText:
		dst = append(dst, byte(KindText))
		var lenBuf [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(lenBuf[:], uint64(len(v.Text)))
		dst = append(dst, lenBuf[:n]...)
		dst = append(dst, v.Text...)
		return dst, nil
	default:
		return dst, fmt.Errorf("%w: 0x%02x", ErrInvalidKind, byte(v.Kind))
	}
}

// DecodeValue reads one value from src. It returns the value and the
// number of bytes consumed.
func DecodeValue(src []byte) (Value, int, error) {
	if len(src) < 1 {
		return Value{}, 0, ErrShortBuffer
	}
	kind := Kind(src[0])
	switch kind {
	case KindNull:
		return Null(), 1, nil
	case KindBoolean:
		if len(src) < 2 {
			return Value{}, 0, ErrShortBuffer
		}
		switch src[1] {
		case 0:
			return Bool(false), 2, nil
		case 1:
			return Bool(true), 2, nil
		default:
			return Value{}, 0, fmt.Errorf("%w: 0x%02x", ErrInvalidBool, src[1])
		}
	case KindInteger:
		if len(src) < 9 {
			return Value{}, 0, ErrShortBuffer
		}
		return Int(int64(binary.BigEndian.Uint64(src[1:9]))), 9, nil
	case KindText:
		length, ln := binary.Uvarint(src[1:])
		if ln <= 0 {
			return Value{}, 0, ErrShortBuffer
		}
		headerN := 1 + ln
		end := headerN + int(length)
		if end < headerN || end > len(src) {
			return Value{}, 0, ErrShortBuffer
		}
		text := string(src[headerN:end])
		if !utf8.ValidString(text) {
			return Value{}, 0, ErrInvalidUTF8
		}
		return Text(text), end, nil
	default:
		return Value{}, 0, fmt.Errorf("%w: 0x%02x", ErrInvalidKind, byte(kind))
	}
}

// EncodeRow encodes a row's values into a self-describing byte stream:
//
//	[row version: u8][column count: uvarint][value 1] [value 2] ...
//
// The schema is not part of the encoding; callers that need to validate
// against a schema should do so before encoding or after decoding.
func EncodeRow(values []Value) ([]byte, error) {
	// Pre-size: header is at most 11 bytes (1 + 10-byte uvarint).
	buf := make([]byte, 0, 11+len(values)*8)
	buf = append(buf, rowVersion)
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(values)))
	buf = append(buf, lenBuf[:n]...)
	for i, v := range values {
		var err error
		buf, err = EncodeValue(buf, v)
		if err != nil {
			return nil, fmt.Errorf("encode value %d: %w", i, err)
		}
	}
	return buf, nil
}

// DecodeRow reads a row from src. It returns the values, the number of
// bytes consumed, and any error. Callers that pass exact-fit buffers
// (e.g. a cell payload) should treat n < len(src) as ErrTrailingBytes.
func DecodeRow(src []byte) ([]Value, int, error) {
	if len(src) < 1 {
		return nil, 0, ErrShortBuffer
	}
	if src[0] != rowVersion {
		return nil, 0, fmt.Errorf("%w: 0x%02x", ErrUnsupportedRowVersion, src[0])
	}
	count, ln := binary.Uvarint(src[1:])
	if ln <= 0 {
		return nil, 0, ErrShortBuffer
	}
	pos := 1 + ln
	values := make([]Value, 0, count)
	for i := uint64(0); i < count; i++ {
		v, n, err := DecodeValue(src[pos:])
		if err != nil {
			return nil, 0, fmt.Errorf("decode value %d: %w", i, err)
		}
		values = append(values, v)
		pos += n
	}
	return values, pos, nil
}

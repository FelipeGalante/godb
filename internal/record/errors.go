// Package record encodes and decodes typed row values for GoDB. It owns
// the on-disk representation of values and rows, and the schema-level
// validation that callers apply before encoding. It does no I/O — bytes
// in, bytes out.
package record

import "errors"

var (
	// ErrShortBuffer is returned when a decode runs out of input before
	// the value or row is fully read.
	ErrShortBuffer = errors.New("record: short buffer")

	// ErrTrailingBytes is returned when DecodeRow consumes fewer bytes
	// than were provided. Callers using the codec with cell payloads
	// should treat this as corruption.
	ErrTrailingBytes = errors.New("record: trailing bytes after row")

	// ErrInvalidKind is returned when an encoded value's kind byte is
	// outside the known set.
	ErrInvalidKind = errors.New("record: invalid value kind")

	// ErrInvalidUTF8 is returned when a TEXT value's bytes are not
	// valid UTF-8.
	ErrInvalidUTF8 = errors.New("record: text value is not valid utf-8")

	// ErrInvalidBool is returned when a BOOLEAN payload byte is not
	// exactly 0x00 or 0x01.
	ErrInvalidBool = errors.New("record: invalid boolean payload")

	// ErrUnsupportedRowVersion is returned when the row version byte
	// does not match the current v0.1 row layout.
	ErrUnsupportedRowVersion = errors.New("record: unsupported row version")

	// ErrColumnCountMismatch is returned by Schema.Validate when the
	// number of values does not match the number of columns.
	ErrColumnCountMismatch = errors.New("record: column count mismatch")

	// ErrNullViolation is returned by Schema.Validate when a NULL value
	// is supplied for a NOT NULL column.
	ErrNullViolation = errors.New("record: null value for not-null column")

	// ErrTypeMismatch is returned by Schema.Validate when a value's
	// kind does not match the column's declared kind.
	ErrTypeMismatch = errors.New("record: value kind does not match column type")
)

package record

// Kind tags the type of a Value. Kind byte values are part of the on-disk
// format — do not reorder, and do not switch to iota assignment.
type Kind uint8

const (
	KindNull    Kind = 0
	KindInteger Kind = 1
	KindText    Kind = 2
	KindBoolean Kind = 3
)

// String returns a short name for debugging and error messages. It is
// not the on-disk representation.
func (k Kind) String() string {
	switch k {
	case KindNull:
		return "NULL"
	case KindInteger:
		return "INTEGER"
	case KindText:
		return "TEXT"
	case KindBoolean:
		return "BOOLEAN"
	default:
		return "UNKNOWN"
	}
}

// Value is a tagged union over the four v0.1 scalar kinds. Only the
// field matching Kind is meaningful; other fields are zero.
//
// Construct values via Null / Int / Text / Bool rather than literal
// struct values so that misuse (e.g. supplying both Int and Text) is
// less likely.
type Value struct {
	Kind Kind
	Int  int64
	Text string
	Bool bool
}

// Null returns a NULL value.
func Null() Value { return Value{Kind: KindNull} }

// Int returns an INTEGER value.
func Int(v int64) Value { return Value{Kind: KindInteger, Int: v} }

// Text returns a TEXT value. The caller is responsible for ensuring the
// string is valid UTF-8; encoding does not validate.
func Text(v string) Value { return Value{Kind: KindText, Text: v} }

// Bool returns a BOOLEAN value.
func Bool(v bool) Value { return Value{Kind: KindBoolean, Bool: v} }

// IsNull is shorthand for v.Kind == KindNull.
func (v Value) IsNull() bool { return v.Kind == KindNull }

package record

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
)

func roundTripValue(t *testing.T, in Value) {
	t.Helper()
	buf, err := EncodeValue(nil, in)
	if err != nil {
		t.Fatalf("EncodeValue(%+v): %v", in, err)
	}
	got, n, err := DecodeValue(buf)
	if err != nil {
		t.Fatalf("DecodeValue(%x): %v", buf, err)
	}
	if n != len(buf) {
		t.Errorf("DecodeValue consumed %d bytes, want %d", n, len(buf))
	}
	if got != in {
		t.Errorf("round trip: got %+v, want %+v", got, in)
	}
}

func TestRoundTripNull(t *testing.T) {
	roundTripValue(t, Null())
}

func TestRoundTripBool(t *testing.T) {
	roundTripValue(t, Bool(true))
	roundTripValue(t, Bool(false))
}

func TestRoundTripIntegerBoundaries(t *testing.T) {
	for _, n := range []int64{0, 1, -1, math.MinInt64, math.MaxInt64, 1 << 40, -(1 << 40)} {
		roundTripValue(t, Int(n))
	}
}

func TestRoundTripText(t *testing.T) {
	cases := []string{
		"",
		"hello",
		"日本語",
		"café",
		"emoji 🌱 and 🚀",
		strings.Repeat("a", 3000),
	}
	for _, s := range cases {
		roundTripValue(t, Text(s))
	}
}

func TestNullAndEmptyTextAreDistinct(t *testing.T) {
	nullBuf, _ := EncodeValue(nil, Null())
	emptyTextBuf, _ := EncodeValue(nil, Text(""))
	if bytes.Equal(nullBuf, emptyTextBuf) {
		t.Fatalf("NULL and empty TEXT have the same encoding (%x); they must differ", nullBuf)
	}
	if len(nullBuf) != 1 {
		t.Errorf("NULL encoding = %d bytes, want 1", len(nullBuf))
	}
	if len(emptyTextBuf) != 2 {
		t.Errorf("empty TEXT encoding = %d bytes, want 2", len(emptyTextBuf))
	}
}

func TestDecodeValueRejectsInvalidKind(t *testing.T) {
	_, _, err := DecodeValue([]byte{0xFF})
	if !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("err = %v, want ErrInvalidKind", err)
	}
}

func TestDecodeValueRejectsInvalidBool(t *testing.T) {
	_, _, err := DecodeValue([]byte{byte(KindBoolean), 0x02})
	if !errors.Is(err, ErrInvalidBool) {
		t.Fatalf("err = %v, want ErrInvalidBool", err)
	}
}

func TestDecodeValueRejectsShortBuffer(t *testing.T) {
	cases := []struct {
		name string
		buf  []byte
	}{
		{"empty", nil},
		{"integer truncated", []byte{byte(KindInteger), 1, 2, 3}},
		{"boolean missing payload", []byte{byte(KindBoolean)}},
		{"text missing length", []byte{byte(KindText)}},
		{"text length larger than buffer", []byte{byte(KindText), 0x05, 'a', 'b'}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := DecodeValue(tc.buf)
			if !errors.Is(err, ErrShortBuffer) {
				t.Errorf("err = %v, want ErrShortBuffer", err)
			}
		})
	}
}

func TestDecodeValueRejectsInvalidUTF8(t *testing.T) {
	buf := []byte{byte(KindText), 0x02, 0xff, 0xfe}
	_, _, err := DecodeValue(buf)
	if !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("err = %v, want ErrInvalidUTF8", err)
	}
}

func TestRoundTripRow(t *testing.T) {
	in := []Value{
		Int(42),
		Text("Felipe"),
		Bool(true),
		Null(),
		Int(math.MinInt64),
	}
	buf, err := EncodeRow(in)
	if err != nil {
		t.Fatalf("EncodeRow: %v", err)
	}
	got, n, err := DecodeRow(buf)
	if err != nil {
		t.Fatalf("DecodeRow: %v", err)
	}
	if n != len(buf) {
		t.Errorf("DecodeRow consumed %d, want %d", n, len(buf))
	}
	if len(got) != len(in) {
		t.Fatalf("decoded %d values, want %d", len(got), len(in))
	}
	for i := range in {
		if got[i] != in[i] {
			t.Errorf("values[%d] = %+v, want %+v", i, got[i], in[i])
		}
	}
}

func TestRoundTripEmptyRow(t *testing.T) {
	buf, err := EncodeRow(nil)
	if err != nil {
		t.Fatalf("EncodeRow(nil): %v", err)
	}
	got, n, err := DecodeRow(buf)
	if err != nil {
		t.Fatalf("DecodeRow: %v", err)
	}
	if n != len(buf) {
		t.Errorf("DecodeRow consumed %d, want %d", n, len(buf))
	}
	if len(got) != 0 {
		t.Errorf("decoded %d values, want 0", len(got))
	}
}

func TestDecodeRowRejectsUnsupportedVersion(t *testing.T) {
	buf := []byte{0xFF, 0x00}
	_, _, err := DecodeRow(buf)
	if !errors.Is(err, ErrUnsupportedRowVersion) {
		t.Fatalf("err = %v, want ErrUnsupportedRowVersion", err)
	}
}

func TestDecodeRowShortBuffer(t *testing.T) {
	_, _, err := DecodeRow(nil)
	if !errors.Is(err, ErrShortBuffer) {
		t.Fatalf("err = %v, want ErrShortBuffer", err)
	}
}

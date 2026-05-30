package catalog

import (
	"errors"
	"strings"
	"testing"

	"github.com/felipegalante/godb/internal/record"
	"github.com/felipegalante/godb/internal/storage"
)

func usersObject() *Object {
	return &Object{
		Type:       ObjectTypeTable,
		Name:       "users",
		RootPageID: storage.PageID(42),
		SQL:        "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, active BOOLEAN);",
		Schema: record.Schema{Columns: []record.Column{
			{Name: "id", Kind: record.KindInteger, NotNull: true, PrimaryKey: true, Position: 0},
			{Name: "name", Kind: record.KindText, NotNull: true, Position: 1},
			{Name: "active", Kind: record.KindBoolean, Position: 2},
		}},
	}
}

func assertSchemaEqual(t *testing.T, got, want record.Schema) {
	t.Helper()
	if len(got.Columns) != len(want.Columns) {
		t.Fatalf("column count = %d, want %d", len(got.Columns), len(want.Columns))
	}
	for i := range want.Columns {
		if got.Columns[i] != want.Columns[i] {
			t.Errorf("column[%d] = %+v, want %+v", i, got.Columns[i], want.Columns[i])
		}
	}
}

func TestEncodeDecodeObjectRoundTrip(t *testing.T) {
	in := usersObject()
	buf, err := EncodeObject(in)
	if err != nil {
		t.Fatalf("EncodeObject: %v", err)
	}
	if len(buf) < 2 || buf[0] != catalogFormatVersion {
		t.Fatalf("first byte = 0x%02x, want 0x%02x", buf[0], catalogFormatVersion)
	}
	got, err := DecodeObject(buf)
	if err != nil {
		t.Fatalf("DecodeObject: %v", err)
	}
	if got.Type != in.Type {
		t.Errorf("Type = %d, want %d", got.Type, in.Type)
	}
	if got.Name != in.Name {
		t.Errorf("Name = %q, want %q", got.Name, in.Name)
	}
	if got.RootPageID != in.RootPageID {
		t.Errorf("RootPageID = %d, want %d", got.RootPageID, in.RootPageID)
	}
	if got.SQL != in.SQL {
		t.Errorf("SQL = %q, want %q", got.SQL, in.SQL)
	}
	assertSchemaEqual(t, got.Schema, in.Schema)
}

func TestDecodeRejectsUnsupportedVersion(t *testing.T) {
	buf := []byte{0xFF, byte(ObjectTypeTable), 0x00}
	_, err := DecodeObject(buf)
	if !errors.Is(err, ErrUnsupportedCatalogVersion) {
		t.Fatalf("err = %v, want ErrUnsupportedCatalogVersion", err)
	}
}

func TestDecodeRejectsEmptyBuffer(t *testing.T) {
	_, err := DecodeObject(nil)
	if !errors.Is(err, ErrShortBuffer) {
		t.Fatalf("err = %v, want ErrShortBuffer", err)
	}
}

func TestDecodeRejectsInvalidObjectType(t *testing.T) {
	in := usersObject()
	buf, err := EncodeObject(in)
	if err != nil {
		t.Fatalf("EncodeObject: %v", err)
	}
	// Corrupt the object type byte (offset 1).
	buf[1] = 0x7F
	_, err = DecodeObject(buf)
	if !errors.Is(err, ErrInvalidObjectType) {
		t.Fatalf("err = %v, want ErrInvalidObjectType", err)
	}
}

func TestEncodeObjectRejectsInvalidType(t *testing.T) {
	in := usersObject()
	in.Type = ObjectType(0x7F)
	_, err := EncodeObject(in)
	if !errors.Is(err, ErrInvalidObjectType) {
		t.Fatalf("err = %v, want ErrInvalidObjectType", err)
	}
}

func TestRoundTripEmptySQL(t *testing.T) {
	in := usersObject()
	in.SQL = ""
	buf, err := EncodeObject(in)
	if err != nil {
		t.Fatalf("EncodeObject: %v", err)
	}
	got, err := DecodeObject(buf)
	if err != nil {
		t.Fatalf("DecodeObject: %v", err)
	}
	if got.SQL != "" {
		t.Errorf("SQL = %q, want empty", got.SQL)
	}
	if len(got.Schema.Columns) != len(in.Schema.Columns) {
		t.Errorf("column count = %d, want %d", len(got.Schema.Columns), len(in.Schema.Columns))
	}
}

func TestRoundTripEmptySchema(t *testing.T) {
	in := &Object{
		Type:       ObjectTypeTable,
		Name:       "empty",
		RootPageID: 0,
		SQL:        "",
		Schema:     record.Schema{},
	}
	buf, err := EncodeObject(in)
	if err != nil {
		t.Fatalf("EncodeObject: %v", err)
	}
	got, err := DecodeObject(buf)
	if err != nil {
		t.Fatalf("DecodeObject: %v", err)
	}
	if len(got.Schema.Columns) != 0 {
		t.Errorf("column count = %d, want 0", len(got.Schema.Columns))
	}
}

func TestRoundTripManyColumns(t *testing.T) {
	cols := make([]record.Column, 50)
	for i := range cols {
		cols[i] = record.Column{
			Name:     "col_" + strings.Repeat("x", i%5+1),
			Kind:     record.KindInteger,
			NotNull:  i%2 == 0,
			Position: i,
		}
	}
	cols[0].PrimaryKey = true
	in := &Object{
		Type:       ObjectTypeTable,
		Name:       "wide",
		RootPageID: 99,
		SQL:        "CREATE TABLE wide (...);",
		Schema:     record.Schema{Columns: cols},
	}
	buf, err := EncodeObject(in)
	if err != nil {
		t.Fatalf("EncodeObject: %v", err)
	}
	got, err := DecodeObject(buf)
	if err != nil {
		t.Fatalf("DecodeObject: %v", err)
	}
	assertSchemaEqual(t, got.Schema, in.Schema)
}

func TestRoundTripUTF8Strings(t *testing.T) {
	in := &Object{
		Type:       ObjectTypeTable,
		Name:       "ユーザー",
		RootPageID: 7,
		SQL:        "CREATE TABLE ユーザー (id 🔑 INTEGER);",
		Schema: record.Schema{Columns: []record.Column{
			{Name: "🔑", Kind: record.KindInteger, PrimaryKey: true, Position: 0},
		}},
	}
	buf, err := EncodeObject(in)
	if err != nil {
		t.Fatalf("EncodeObject: %v", err)
	}
	got, err := DecodeObject(buf)
	if err != nil {
		t.Fatalf("DecodeObject: %v", err)
	}
	if got.Name != in.Name {
		t.Errorf("Name = %q, want %q", got.Name, in.Name)
	}
	if got.Schema.Columns[0].Name != "🔑" {
		t.Errorf("column[0].Name = %q, want %q", got.Schema.Columns[0].Name, "🔑")
	}
}

func TestDecodeRejectsInvalidUTF8(t *testing.T) {
	// Hand-craft a payload with valid header but invalid UTF-8 in the name.
	buf := []byte{
		catalogFormatVersion,
		byte(ObjectTypeTable),
		0x02, 0xff, 0xfe, // name length 2, bytes are not valid UTF-8
	}
	_, err := DecodeObject(buf)
	if !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("err = %v, want ErrInvalidUTF8", err)
	}
}

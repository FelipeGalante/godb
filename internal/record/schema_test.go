package record

import (
	"errors"
	"testing"
)

func usersSchema() *Schema {
	return &Schema{Columns: []Column{
		{Name: "id", Kind: KindInteger, NotNull: true, PrimaryKey: true, Position: 0},
		{Name: "name", Kind: KindText, NotNull: true, Position: 1},
		{Name: "nickname", Kind: KindText, Position: 2},
		{Name: "active", Kind: KindBoolean, Position: 3},
	}}
}

func TestSchemaValidateOK(t *testing.T) {
	s := usersSchema()
	values := []Value{Int(1), Text("Felipe"), Null(), Bool(true)}
	if err := s.Validate(values); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSchemaValidateColumnCountMismatch(t *testing.T) {
	s := usersSchema()
	values := []Value{Int(1), Text("Felipe")}
	err := s.Validate(values)
	if !errors.Is(err, ErrColumnCountMismatch) {
		t.Fatalf("err = %v, want ErrColumnCountMismatch", err)
	}
}

func TestSchemaValidateNullViolation(t *testing.T) {
	s := usersSchema()
	values := []Value{Int(1), Null(), Null(), Bool(true)} // name is NOT NULL
	err := s.Validate(values)
	if !errors.Is(err, ErrNullViolation) {
		t.Fatalf("err = %v, want ErrNullViolation", err)
	}
}

func TestSchemaValidateTypeMismatch(t *testing.T) {
	s := usersSchema()
	values := []Value{Text("not-an-int"), Text("Felipe"), Null(), Bool(true)}
	err := s.Validate(values)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want ErrTypeMismatch", err)
	}
}

func TestSchemaValidateAllowsNullInNullableColumn(t *testing.T) {
	s := usersSchema()
	values := []Value{Int(1), Text("Felipe"), Null(), Null()}
	if err := s.Validate(values); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSchemaValidateAcceptsBooleanFalse(t *testing.T) {
	s := usersSchema()
	values := []Value{Int(0), Text(""), Null(), Bool(false)}
	if err := s.Validate(values); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

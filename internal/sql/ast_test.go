package sql

import (
	"testing"

	"github.com/felipegalante/godb/internal/record"
)

func TestColumnDefsToSchemaMapsCorrectly(t *testing.T) {
	defs := []ColumnDef{
		{Name: "id", Kind: record.KindInteger, NotNull: true, PrimaryKey: true, Pos: Position{Line: 1, Column: 5}},
		{Name: "name", Kind: record.KindText, NotNull: true},
		{Name: "nickname", Kind: record.KindText},
		{Name: "active", Kind: record.KindBoolean},
	}
	schema := ColumnDefsToSchema(defs)
	if len(schema.Columns) != len(defs) {
		t.Fatalf("len(Columns) = %d, want %d", len(schema.Columns), len(defs))
	}
	for i, def := range defs {
		col := schema.Columns[i]
		if col.Name != def.Name {
			t.Errorf("[%d] Name = %q, want %q", i, col.Name, def.Name)
		}
		if col.Kind != def.Kind {
			t.Errorf("[%d] Kind = %v, want %v", i, col.Kind, def.Kind)
		}
		if col.NotNull != def.NotNull {
			t.Errorf("[%d] NotNull = %v, want %v", i, col.NotNull, def.NotNull)
		}
		if col.PrimaryKey != def.PrimaryKey {
			t.Errorf("[%d] PrimaryKey = %v, want %v", i, col.PrimaryKey, def.PrimaryKey)
		}
		if col.Position != i {
			t.Errorf("[%d] Position = %d, want %d", i, col.Position, i)
		}
	}

	// Round-trip through Schema.Validate to confirm shape compatibility.
	if err := schema.Validate([]record.Value{
		record.Int(1),
		record.Text("Felipe"),
		record.Null(),
		record.Bool(true),
	}); err != nil {
		t.Errorf("round-tripped Schema.Validate: %v", err)
	}
}

func TestColumnDefsToSchemaEmpty(t *testing.T) {
	schema := ColumnDefsToSchema(nil)
	if len(schema.Columns) != 0 {
		t.Errorf("empty defs produced %d columns, want 0", len(schema.Columns))
	}
}

func TestStatementsImplementStatement(t *testing.T) {
	// Compile-time-ish guard: every statement type implements Statement.
	var _ Statement = (*CreateTableStatement)(nil)
	var _ Statement = (*InsertStatement)(nil)
	var _ Statement = (*SelectStatement)(nil)
}

func TestExpressionsImplementExpression(t *testing.T) {
	var _ Expression = (*IntegerLiteral)(nil)
	var _ Expression = (*StringLiteral)(nil)
	var _ Expression = (*BooleanLiteral)(nil)
	var _ Expression = (*NullLiteral)(nil)
	var _ Expression = (*Placeholder)(nil)
	var _ Expression = (*Identifier)(nil)
	var _ Expression = (*BinaryExpr)(nil)
}

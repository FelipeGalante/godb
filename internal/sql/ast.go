package sql

import "github.com/felipegalante/godb/internal/record"

// Statement is the common interface for every top-level parsed
// statement. Implementations carry a starting Position for error
// messages and (later) IDE-style highlighting.
type Statement interface {
	statementNode()
	Position() Position
}

// CreateTableStatement is the AST for `CREATE TABLE name (...)`.
type CreateTableStatement struct {
	Name    string
	Columns []ColumnDef
	Pos     Position
}

func (s *CreateTableStatement) statementNode()     {}
func (s *CreateTableStatement) Position() Position { return s.Pos }

// ColumnDef is one column declaration inside a CreateTableStatement.
// Kind reuses internal/record's Kind enum directly — the same byte
// values that show up in encoded rows on disk show up in catalog rows.
type ColumnDef struct {
	Name       string
	Kind       record.Kind
	NotNull    bool
	PrimaryKey bool
	Pos        Position
}

// InsertStatement is the AST for `INSERT INTO name (cols) VALUES (...)`.
// Columns is empty when the user omitted the explicit list (i.e.
// `INSERT INTO name VALUES (...)`); the executor will treat that as
// "values in declared schema order."
type InsertStatement struct {
	Table   string
	Columns []string
	Values  []Expression
	Pos     Position
}

func (s *InsertStatement) statementNode()     {}
func (s *InsertStatement) Position() Position { return s.Pos }

// SelectStatement is the AST for `SELECT ... FROM name [WHERE ...]`.
// Either Wildcard is true (representing `SELECT *`) or Columns lists
// the projection in source order.
type SelectStatement struct {
	Wildcard bool
	Columns  []string
	Table    string
	Where    Expression
	Pos      Position
}

func (s *SelectStatement) statementNode()     {}
func (s *SelectStatement) Position() Position { return s.Pos }

// ColumnDefsToSchema converts a parser-shaped column list into the
// record.Schema shape the catalog wants. Used by M9's executor when
// translating a parsed CREATE TABLE into a catalog.CreateTable call.
// The order of Schema.Columns is preserved from defs; the Position
// field is dropped (the schema layer doesn't carry source positions).
func ColumnDefsToSchema(defs []ColumnDef) record.Schema {
	cols := make([]record.Column, len(defs))
	for i, d := range defs {
		cols[i] = record.Column{
			Name:       d.Name,
			Kind:       d.Kind,
			NotNull:    d.NotNull,
			PrimaryKey: d.PrimaryKey,
			Position:   i,
		}
	}
	return record.Schema{Columns: cols}
}

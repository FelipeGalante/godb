package planner

import (
	"errors"
	"fmt"

	"github.com/felipegalante/godb/internal/catalog"
	"github.com/felipegalante/godb/internal/record"
	"github.com/felipegalante/godb/internal/sql"
)

// Planner produces a Plan for any supported sql.Statement. It needs a
// catalog reference to resolve table and column names. The planner is
// stateless beyond its catalog handle; callers can share one Planner
// across many calls.
type Planner struct {
	catalog *catalog.Catalog
}

// New constructs a Planner.
func New(c *catalog.Catalog) *Planner { return &Planner{catalog: c} }

// Sentinel errors used by both the planner and the pkg/godb layer.
// pkg/godb re-exports these (or wraps them) so callers can dispatch
// with errors.Is.
var (
	// ErrTableNotFound is returned when a referenced table doesn't
	// exist in the catalog.
	ErrTableNotFound = errors.New("planner: table not found")

	// ErrTableExists is returned by Plan(CREATE TABLE) when the name
	// is already taken.
	ErrTableExists = errors.New("planner: table already exists")

	// ErrColumnNotFound is returned when a referenced column doesn't
	// exist in the resolved table's schema.
	ErrColumnNotFound = errors.New("planner: column not found")

	// ErrInvalidSchema is returned by Plan(CREATE TABLE) for shape
	// problems the parser doesn't catch (no PK, multiple PKs, non-
	// INTEGER PK in v0.1).
	ErrInvalidSchema = errors.New("planner: invalid table schema")

	// ErrWhereOnlyPrimaryKey is returned for WHERE clauses on
	// non-primary-key columns. v0.2 will add TableScan+Filter.
	ErrWhereOnlyPrimaryKey = errors.New("planner: v0.1 only supports WHERE on the primary key")

	// ErrInsertCountMismatch is returned when INSERT's value count
	// doesn't match the column count.
	ErrInsertCountMismatch = errors.New("planner: INSERT value count does not match column count")

	// ErrUnsupportedStatement is returned for AST shapes the planner
	// doesn't recognize. Should never happen in v0.1 because the
	// parser produces only the three statement kinds we plan for.
	ErrUnsupportedStatement = errors.New("planner: unsupported statement kind")
)

// Plan dispatches on the statement kind and returns the appropriate
// Plan. Schema validation happens here; pure binding (placeholders
// to args) happens at execution time.
func (p *Planner) Plan(stmt sql.Statement) (Plan, error) {
	switch s := stmt.(type) {
	case *sql.CreateTableStatement:
		return p.planCreateTable(s)
	case *sql.InsertStatement:
		return p.planInsert(s)
	case *sql.SelectStatement:
		return p.planSelect(s)
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedStatement, stmt)
	}
}

// planCreateTable validates the parser-shaped schema and converts to a
// record.Schema. Rejects: duplicate name, empty column list, no
// PRIMARY KEY, multiple PRIMARY KEYs, non-INTEGER PRIMARY KEY (v0.1
// only supports integer rowids per spec §7.4).
func (p *Planner) planCreateTable(s *sql.CreateTableStatement) (*CreateTablePlan, error) {
	if _, err := p.catalog.LookupTable(s.Name); err == nil {
		return nil, fmt.Errorf("%w: %q", ErrTableExists, s.Name)
	}
	if len(s.Columns) == 0 {
		return nil, fmt.Errorf("%w: %q has no columns", ErrInvalidSchema, s.Name)
	}
	// Find the primary key.
	pkCount := 0
	var pkIdx int
	for i, c := range s.Columns {
		if c.PrimaryKey {
			pkCount++
			pkIdx = i
		}
	}
	if pkCount == 0 {
		return nil, fmt.Errorf("%w: %q has no PRIMARY KEY (v0.1 requires exactly one INTEGER PRIMARY KEY column)", ErrInvalidSchema, s.Name)
	}
	if pkCount > 1 {
		return nil, fmt.Errorf("%w: %q has %d PRIMARY KEY columns (v0.1 requires exactly one)", ErrInvalidSchema, s.Name, pkCount)
	}
	if s.Columns[pkIdx].Kind != record.KindInteger {
		return nil, fmt.Errorf("%w: PRIMARY KEY column %q in %q must be INTEGER (v0.1 only supports integer rowids)",
			ErrInvalidSchema, s.Columns[pkIdx].Name, s.Name)
	}
	return &CreateTablePlan{
		Name:   s.Name,
		Schema: sql.ColumnDefsToSchema(s.Columns),
		SQL:    "", // executor stamps this from the original source string
	}, nil
}

// planInsert validates the insert against the table's schema. The
// returned plan has Columns as schema-column indices in user-specified
// order, or nil when the user omitted the explicit list. Value count
// is checked here so the executor doesn't have to.
func (p *Planner) planInsert(s *sql.InsertStatement) (*InsertPlan, error) {
	info, err := p.catalog.LookupTable(s.Table)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrTableNotFound, s.Table)
	}
	plan := &InsertPlan{
		Table:  s.Table,
		Schema: info.Schema,
		Values: s.Values,
	}
	if len(s.Columns) == 0 {
		// No explicit column list — values are in schema order with all columns.
		if len(s.Values) != len(info.Schema.Columns) {
			return nil, fmt.Errorf("%w: table %q has %d columns, got %d values",
				ErrInsertCountMismatch, s.Table, len(info.Schema.Columns), len(s.Values))
		}
		return plan, nil
	}
	// Explicit column list — resolve every name to its schema index.
	indices := make([]int, 0, len(s.Columns))
	for _, name := range s.Columns {
		idx := -1
		for i, col := range info.Schema.Columns {
			if col.Name == name {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, fmt.Errorf("%w: %q on table %q", ErrColumnNotFound, name, s.Table)
		}
		indices = append(indices, idx)
	}
	if len(indices) != len(s.Values) {
		return nil, fmt.Errorf("%w: %d columns named, %d values given",
			ErrInsertCountMismatch, len(indices), len(s.Values))
	}
	plan.Columns = indices
	return plan, nil
}

// planSelect dispatches to TableScan / PrimaryKeyLookup (optionally
// wrapped in Projection). The WHERE clause is restricted to
// primary-key equality in v0.1; non-PK predicates produce
// ErrWhereOnlyPrimaryKey.
func (p *Planner) planSelect(s *sql.SelectStatement) (Plan, error) {
	info, err := p.catalog.LookupTable(s.Table)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrTableNotFound, s.Table)
	}
	// Inner plan: TableScan or PrimaryKeyLookup.
	var inner Plan
	if s.Where == nil {
		inner = &TableScanPlan{Table: s.Table, Schema: info.Schema, RootID: info.RootPageID}
	} else {
		pk, err := primaryKeyColumn(info.Schema)
		if err != nil {
			return nil, err
		}
		// WHERE was parsed as `identifier "=" expression`; pull out
		// the identifier and check it's the PK.
		be, ok := s.Where.(*sql.BinaryExpr)
		if !ok || be.Op != "=" {
			return nil, fmt.Errorf("%w: WHERE must be of the form `<column> = <value>`", ErrWhereOnlyPrimaryKey)
		}
		ident, ok := be.Left.(*sql.Identifier)
		if !ok {
			return nil, fmt.Errorf("%w: WHERE left side must be a column reference", ErrWhereOnlyPrimaryKey)
		}
		if ident.Name != pk.Name {
			return nil, fmt.Errorf("%w: %q is not the primary key of %q", ErrWhereOnlyPrimaryKey, ident.Name, s.Table)
		}
		inner = &PrimaryKeyLookupPlan{Table: s.Table, Schema: info.Schema, RootID: info.RootPageID, KeyExpr: be.Right}
	}
	// Projection: wildcard = no wrapping; named columns = ProjectionPlan.
	if s.Wildcard {
		return inner, nil
	}
	indices := make([]int, 0, len(s.Columns))
	names := make([]string, 0, len(s.Columns))
	for _, name := range s.Columns {
		idx := -1
		for i, col := range info.Schema.Columns {
			if col.Name == name {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, fmt.Errorf("%w: %q on table %q", ErrColumnNotFound, name, s.Table)
		}
		indices = append(indices, idx)
		names = append(names, name)
	}
	return &ProjectionPlan{Output: indices, Names: names, Input: inner}, nil
}

// primaryKeyColumn returns the (single) primary-key column from the
// schema. v0.1 guarantees exactly one — established at CreateTable
// time by planCreateTable's validation.
func primaryKeyColumn(s record.Schema) (record.Column, error) {
	for _, c := range s.Columns {
		if c.PrimaryKey {
			return c, nil
		}
	}
	return record.Column{}, fmt.Errorf("%w: schema has no PRIMARY KEY (catalog corruption?)", ErrInvalidSchema)
}

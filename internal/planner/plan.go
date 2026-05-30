// Package planner turns parsed SQL ASTs into executable plans.
//
// In v0.1 the planner is small: each statement kind produces one of
// five plan types, the catalog is consulted for schema validation,
// and unsupported shapes (non-PK WHERE, missing columns, unknown
// tables) are rejected here rather than at execution time so the
// error UX stays close to the source.
//
// The planner does not execute anything. internal/exec does that.
package planner

import (
	"github.com/felipegalante/godb/internal/record"
	"github.com/felipegalante/godb/internal/sql"
	"github.com/felipegalante/godb/internal/storage"
)

// Plan is the common interface for every executable plan. Plan types
// are concrete structs; the executor type-switches on them.
type Plan interface {
	planNode()
}

// CreateTablePlan registers a new table with the catalog. The Schema
// is the parser's column definitions converted to record.Schema, and
// SQL is the original CREATE TABLE source (stored alongside the
// metadata for debugging / inspect tools).
type CreateTablePlan struct {
	Name   string
	Schema record.Schema
	SQL    string
}

func (*CreateTablePlan) planNode() {}

// InsertPlan inserts one row into a table. Columns holds schema-column
// indices in the user's specified order (e.g. for
// INSERT INTO t (c, a) ... it's [2, 0] when t's schema is (a, b, c)).
// Columns is nil when the user omitted the explicit column list, in
// which case Values is assumed to be in schema order with all columns
// present.
type InsertPlan struct {
	Table   string
	Schema  record.Schema
	Columns []int
	Values  []sql.Expression
}

func (*InsertPlan) planNode() {}

// TableScanPlan iterates every row of a table in primary-key order.
type TableScanPlan struct {
	Table  string
	Schema record.Schema
	RootID storage.PageID // resolved at plan time; executor uses for btree.Open
}

func (*TableScanPlan) planNode() {}

// PrimaryKeyLookupPlan fetches at most one row by its primary key.
// KeyExpr is a literal or placeholder; the executor binds it.
type PrimaryKeyLookupPlan struct {
	Table   string
	Schema  record.Schema
	RootID  storage.PageID
	KeyExpr sql.Expression
}

func (*PrimaryKeyLookupPlan) planNode() {}

// ProjectionPlan picks a subset of columns from the rows produced by
// Input. Output is the list of schema-column indices in output order;
// Names is the parallel list of column names (used for the Rows
// header). Wrapping the same plan in ProjectionPlan twice is
// possible but never produced by the v0.1 planner.
type ProjectionPlan struct {
	Output []int
	Names  []string
	Input  Plan
}

func (*ProjectionPlan) planNode() {}

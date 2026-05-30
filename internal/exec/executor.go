// Package exec runs plans produced by internal/planner against a
// pager and catalog. It owns the runtime side of GoDB: parameter
// binding, schema validation of bound values, B+tree I/O for
// INSERT/SELECT, and materializing query results into Rows the public
// API hands to callers.
//
// The executor is stateless beyond its pager + catalog references;
// concurrent callers serialize at the pager's mutex.
package exec

import (
	"errors"
	"fmt"

	"github.com/felipegalante/godb/internal/btree"
	"github.com/felipegalante/godb/internal/catalog"
	"github.com/felipegalante/godb/internal/planner"
	"github.com/felipegalante/godb/internal/record"
	"github.com/felipegalante/godb/internal/sql"
	"github.com/felipegalante/godb/internal/storage"
)

// Sentinel errors used by both the executor and the pkg/godb layer.
var (
	// ErrTypeMismatch is returned when an argument's Go type or a
	// row value's record.Kind doesn't match the column's declared
	// kind. No implicit conversions in v0.1.
	ErrTypeMismatch = errors.New("exec: type mismatch")

	// ErrNullViolation is returned when a NULL value is inserted
	// into a NOT NULL column.
	ErrNullViolation = errors.New("exec: NULL into NOT NULL column")

	// ErrDuplicatePrimaryKey is returned by INSERT when the key is
	// already present in the table.
	ErrDuplicatePrimaryKey = errors.New("exec: duplicate primary key")

	// ErrPlaceholderCountMismatch is returned when the number of
	// placeholders in the SQL doesn't match the number of args
	// passed.
	ErrPlaceholderCountMismatch = errors.New("exec: placeholder count does not match args count")

	// ErrUnsupportedArgType is returned for argument types other
	// than int/int32/int64/string/bool/nil.
	ErrUnsupportedArgType = errors.New("exec: unsupported argument type")

	// ErrUnsupportedPlan is returned if a plan node arrives that the
	// executor doesn't know about. Shouldn't happen in v0.1.
	ErrUnsupportedPlan = errors.New("exec: unsupported plan node")
)

// Executor runs plans. The pager and catalog references are
// non-owning; the caller (typically *pkg/godb.DB) keeps them alive.
type Executor struct {
	pager   *storage.Pager
	catalog *catalog.Catalog
}

// New constructs an Executor.
func New(p *storage.Pager, c *catalog.Catalog) *Executor {
	return &Executor{pager: p, catalog: c}
}

// Result is returned by Run for statements that don't produce row
// data (CREATE TABLE, INSERT).
type Result struct {
	RowsAffected int64
	LastInsertID int64
}

// Rows is the materialized result set returned by RunQuery.
// Materialization (vs streaming) is the v0.1 strategy — see ADR-0016.
// Each row is a []record.Value in Columns-aligned order.
type Rows struct {
	Columns []string
	Values  [][]record.Value
}

// Run executes a plan that produces no row data. Returns
// ErrUnsupportedPlan if given a plan kind that doesn't fit (e.g. a
// TableScanPlan should go through RunQuery).
func (e *Executor) Run(plan planner.Plan, args []any, sqlSrc string) (Result, error) {
	switch p := plan.(type) {
	case *planner.CreateTablePlan:
		return e.runCreateTable(p, sqlSrc)
	case *planner.InsertPlan:
		return e.runInsert(p, args)
	default:
		return Result{}, fmt.Errorf("%w: %T (use RunQuery for SELECT)", ErrUnsupportedPlan, plan)
	}
}

// RunQuery executes a plan that produces row data. The result is
// fully materialized in memory per ADR-0016.
func (e *Executor) RunQuery(plan planner.Plan, args []any) (*Rows, error) {
	switch p := plan.(type) {
	case *planner.TableScanPlan:
		return e.runTableScan(p)
	case *planner.PrimaryKeyLookupPlan:
		return e.runPKLookup(p, args)
	case *planner.ProjectionPlan:
		return e.runProjection(p, args)
	default:
		return nil, fmt.Errorf("%w: %T (use Run for non-query plans)", ErrUnsupportedPlan, plan)
	}
}

// runCreateTable registers the table in the catalog. The planner
// already validated the schema shape; the catalog enforces name
// uniqueness.
func (e *Executor) runCreateTable(p *planner.CreateTablePlan, sqlSrc string) (Result, error) {
	if _, err := e.catalog.CreateTable(p.Name, p.Schema, sqlSrc); err != nil {
		return Result{}, fmt.Errorf("exec.CreateTable: %w", err)
	}
	return Result{RowsAffected: 1}, nil
}

// runInsert binds args to value expressions, validates them against
// the schema, encodes the row, and writes via the table's B+tree.
// If the tree's root grows mid-insert (a root split), we persist the
// new root id via catalog.SetTableRoot so the change survives reopen.
func (e *Executor) runInsert(p *planner.InsertPlan, args []any) (Result, error) {
	info, err := e.catalog.LookupTable(p.Table)
	if err != nil {
		return Result{}, fmt.Errorf("exec.Insert: %w", err)
	}
	// Build the full row in schema order. For inserts without an
	// explicit column list, values are already in schema order.
	// For explicit lists, each user-specified value goes into its
	// resolved schema slot; un-specified columns are NULL (or error
	// if NOT NULL — the schema validation handles that).
	row := make([]record.Value, len(p.Schema.Columns))
	for i := range row {
		row[i] = record.Null()
	}
	bound, err := bindArgs(p.Values, args)
	if err != nil {
		return Result{}, err
	}
	if p.Columns == nil {
		copy(row, bound)
	} else {
		for i, schemaIdx := range p.Columns {
			row[schemaIdx] = bound[i]
		}
	}
	// Schema-level validation: column count is by construction OK;
	// nullability and type must be checked. record.Schema.Validate
	// handles both.
	if err := p.Schema.Validate(row); err != nil {
		return Result{}, mapSchemaErr(err)
	}
	// Extract the primary key (the planner guaranteed exactly one
	// INTEGER PK column).
	pkIdx, ok := primaryKeyIndex(p.Schema)
	if !ok {
		return Result{}, fmt.Errorf("exec.Insert: catalog corruption — table %q has no PK", p.Table)
	}
	pk := uint64(row[pkIdx].Int)

	payload, err := record.EncodeRow(row)
	if err != nil {
		return Result{}, fmt.Errorf("exec.Insert: encode row: %w", err)
	}
	tree := btree.Open(e.pager, info.RootPageID)
	if err := tree.Insert(pk, payload); err != nil {
		if errors.Is(err, btree.ErrDuplicateKey) {
			return Result{}, fmt.Errorf("%w: key=%d in %q", ErrDuplicatePrimaryKey, pk, p.Table)
		}
		return Result{}, fmt.Errorf("exec.Insert: tree.Insert: %w", err)
	}
	// Persist root drift if the tree grew a new root via split.
	if tree.RootPageID() != info.RootPageID {
		if err := e.catalog.SetTableRoot(p.Table, tree.RootPageID()); err != nil {
			return Result{}, fmt.Errorf("exec.Insert: persist new root: %w", err)
		}
	}
	return Result{RowsAffected: 1, LastInsertID: int64(pk)}, nil
}

// runTableScan reads every row from the table tree in primary-key
// order. Materializes into Rows per ADR-0016.
func (e *Executor) runTableScan(p *planner.TableScanPlan) (*Rows, error) {
	// Re-resolve the root id in case it has drifted since the plan
	// was built (e.g. another Insert split the tree between planning
	// and execution). For single-statement Exec/Query this is rare,
	// but it's also cheap.
	info, err := e.catalog.LookupTable(p.Table)
	if err != nil {
		return nil, fmt.Errorf("exec.TableScan: %w", err)
	}
	tree := btree.Open(e.pager, info.RootPageID)
	out := &Rows{Columns: columnNames(p.Schema)}
	if err := tree.Scan(func(_ uint64, payload []byte) error {
		values, _, err := record.DecodeRow(payload)
		if err != nil {
			return fmt.Errorf("exec.TableScan: decode row: %w", err)
		}
		out.Values = append(out.Values, values)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// runPKLookup binds the key expression to an int64, calls Tree.Get,
// and returns 0 or 1 row.
func (e *Executor) runPKLookup(p *planner.PrimaryKeyLookupPlan, args []any) (*Rows, error) {
	bound, err := bindArgs([]sql.Expression{p.KeyExpr}, args)
	if err != nil {
		return nil, err
	}
	if bound[0].Kind != record.KindInteger {
		return nil, fmt.Errorf("%w: WHERE key for %q must be INTEGER, got %s",
			ErrTypeMismatch, p.Table, bound[0].Kind)
	}
	info, err := e.catalog.LookupTable(p.Table)
	if err != nil {
		return nil, fmt.Errorf("exec.PKLookup: %w", err)
	}
	tree := btree.Open(e.pager, info.RootPageID)
	payload, found, err := tree.Get(uint64(bound[0].Int))
	if err != nil {
		return nil, err
	}
	out := &Rows{Columns: columnNames(p.Schema)}
	if !found {
		return out, nil
	}
	values, _, err := record.DecodeRow(payload)
	if err != nil {
		return nil, fmt.Errorf("exec.PKLookup: decode row: %w", err)
	}
	out.Values = append(out.Values, values)
	return out, nil
}

// runProjection runs the inner plan and picks out the projected
// columns by index.
func (e *Executor) runProjection(p *planner.ProjectionPlan, args []any) (*Rows, error) {
	inner, err := e.RunQuery(p.Input, args)
	if err != nil {
		return nil, err
	}
	out := &Rows{Columns: p.Names}
	for _, row := range inner.Values {
		projected := make([]record.Value, len(p.Output))
		for i, idx := range p.Output {
			projected[i] = row[idx]
		}
		out.Values = append(out.Values, projected)
	}
	return out, nil
}

// bindArgs converts the value-expression list (from an InsertPlan's
// Values or a PKLookupPlan's KeyExpr) into record.Values. Placeholders
// consume one arg in occurrence order; literals pass through.
func bindArgs(exprs []sql.Expression, args []any) ([]record.Value, error) {
	out := make([]record.Value, len(exprs))
	argIdx := 0
	for i, expr := range exprs {
		switch e := expr.(type) {
		case *sql.IntegerLiteral:
			out[i] = record.Int(e.Value)
		case *sql.StringLiteral:
			out[i] = record.Text(e.Value)
		case *sql.BooleanLiteral:
			out[i] = record.Bool(e.Value)
		case *sql.NullLiteral:
			out[i] = record.Null()
		case *sql.Placeholder:
			if argIdx >= len(args) {
				return nil, fmt.Errorf("%w: too few args (need at least %d)", ErrPlaceholderCountMismatch, argIdx+1)
			}
			v, err := argToValue(args[argIdx])
			if err != nil {
				return nil, err
			}
			out[i] = v
			argIdx++
		case *sql.Identifier:
			// Identifiers appear here only in WHERE = <identifier>
			// (the parser allows this; v0.1 executor doesn't handle
			// column-to-column equality at runtime). Reject loudly.
			return nil, fmt.Errorf("%w: column reference %q as a value is not supported in v0.1", ErrUnsupportedPlan, e.Name)
		default:
			return nil, fmt.Errorf("%w: %T", ErrUnsupportedPlan, expr)
		}
	}
	if argIdx != len(args) {
		return nil, fmt.Errorf("%w: %d placeholders consumed, %d args provided", ErrPlaceholderCountMismatch, argIdx, len(args))
	}
	return out, nil
}

// argToValue converts a single Go arg to record.Value. Strict typing
// in v0.1: int/int32/int64 → Int, string → Text, bool → Bool, nil → Null.
// Everything else is rejected.
func argToValue(arg any) (record.Value, error) {
	switch v := arg.(type) {
	case nil:
		return record.Null(), nil
	case int:
		return record.Int(int64(v)), nil
	case int32:
		return record.Int(int64(v)), nil
	case int64:
		return record.Int(v), nil
	case string:
		return record.Text(v), nil
	case bool:
		return record.Bool(v), nil
	default:
		return record.Value{}, fmt.Errorf("%w: %T", ErrUnsupportedArgType, arg)
	}
}

// mapSchemaErr translates record.Schema.Validate's errors into the
// executor's sentinels so callers above can dispatch on them.
func mapSchemaErr(err error) error {
	switch {
	case errors.Is(err, record.ErrNullViolation):
		return fmt.Errorf("%w: %s", ErrNullViolation, err)
	case errors.Is(err, record.ErrTypeMismatch):
		return fmt.Errorf("%w: %s", ErrTypeMismatch, err)
	}
	return err
}

// primaryKeyIndex returns the index of the (single) PK column.
func primaryKeyIndex(s record.Schema) (int, bool) {
	for i, c := range s.Columns {
		if c.PrimaryKey {
			return i, true
		}
	}
	return 0, false
}

// columnNames returns the schema's column names in order.
func columnNames(s record.Schema) []string {
	out := make([]string, len(s.Columns))
	for i, c := range s.Columns {
		out[i] = c.Name
	}
	return out
}

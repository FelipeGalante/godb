package godb

import (
	"context"
	"errors"
	"fmt"

	"github.com/felipegalante/godb/internal/exec"
	"github.com/felipegalante/godb/internal/record"
	"github.com/felipegalante/godb/internal/sql"
)

// Rows is the materialized result set returned by Query. Iterate via
// Next, decode into typed destinations via Scan. Close releases
// internal references; calling Close on the same *Rows twice is
// safe.
//
// In v0.1 Rows is fully materialized in memory (see ADR-0016); the
// public API doesn't change when v0.2 switches to streaming.
type Rows struct {
	columns []string
	values  [][]record.Value
	idx     int // next-to-be-returned row index; starts at -1 (before any Next call)
	err     error
	closed  bool
}

// Query runs a SELECT statement and returns a *Rows. Args are bound
// to ? placeholders in occurrence order. The result is fully
// materialized before Query returns.
func (db *DB) Query(ctx context.Context, sqlSrc string, args ...any) (*Rows, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.guardOpen(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("godb.Query: %w", err)
	}
	stmt, err := sql.Parse(sqlSrc)
	if err != nil {
		return nil, translateSQLErr(err)
	}
	if _, isSelect := stmt.(*sql.SelectStatement); !isSelect {
		return nil, fmt.Errorf("godb.Query: only SELECT statements are supported; use Exec for DDL/DML")
	}
	plan, err := db.planner.Plan(stmt)
	if err != nil {
		return nil, mapInternalErr(err)
	}
	execRows, err := db.executor.RunQuery(plan, args)
	if err != nil {
		return nil, mapInternalErr(err)
	}
	return newRows(execRows), nil
}

func newRows(r *exec.Rows) *Rows {
	return &Rows{
		columns: r.Columns,
		values:  r.Values,
		idx:     -1,
	}
}

// Columns returns the names of the result's columns in output order.
func (r *Rows) Columns() []string {
	return append([]string(nil), r.columns...)
}

// Next advances to the next row; the first Next call moves to row 0.
// Returns false when there are no more rows OR when an error has
// occurred (check Err to distinguish).
func (r *Rows) Next() bool {
	if r.closed || r.err != nil {
		return false
	}
	r.idx++
	return r.idx < len(r.values)
}

// Scan copies the current row's values into dest. dest must be a
// list of pointer destinations equal in number to len(Columns()).
// Supported destination types in v0.1:
//
//   - *int64       — INTEGER columns. NULL → ErrScanNullIntoNonNullable.
//   - *string      — TEXT columns. NULL → ErrScanNullIntoNonNullable.
//   - *bool        — BOOLEAN columns. NULL → ErrScanNullIntoNonNullable.
//   - *any (i.e. *interface{}) — any kind, including NULL (becomes nil).
//
// Type mismatches return ErrScanTypeMismatch.
func (r *Rows) Scan(dest ...any) error {
	if r.closed {
		return errors.New("godb: Scan on closed Rows")
	}
	if r.idx < 0 || r.idx >= len(r.values) {
		return errors.New("godb: Scan without Next or after Next returned false")
	}
	if len(dest) != len(r.columns) {
		return fmt.Errorf("%w: %d destinations, %d columns", ErrScanWrongCount, len(dest), len(r.columns))
	}
	row := r.values[r.idx]
	for i, d := range dest {
		if err := scanValueInto(d, row[i]); err != nil {
			r.err = err
			return err
		}
	}
	return nil
}

// scanValueInto writes one record.Value into one Go destination
// pointer. Strict typing: no implicit conversions.
func scanValueInto(dest any, v record.Value) error {
	switch p := dest.(type) {
	case *int64:
		if v.IsNull() {
			return fmt.Errorf("%w: *int64", ErrScanNullIntoNonNullable)
		}
		if v.Kind != record.KindInteger {
			return fmt.Errorf("%w: column is %s, destination is *int64", ErrScanTypeMismatch, v.Kind)
		}
		*p = v.Int
		return nil
	case *string:
		if v.IsNull() {
			return fmt.Errorf("%w: *string", ErrScanNullIntoNonNullable)
		}
		if v.Kind != record.KindText {
			return fmt.Errorf("%w: column is %s, destination is *string", ErrScanTypeMismatch, v.Kind)
		}
		*p = v.Text
		return nil
	case *bool:
		if v.IsNull() {
			return fmt.Errorf("%w: *bool", ErrScanNullIntoNonNullable)
		}
		if v.Kind != record.KindBoolean {
			return fmt.Errorf("%w: column is %s, destination is *bool", ErrScanTypeMismatch, v.Kind)
		}
		*p = v.Bool
		return nil
	case *any:
		if v.IsNull() {
			*p = nil
			return nil
		}
		switch v.Kind {
		case record.KindInteger:
			*p = v.Int
		case record.KindText:
			*p = v.Text
		case record.KindBoolean:
			*p = v.Bool
		default:
			return fmt.Errorf("%w: unknown column kind %s", ErrScanTypeMismatch, v.Kind)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported destination type %T", ErrScanTypeMismatch, dest)
	}
}

// Err returns the last non-nil error from Scan, or nil if Scan
// hasn't returned an error.
func (r *Rows) Err() error { return r.err }

// Close releases internal references. Idempotent.
func (r *Rows) Close() error {
	r.closed = true
	r.values = nil
	r.columns = nil
	return nil
}

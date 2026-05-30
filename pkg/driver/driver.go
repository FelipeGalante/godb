// Package driver registers a database/sql/driver for GoDB.
//
// Importing this package for side effects registers the driver under
// the name "godb":
//
//	import (
//	    "database/sql"
//	    _ "github.com/felipegalante/godb/pkg/driver"
//	)
//
//	db, _ := sql.Open("godb", "app.godb")
//	defer db.Close()
//	db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL)")
//	rows, _ := db.Query("SELECT * FROM users WHERE id = ?", 1)
//
// The wrapper is thin: a Conn holds one *godb.DB; Stmt remembers its
// SQL text (no parse caching in v0.1); Rows iterates a materialized
// *godb.Rows. Public sentinels (godb.ErrXxx) flow through
// errors.Is so callers dispatch on stable identifiers regardless of
// the path (native pkg/godb or via database/sql).
//
// The layering (this package wraps pkg/godb; pkg/godb does not wrap
// database/sql) is recorded in ADR-0019.
package driver

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"fmt"
	"io"
	"time"

	"github.com/felipegalante/godb/pkg/godb"
)

func init() {
	sql.Register("godb", &Driver{})
}

// Driver implements database/sql/driver.Driver. One Driver value can
// open many Conns; database/sql's pool will call Open as needed.
type Driver struct{}

// Open opens a new connection. name is the path to a .godb file (the
// "DSN" in database/sql terms — for v0.1 just a path, no query
// parameters). Returns an *sql.DB-compatible Conn that wraps a fresh
// *godb.DB.
func (*Driver) Open(name string) (sqldriver.Conn, error) {
	db, err := godb.Open(name)
	if err != nil {
		return nil, fmt.Errorf("driver.Open(%q): %w", name, err)
	}
	return &Conn{db: db}, nil
}

// Conn implements database/sql/driver.Conn. Each Conn owns one godb.DB.
// database/sql's pool will create several of these; for the v0.1
// single-writer model that's not ideal (each conn is a separate pager),
// but it's the standard shape. Users who want strict single-writer
// semantics should call sql.DB.SetMaxOpenConns(1).
type Conn struct {
	db *godb.DB
}

// Prepare returns a Stmt that holds the SQL text. No parse caching in
// v0.1 — each Exec/Query on the Stmt re-parses. database/sql often
// reuses prepared Stmts across calls so the SQL text only travels the
// wire once.
func (c *Conn) Prepare(query string) (sqldriver.Stmt, error) {
	return &Stmt{conn: c, sql: query}, nil
}

// Close closes the underlying godb.DB.
func (c *Conn) Close() error {
	return c.db.Close()
}

// Begin would start a transaction. In v0.1 it returns an error
// wrapping godb.ErrTransactionsUnsupported. See ADR-0017.
//
// We deliberately do NOT return driver.ErrSkip — that would mean
// "try the next driver" which doesn't apply here.
func (c *Conn) Begin() (sqldriver.Tx, error) {
	return nil, godb.ErrTransactionsUnsupported
}

// Stmt implements database/sql/driver.Stmt. Holds the SQL text +
// a connection reference. Each Exec/Query routes through the
// underlying godb.DB.
type Stmt struct {
	conn *Conn
	sql  string
}

// Close is a no-op in v0.1 — there's no parse cache to release.
func (s *Stmt) Close() error { return nil }

// NumInput returns -1 to signal that database/sql shouldn't validate
// arg count up-front. The underlying parser + planner will check.
func (s *Stmt) NumInput() int { return -1 }

// Exec runs the statement as a non-query (Result, no Rows).
func (s *Stmt) Exec(args []sqldriver.Value) (sqldriver.Result, error) {
	return s.ExecContext(context.Background(), valuesToNamed(args))
}

// ExecContext is the context-aware variant. Implementing
// driver.StmtExecContext means database/sql calls this directly when
// the user uses ExecContext, instead of stripping the context.
func (s *Stmt) ExecContext(ctx context.Context, args []sqldriver.NamedValue) (sqldriver.Result, error) {
	goArgs, err := namedToAny(args)
	if err != nil {
		return nil, err
	}
	res, err := s.conn.db.Exec(ctx, s.sql, goArgs...)
	if err != nil {
		return nil, err
	}
	return Result{lastID: res.LastInsertID, rowsAff: res.RowsAffected}, nil
}

// Query runs the statement as a query (Rows).
func (s *Stmt) Query(args []sqldriver.Value) (sqldriver.Rows, error) {
	return s.QueryContext(context.Background(), valuesToNamed(args))
}

// QueryContext is the context-aware variant.
func (s *Stmt) QueryContext(ctx context.Context, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	goArgs, err := namedToAny(args)
	if err != nil {
		return nil, err
	}
	rows, err := s.conn.db.Query(ctx, s.sql, goArgs...)
	if err != nil {
		return nil, err
	}
	return &Rows{inner: rows}, nil
}

// Rows implements database/sql/driver.Rows over a *godb.Rows. Each
// Next call advances the underlying Rows; database/sql then maps the
// dest values into the user's Scan destinations.
type Rows struct {
	inner *godb.Rows
}

// Columns returns the column names of the result set.
func (r *Rows) Columns() []string {
	return r.inner.Columns()
}

// Close releases the underlying Rows.
func (r *Rows) Close() error {
	return r.inner.Close()
}

// Next advances the underlying Rows and copies the current row into
// dest. database/sql allocates dest with len == len(Columns()).
//
// When there are no more rows, Next returns io.EOF (the
// database/sql/driver convention; sql.Rows translates that into a
// Next() == false on the consumer side).
func (r *Rows) Next(dest []sqldriver.Value) error {
	if !r.inner.Next() {
		if err := r.inner.Err(); err != nil {
			return err
		}
		return io.EOF
	}
	scanDest := make([]any, len(dest))
	holders := make([]any, len(dest))
	for i := range scanDest {
		// Use *any (i.e. *interface{}) destinations so godb.Rows.Scan
		// returns the typed value for each column without us having
		// to know the column kind up front. database/sql will then
		// convert to the user's Scan destination type.
		scanDest[i] = &holders[i]
	}
	if err := r.inner.Scan(scanDest...); err != nil {
		return err
	}
	for i, v := range holders {
		dest[i] = toDriverValue(v)
	}
	return nil
}

// Result implements database/sql/driver.Result.
type Result struct {
	lastID  int64
	rowsAff int64
}

// LastInsertId returns the row id of the last INSERT.
func (r Result) LastInsertId() (int64, error) { return r.lastID, nil }

// RowsAffected returns the number of rows affected.
func (r Result) RowsAffected() (int64, error) { return r.rowsAff, nil }

// --- value mapping ------------------------------------------------------

// valuesToNamed converts the legacy driver.Value slice into the
// NamedValue slice the *Context variants expect. Each value gets a
// 1-indexed Ordinal and an empty Name (positional argument).
func valuesToNamed(args []sqldriver.Value) []sqldriver.NamedValue {
	out := make([]sqldriver.NamedValue, len(args))
	for i, v := range args {
		out[i] = sqldriver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return out
}

// namedToAny converts driver.NamedValue (driver-flavored values) into
// the []any pkg/godb.Exec/Query expects. database/sql validates value
// types against driver.IsValue before passing them in, so we only need
// to handle the types pkg/godb supports: int64, string, bool, nil.
// Anything else is a v0.1 limitation we surface clearly.
func namedToAny(args []sqldriver.NamedValue) ([]any, error) {
	out := make([]any, len(args))
	for i, a := range args {
		if a.Name != "" {
			return nil, fmt.Errorf("driver: named parameters are not supported in GoDB v0.1 (got name=%q)", a.Name)
		}
		switch v := a.Value.(type) {
		case nil, int64, string, bool:
			out[i] = v
		case float64:
			return nil, fmt.Errorf("driver: float64 args are not supported in GoDB v0.1 (column types are INTEGER/TEXT/BOOLEAN)")
		case []byte:
			return nil, fmt.Errorf("driver: []byte args are not supported in GoDB v0.1 (no BLOB column type yet)")
		case time.Time:
			return nil, fmt.Errorf("driver: time.Time args are not supported in GoDB v0.1 (no TIMESTAMP column type yet)")
		default:
			return nil, fmt.Errorf("driver: unsupported arg type %T at position %d", v, i+1)
		}
	}
	return out, nil
}

// toDriverValue converts a value coming out of godb.Rows.Scan (which
// uses *any destinations and so produces int64 / string / bool / nil)
// into the driver.Value the database/sql layer expects. The set
// happens to be identical, so this is mostly a type assertion guard.
func toDriverValue(v any) sqldriver.Value {
	switch x := v.(type) {
	case nil:
		return nil
	case int64:
		return x
	case string:
		return x
	case bool:
		return x
	default:
		// Shouldn't happen given godb.Rows.Scan's contract, but
		// return as-is and let database/sql complain if it doesn't
		// match driver.IsValue.
		return v
	}
}

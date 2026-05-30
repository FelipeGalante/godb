// Package godb is the public Go API for the GoDB embedded database
// engine. Applications import this package; the internal layers
// (storage, btree, catalog, sql, planner, exec) are not directly
// importable from outside the module.
//
// The shape mirrors database/sql in spirit (Open, Exec, Query, Rows,
// Scan, Begin/Tx) so users familiar with that package have minimal
// surprise. The actual database/sql/driver wrapper is a separate
// (later) deliverable; for now godb.DB is its own type.
package godb

import "errors"

// All errors returned from godb wrap (via errors.Is) one of these
// sentinels. Callers should use errors.Is(err, godb.ErrXxx) for
// dispatch, never string matching.
var (
	// ErrDatabaseClosed is returned from any operation on a DB whose
	// Close has been called.
	ErrDatabaseClosed = errors.New("godb: database closed")

	// ErrTransactionsUnsupported is returned by DB.Begin in v0.1.
	// v0.2 will introduce real transactions via the rollback journal.
	ErrTransactionsUnsupported = errors.New("godb: transactions are not supported in v0.1")

	// ErrTableNotFound is returned when a referenced table doesn't
	// exist.
	ErrTableNotFound = errors.New("godb: table not found")

	// ErrTableExists is returned by CREATE TABLE when the name is
	// already taken.
	ErrTableExists = errors.New("godb: table already exists")

	// ErrColumnNotFound is returned when a referenced column doesn't
	// exist in the resolved table.
	ErrColumnNotFound = errors.New("godb: column not found")

	// ErrTypeMismatch is returned when an argument type or a row
	// value's kind doesn't match the column's declared kind. v0.1
	// does no implicit conversions.
	ErrTypeMismatch = errors.New("godb: type mismatch")

	// ErrDuplicatePrimaryKey is returned by INSERT when the key is
	// already present in the table.
	ErrDuplicatePrimaryKey = errors.New("godb: duplicate primary key")

	// ErrNullViolation is returned when a NULL value would be
	// inserted into a NOT NULL column.
	ErrNullViolation = errors.New("godb: NULL into NOT NULL column")

	// ErrUnsupportedSQL is returned when the parser recognizes a
	// SQL feature that GoDB v0.1 doesn't support.
	ErrUnsupportedSQL = errors.New("godb: unsupported SQL feature")

	// ErrWhereOnlyPrimaryKey is returned by SELECT WHERE on any
	// column other than the primary key. v0.2 will lift this with
	// TableScan + Filter.
	ErrWhereOnlyPrimaryKey = errors.New("godb: v0.1 only supports WHERE on the primary key")

	// ErrInvalidSchema is returned by CREATE TABLE for shape
	// problems the parser doesn't catch (no PK, multiple PKs,
	// non-INTEGER PK in v0.1).
	ErrInvalidSchema = errors.New("godb: invalid table schema")

	// ErrInsertCountMismatch is returned when INSERT's value count
	// doesn't match the (explicit or implicit) column count.
	ErrInsertCountMismatch = errors.New("godb: INSERT value count does not match column count")

	// ErrPlaceholderCountMismatch is returned when the number of ?
	// placeholders in the SQL doesn't match the number of args
	// passed to Exec / Query.
	ErrPlaceholderCountMismatch = errors.New("godb: placeholder count does not match args count")

	// ErrUnsupportedArgType is returned for arg types other than
	// int / int32 / int64 / string / bool / nil.
	ErrUnsupportedArgType = errors.New("godb: unsupported argument type")

	// ErrScanWrongCount is returned when the number of Scan
	// destinations doesn't match the result's column count.
	ErrScanWrongCount = errors.New("godb: scan destination count does not match column count")

	// ErrScanTypeMismatch is returned when a Scan destination's Go
	// type doesn't match the column's value kind. v0.1 is strict; no
	// implicit conversions like database/sql allows.
	ErrScanTypeMismatch = errors.New("godb: scan destination type mismatches column type")

	// ErrScanNullIntoNonNullable is returned when a NULL value would
	// be scanned into a destination that can't hold NULL (anything
	// other than *any/*interface{}).
	ErrScanNullIntoNonNullable = errors.New("godb: cannot scan NULL into non-nullable destination")
)

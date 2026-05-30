package sql

import (
	"errors"
	"fmt"
)

// ErrSyntax is the sentinel for "this byte stream is not syntactically
// valid SQL." Returned by both the lexer (unknown character,
// unterminated string, etc.) and the parser (missing token, wrong
// shape). Always wrapped in a *SQLError carrying the source position.
var ErrSyntax = errors.New("sql: syntax error")

// ErrUnsupportedSQL is the sentinel for "this is well-formed SQL but
// the GoDB v0.1 subset doesn't include it." JOIN, GROUP BY, UPDATE,
// DELETE, AND/OR in WHERE, comparison operators other than '=', etc.
// Callers can errors.Is(err, ErrUnsupportedSQL) to distinguish this
// from a plain syntax error.
var ErrUnsupportedSQL = errors.New("sql: unsupported SQL feature")

// SQLError attaches a source position and a human-readable message to
// one of the sentinel errors above. Callers shouldn't construct this
// directly; the lexer and parser produce them.
type SQLError struct {
	Sentinel error // ErrSyntax or ErrUnsupportedSQL
	Message  string
	Pos      Position
}

func (e *SQLError) Error() string {
	what := "syntax error"
	if errors.Is(e.Sentinel, ErrUnsupportedSQL) {
		what = "unsupported SQL feature"
	}
	return fmt.Sprintf("sql: %s at line %d, column %d: %s",
		what, e.Pos.Line, e.Pos.Column, e.Message)
}

// Unwrap so errors.Is(err, ErrSyntax) and errors.Is(err, ErrUnsupportedSQL) work.
func (e *SQLError) Unwrap() error { return e.Sentinel }

// newSyntaxError produces a *SQLError wrapping ErrSyntax.
func newSyntaxError(pos Position, format string, args ...any) *SQLError {
	return &SQLError{
		Sentinel: ErrSyntax,
		Message:  fmt.Sprintf(format, args...),
		Pos:      pos,
	}
}

// newUnsupportedError produces a *SQLError wrapping ErrUnsupportedSQL.
func newUnsupportedError(pos Position, format string, args ...any) *SQLError {
	return &SQLError{
		Sentinel: ErrUnsupportedSQL,
		Message:  fmt.Sprintf(format, args...),
		Pos:      pos,
	}
}

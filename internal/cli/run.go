package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/felipegalante/godb/internal/sql"
	"github.com/felipegalante/godb/pkg/godb"
)

// openDB opens the database at path through the public API. When
// create is false a missing file yields an error rather than a freshly
// created empty database — appropriate for read-only commands.
func openDB(path string, create bool) (*godb.DB, error) {
	return godb.Open(path, godb.WithCreateIfMissing(create))
}

type stmtKind int

const (
	stmtSkip  stmtKind = iota // only whitespace/comments
	stmtQuery                 // SELECT
	stmtExec                  // everything else (incl. unlexable — let the parser report it)
)

// classify decides how to route a statement by looking at its first
// token.
func classify(src string) stmtKind {
	tok, err := sql.NewLexer(src).Next()
	switch {
	case err == nil && tok.Type == sql.TokenEOF:
		return stmtSkip
	case err == nil && tok.Type == sql.TokenKeywordSelect:
		return stmtQuery
	default:
		return stmtExec
	}
}

// runStatement executes one SQL statement, routing SELECT to Query
// (rendered to out) and everything else to Exec (status to info).
// Statements that contain only whitespace/comments are skipped.
func runStatement(db *godb.DB, src string, out, info io.Writer, format outputFormat) error {
	switch classify(src) {
	case stmtSkip:
		return nil
	case stmtQuery:
		rows, err := db.Query(context.Background(), src)
		if err != nil {
			return err
		}
		defer rows.Close()
		n, err := renderRows(out, rows, format)
		if err != nil {
			return err
		}
		fmt.Fprintf(info, "(%d row%s)\n", n, plural(n))
		return nil
	default:
		res, err := db.Exec(context.Background(), src)
		if err != nil {
			return err
		}
		fmt.Fprintf(info, "ok (%d row%s affected, last insert id %d)\n",
			res.RowsAffected, plural(res.RowsAffected), res.LastInsertID)
		return nil
	}
}

func plural[T ~int | ~int64](n T) string {
	if n == 1 {
		return ""
	}
	return "s"
}

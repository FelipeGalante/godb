package godb

import (
	"context"
	"errors"
	"fmt"

	"github.com/felipegalante/godb/internal/sql"
)

// Result is returned by Exec for statements that don't produce row
// data (CREATE TABLE, INSERT).
type Result struct {
	RowsAffected int64
	LastInsertID int64
}

// Exec runs a single SQL statement that doesn't return rows (CREATE
// TABLE, INSERT). For queries that produce rows, use Query.
//
// Args are bound to ? placeholders in occurrence order. Supported arg
// types: int, int32, int64, string, bool, nil. Anything else returns
// ErrUnsupportedArgType.
//
// Exec checks ctx.Err() at entry; deeper cancellation isn't honored
// in v0.1 (most operations are short-lived against a single pager).
func (db *DB) Exec(ctx context.Context, sqlSrc string, args ...any) (Result, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.guardOpen(); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("godb.Exec: %w", err)
	}
	stmt, err := sql.Parse(sqlSrc)
	if err != nil {
		return Result{}, wrapStatementErr(sqlSrc, translateSQLErr(err))
	}
	plan, err := db.planner.Plan(stmt)
	if err != nil {
		return Result{}, wrapStatementErr(sqlSrc, mapInternalErr(err))
	}
	res, err := db.executor.Run(plan, args, sqlSrc)
	if err != nil {
		return Result{}, wrapStatementErr(sqlSrc, mapInternalErr(err))
	}
	return Result{
		RowsAffected: res.RowsAffected,
		LastInsertID: res.LastInsertID,
	}, nil
}

// translateSQLErr maps internal/sql errors into the public sentinels
// while preserving the original *sql.SQLError (which carries the
// source position) via errors.As.
func translateSQLErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrUnsupportedSQL) {
		return fmt.Errorf("%w: %s", ErrUnsupportedSQL, err.Error())
	}
	// Syntax errors (sql.ErrSyntax) pass through as-is so callers can
	// still errors.As into *sql.SQLError for the source position;
	// most callers don't need to distinguish syntax from unsupported.
	return err
}

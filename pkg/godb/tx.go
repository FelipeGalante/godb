package godb

import "context"

// Tx is a transaction handle. It exists in the v0.1 API surface but
// is unreachable — DB.Begin always returns (nil, ErrTransactionsUnsupported).
// Real transactions arrive in v0.2 alongside the rollback journal;
// see ADR-0017.
//
// Tx's methods are declared so that v0.2 can implement them without
// expanding the public API surface, but callers in v0.1 should not
// receive a non-nil *Tx and so will not call any of these.
type Tx struct {
	_ struct{} // prevent zero-value construction outside the package
}

// Begin would start a transaction. In v0.1 it always returns
// (nil, ErrTransactionsUnsupported). v0.2 will implement real
// transactions.
//
// Callers that want forward-compatibility with v0.2 should still
// route writes through Begin/Tx in their code; on v0.1 they'll need
// to fall back to direct DB.Exec/DB.Query in autocommit mode (which
// is fully supported).
func (db *DB) Begin(ctx context.Context) (*Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrTransactionsUnsupported
}

// Exec on Tx is declared for v0.2; in v0.1 it would be unreachable
// because Begin never returns a non-nil Tx. If somehow called, it
// returns the same sentinel.
func (tx *Tx) Exec(ctx context.Context, sql string, args ...any) (Result, error) {
	return Result{}, ErrTransactionsUnsupported
}

// Query on Tx — same v0.2-only story as Exec.
func (tx *Tx) Query(ctx context.Context, sql string, args ...any) (*Rows, error) {
	return nil, ErrTransactionsUnsupported
}

// Commit — v0.2 only.
func (tx *Tx) Commit() error { return ErrTransactionsUnsupported }

// Rollback — v0.2 only.
func (tx *Tx) Rollback() error { return ErrTransactionsUnsupported }

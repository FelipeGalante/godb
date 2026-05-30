package godb

import (
	"errors"
	"fmt"
	"sync"

	"github.com/felipegalante/godb/internal/catalog"
	"github.com/felipegalante/godb/internal/exec"
	"github.com/felipegalante/godb/internal/planner"
	"github.com/felipegalante/godb/internal/storage"
)

// DB is a handle to an open GoDB database. A DB is safe for use from
// multiple goroutines per the v0.1 single-writer model — the pager
// serializes all I/O — but writes do not have proper isolation
// (transactions are v0.2).
//
// A DB owns its pager and catalog and is responsible for closing
// them. Always defer db.Close() after a successful Open.
type DB struct {
	mu       sync.Mutex
	closed   bool
	pager    *storage.Pager
	catalog  *catalog.Catalog
	planner  *planner.Planner
	executor *exec.Executor
}

// Open opens (or creates, by default) a GoDB database at path.
// Apply options to override defaults; see WithCreateIfMissing.
//
// Open initializes the pager, opens the catalog (which bootstraps
// itself on a fresh database), and wires up the planner + executor.
// The returned *DB is ready for Exec and Query.
func Open(path string, opts ...Option) (*DB, error) {
	o := defaultOptions()
	for _, fn := range opts {
		fn(&o)
	}
	pager, err := storage.OpenPager(path, storage.PagerOptions{
		CreateIfMissing: o.createIfMissing,
	})
	if err != nil {
		return nil, fmt.Errorf("godb.Open: %w", err)
	}
	cat, err := catalog.Open(pager)
	if err != nil {
		_ = pager.Close()
		return nil, fmt.Errorf("godb.Open: %w", err)
	}
	return &DB{
		pager:    pager,
		catalog:  cat,
		planner:  planner.New(cat),
		executor: exec.New(pager, cat),
	}, nil
}

// Close syncs pending writes and closes the database. Safe to call
// multiple times; subsequent operations on the DB return
// ErrDatabaseClosed.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil
	}
	db.closed = true
	// Sync the catalog first (refreshes the header's CatalogRootPageID).
	if err := db.catalog.Sync(); err != nil {
		_ = db.pager.Close()
		return fmt.Errorf("godb.Close: sync catalog: %w", err)
	}
	return db.pager.Close()
}

// Sync flushes pending writes to disk without closing the database.
// Useful for long-running processes that want a durability
// checkpoint between operations — without it, writes are durable only
// after Close.
//
// Sync refreshes the database header's catalog root id (in case
// catalog operations grew the catalog tree's root) and then fsyncs
// the underlying file. Returns ErrDatabaseClosed if Close has been
// called.
func (db *DB) Sync() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.guardOpen(); err != nil {
		return err
	}
	if err := db.catalog.Sync(); err != nil {
		return fmt.Errorf("godb.Sync: %w", err)
	}
	return nil
}

// guardOpen returns ErrDatabaseClosed if Close has been called.
// Callers should defer-unlock the returned closure (or call db.mu
// themselves). For convenience callers can use db.withLock(func()...).
func (db *DB) guardOpen() error {
	if db.closed {
		return ErrDatabaseClosed
	}
	return nil
}

// mapInternalErr translates internal-package sentinels into the
// public godb.Err* sentinels. The mapping is exhaustive for the v0.1
// supported feature set; unmapped errors pass through unchanged
// (callers can still inspect them with errors.As to *SQLError, etc.,
// but the common case is errors.Is(err, godb.ErrXxx)).
func mapInternalErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	// catalog
	case errors.Is(err, catalog.ErrTableExists):
		return fmt.Errorf("%w: %s", ErrTableExists, err.Error())
	case errors.Is(err, catalog.ErrTableNotFound):
		return fmt.Errorf("%w: %s", ErrTableNotFound, err.Error())
	// planner
	case errors.Is(err, planner.ErrTableExists):
		return fmt.Errorf("%w: %s", ErrTableExists, err.Error())
	case errors.Is(err, planner.ErrTableNotFound):
		return fmt.Errorf("%w: %s", ErrTableNotFound, err.Error())
	case errors.Is(err, planner.ErrColumnNotFound):
		return fmt.Errorf("%w: %s", ErrColumnNotFound, err.Error())
	case errors.Is(err, planner.ErrInvalidSchema):
		return fmt.Errorf("%w: %s", ErrInvalidSchema, err.Error())
	case errors.Is(err, planner.ErrWhereOnlyPrimaryKey):
		return fmt.Errorf("%w: %s", ErrWhereOnlyPrimaryKey, err.Error())
	case errors.Is(err, planner.ErrInsertCountMismatch):
		return fmt.Errorf("%w: %s", ErrInsertCountMismatch, err.Error())
	// exec
	case errors.Is(err, exec.ErrTypeMismatch):
		return fmt.Errorf("%w: %s", ErrTypeMismatch, err.Error())
	case errors.Is(err, exec.ErrNullViolation):
		return fmt.Errorf("%w: %s", ErrNullViolation, err.Error())
	case errors.Is(err, exec.ErrDuplicatePrimaryKey):
		return fmt.Errorf("%w: %s", ErrDuplicatePrimaryKey, err.Error())
	case errors.Is(err, exec.ErrPlaceholderCountMismatch):
		return fmt.Errorf("%w: %s", ErrPlaceholderCountMismatch, err.Error())
	case errors.Is(err, exec.ErrUnsupportedArgType):
		return fmt.Errorf("%w: %s", ErrUnsupportedArgType, err.Error())
	}
	return err
}

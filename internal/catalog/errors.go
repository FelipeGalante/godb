// Package catalog stores metadata about every table the database holds:
// names, root page ids, schemas, and the original CREATE statement.
//
// The catalog is itself a B+tree keyed on a monotonically-increasing
// object id, persisted alongside the table data in the same .godb
// file. The catalog's own root page id lives in the database header
// (Header.CatalogRootPageID) — that is the privileged slot that
// resolves the bootstrap problem ("the catalog stores all root ids;
// where does the catalog find its own root?").
//
// Catalog rows are encoded by the package-local codec (see codec.go);
// they are deliberately NOT encoded via internal/record because the
// variable-length column list does not fit the flat record-of-values
// shape cleanly. See ADR-0014.
package catalog

import "errors"

var (
	// ErrTableExists is returned by CreateTable when name is already in
	// use. The catalog state is unchanged on this error.
	ErrTableExists = errors.New("catalog: table already exists")

	// ErrTableNotFound is returned by LookupTable and SetTableRoot when
	// no object with the given name is registered.
	ErrTableNotFound = errors.New("catalog: table not found")

	// ErrInvalidName is returned by CreateTable for empty or
	// excessively long names. Character-set rules are deferred to the
	// SQL parser (M7); the catalog only enforces the minimums.
	ErrInvalidName = errors.New("catalog: invalid object name")

	// ErrInvalidObjectType is returned by DecodeObject when the type
	// byte is not a recognized ObjectType.
	ErrInvalidObjectType = errors.New("catalog: invalid object type")

	// ErrUnsupportedCatalogVersion is returned by DecodeObject when the
	// leading format-version byte is not the version this binary
	// supports. Doubles as a fence against accidentally walking a
	// pre-M6 .godb file (where the header's CatalogRootPageID pointed
	// at a regular table tree, not a catalog tree).
	ErrUnsupportedCatalogVersion = errors.New("catalog: unsupported catalog row format version")

	// ErrShortBuffer is returned by DecodeObject when the encoded byte
	// stream is truncated before all fields are read.
	ErrShortBuffer = errors.New("catalog: short buffer during decode")

	// ErrInvalidUTF8 is returned by DecodeObject when a name, sql, or
	// column name is not valid UTF-8.
	ErrInvalidUTF8 = errors.New("catalog: text field is not valid utf-8")
)

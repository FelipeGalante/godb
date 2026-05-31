package godb

import "sort"

// TableInfo is a read-only description of one table, intended for
// tooling (the godb CLI) that wants to list tables or recover their
// schema without going through SQL. It is a flattened view of the
// catalog's internal record.
type TableInfo struct {
	// Name is the table name as given in CREATE TABLE.
	Name string
	// SQL is the original CREATE TABLE statement text that defined the
	// table, suitable for re-execution (used by the CLI's dump).
	SQL string
}

// Tables returns the database's tables sorted by name. The result is a
// snapshot; it does not track later CREATE TABLE statements.
//
// Tables exists so tooling sharing a single open *DB can introspect the
// catalog — the pager has no cross-process lock, so opening a second
// handle to the same file would be an uncoordinated view.
func (db *DB) Tables() ([]TableInfo, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.guardOpen(); err != nil {
		return nil, err
	}
	infos := db.catalog.ListTables()
	out := make([]TableInfo, 0, len(infos))
	for _, t := range infos {
		out = append(out, TableInfo{Name: t.Name, SQL: t.SQL})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

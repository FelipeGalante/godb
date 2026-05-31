package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/felipegalante/godb/pkg/godb"
)

// runDump prints a SQL script that recreates the database: each table's
// CREATE statement followed by an INSERT per row. The output can be fed
// back through `godb <new.db> exec` to reload the data.
func runDump(dbPath string, out io.Writer) error {
	db, err := openDB(dbPath, false)
	if err != nil {
		return err
	}
	defer db.Close()
	return dumpAll(db, out)
}

// dumpAll writes the SQL reconstruction of an already-open database. It
// backs both the dump subcommand and the shell's .dump meta-command.
func dumpAll(db *godb.DB, out io.Writer) error {
	tables, err := db.Tables()
	if err != nil {
		return err
	}
	for _, t := range tables {
		create := strings.TrimRight(strings.TrimSpace(t.SQL), ";")
		fmt.Fprintf(out, "%s;\n", create)
		if err := dumpRows(db, t.Name, out); err != nil {
			return err
		}
	}
	return nil
}

func dumpRows(db *godb.DB, table string, out io.Writer) error {
	rows, err := db.Query(context.Background(), "SELECT * FROM "+table)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols := rows.Columns()
	colList := strings.Join(cols, ", ")
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		vals := make([]string, len(cols))
		for i, v := range cells {
			vals[i] = sqlLiteral(v)
		}
		fmt.Fprintf(out, "INSERT INTO %s (%s) VALUES (%s);\n", table, colList, strings.Join(vals, ", "))
	}
	return rows.Err()
}

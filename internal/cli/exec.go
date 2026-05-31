package cli

import (
	"fmt"
	"io"
	"os"
)

// runExecFile reads a SQL script and runs each statement in order
// against the database (created if missing, so a script can bootstrap a
// fresh file). Execution stops at the first failing statement, with its
// 1-based index in the error.
func runExecFile(dbPath, scriptPath string, stdout, stderr io.Writer, format outputFormat) error {
	src, err := os.ReadFile(scriptPath)
	if err != nil {
		return err
	}
	db, err := openDB(dbPath, true)
	if err != nil {
		return err
	}
	defer db.Close()

	stmts := splitStatements(string(src))
	for i, stmt := range stmts {
		if err := runStatement(db, stmt, stdout, stderr, format); err != nil {
			return fmt.Errorf("statement %d: %w", i+1, err)
		}
	}
	return nil
}

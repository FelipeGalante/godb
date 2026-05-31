package cli

import "io"

// runQuery executes a single inline SQL statement against an existing
// database. Row data goes to stdout; the row/affected count goes to
// stderr so stdout stays pipe-clean.
func runQuery(dbPath, src string, stdout, stderr io.Writer, format outputFormat) error {
	db, err := openDB(dbPath, false)
	if err != nil {
		return err
	}
	defer db.Close()
	return runStatement(db, src, stdout, stderr, format)
}

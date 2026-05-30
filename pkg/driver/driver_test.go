package driver_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	// Importing for side effects registers the "godb" driver.
	_ "github.com/felipegalante/godb/pkg/driver"
	"github.com/felipegalante/godb/pkg/godb"
)

// openDB opens a fresh database at a temp path via sql.Open. Cleanup
// closes the *sql.DB.
func openDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "driver.godb")
	db, err := sql.Open("godb", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestDriverOpenClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "open.godb")
	db, err := sql.Open("godb", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	// sql.Open is lazy; ping forces a real Open.
	if err := db.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestDriverExecCreateInsert(t *testing.T) {
	db := openDB(t)
	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	res, err := db.Exec(`INSERT INTO users VALUES (?, ?)`, int64(1), "Felipe")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil || id != 1 {
		t.Errorf("LastInsertId = %d, err = %v; want 1, nil", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		t.Errorf("RowsAffected = %d, err = %v; want 1, nil", n, err)
	}
}

func TestDriverQueryAndScan(t *testing.T) {
	db := openDB(t)
	mustExec(t, db, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, active BOOLEAN)`)
	mustExec(t, db, `INSERT INTO users VALUES (?, ?, ?)`, int64(1), "Felipe", true)
	mustExec(t, db, `INSERT INTO users VALUES (?, ?, ?)`, int64(2), "MG", false)

	rows, err := db.Query(`SELECT * FROM users`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()

	type r struct {
		id     int64
		name   string
		active bool
	}
	var got []r
	for rows.Next() {
		var x r
		if err := rows.Scan(&x.id, &x.name, &x.active); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		got = append(got, x)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	want := []r{{1, "Felipe", true}, {2, "MG", false}}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDriverPreparedStatement(t *testing.T) {
	db := openDB(t)
	mustExec(t, db, `CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`)
	stmt, err := db.Prepare(`INSERT INTO t VALUES (?, ?)`)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer stmt.Close()
	for i := int64(1); i <= 10; i++ {
		if _, err := stmt.Exec(i, fmt.Sprintf("r%d", i)); err != nil {
			t.Fatalf("Exec(%d): %v", i, err)
		}
	}
	rows, _ := db.Query(`SELECT id FROM t`)
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if count != 10 {
		t.Errorf("count = %d, want 10", count)
	}
}

func TestDriverBeginReturnsError(t *testing.T) {
	db := openDB(t)
	tx, err := db.Begin()
	if err == nil {
		_ = tx.Rollback()
		t.Fatal("Begin returned no error")
	}
	if !errors.Is(err, godb.ErrTransactionsUnsupported) {
		t.Errorf("err = %v, want wraps godb.ErrTransactionsUnsupported", err)
	}
}

func TestDriverScanWithSqlNullString(t *testing.T) {
	db := openDB(t)
	mustExec(t, db, `CREATE TABLE t (id INTEGER PRIMARY KEY, nickname TEXT)`)
	mustExec(t, db, `INSERT INTO t VALUES (?, ?)`, int64(1), nil)
	mustExec(t, db, `INSERT INTO t VALUES (?, ?)`, int64(2), "felipe")
	rows, err := db.Query(`SELECT nickname FROM t`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()
	var got []sql.NullString
	for rows.Next() {
		var n sql.NullString
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		got = append(got, n)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	if got[0].Valid {
		t.Errorf("row[0] should be NULL, got %+v", got[0])
	}
	if !got[1].Valid || got[1].String != "felipe" {
		t.Errorf("row[1] = %+v, want {Valid:true String:felipe}", got[1])
	}
}

func TestDriverContextCancellation(t *testing.T) {
	db := openDB(t)
	mustExec(t, db, `CREATE TABLE t (id INTEGER PRIMARY KEY)`)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := db.ExecContext(cancelled, `INSERT INTO t VALUES (?)`, int64(1))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestDriverErrorsIsForwarded(t *testing.T) {
	db := openDB(t)
	// No CREATE TABLE first; INSERT to a nonexistent table.
	_, err := db.Exec(`INSERT INTO ghosts VALUES (?, ?)`, int64(1), "x")
	if !errors.Is(err, godb.ErrTableNotFound) {
		t.Fatalf("err = %v, want wraps godb.ErrTableNotFound", err)
	}
}

func TestDriverRejectsUnsupportedSQL(t *testing.T) {
	db := openDB(t)
	mustExec(t, db, `CREATE TABLE t (id INTEGER PRIMARY KEY)`)
	_, err := db.Query(`SELECT * FROM t JOIN x ON t.id = x.id`)
	if !errors.Is(err, godb.ErrUnsupportedSQL) {
		t.Fatalf("err = %v, want wraps godb.ErrUnsupportedSQL", err)
	}
}

func TestDriverRejectsFloatArg(t *testing.T) {
	db := openDB(t)
	mustExec(t, db, `CREATE TABLE t (id INTEGER PRIMARY KEY)`)
	_, err := db.Exec(`INSERT INTO t VALUES (?)`, 3.14)
	if err == nil {
		t.Fatal("expected error for float arg")
	}
}

func TestDriverRoundTripAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.godb")
	{
		db, err := sql.Open("godb", path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		mustExec(t, db, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`)
		mustExec(t, db, `INSERT INTO users VALUES (?, ?)`, int64(1), "Felipe")
		if err := db.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	{
		db, err := sql.Open("godb", path)
		if err != nil {
			t.Fatalf("Reopen: %v", err)
		}
		defer db.Close()
		rows, err := db.Query(`SELECT name FROM users WHERE id = ?`, int64(1))
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("expected one row")
		}
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if name != "Felipe" {
			t.Errorf("name = %q, want Felipe", name)
		}
	}
}

func TestDriverConcurrentReads(t *testing.T) {
	// database/sql may pool multiple conns; each conn is its own pager
	// in v0.1. Reads from the SAME file are safe in autocommit mode
	// (the pager's mutex serializes reads within one conn; multiple
	// conns each have their own pager). Writes from multiple conns
	// concurrently are not safe and we don't test it here. We do test
	// that multi-conn reads work, which is the common case for the
	// database/sql pool.
	path := filepath.Join(t.TempDir(), "conc.godb")
	{
		// Seed via one conn.
		seed, err := sql.Open("godb", path)
		if err != nil {
			t.Fatalf("seed Open: %v", err)
		}
		mustExec(t, seed, `CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`)
		for i := int64(1); i <= 20; i++ {
			mustExec(t, seed, `INSERT INTO t VALUES (?, ?)`, i, fmt.Sprintf("r%d", i))
		}
		if err := seed.Close(); err != nil {
			t.Fatalf("seed Close: %v", err)
		}
	}

	db, err := sql.Open("godb", path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(4)

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := int64(1); k <= 20; k++ {
				rows, err := db.Query(`SELECT name FROM t WHERE id = ?`, k)
				if err != nil {
					t.Errorf("g=%d k=%d Query: %v", g, k, err)
					return
				}
				if rows.Next() {
					var name string
					if err := rows.Scan(&name); err != nil {
						t.Errorf("g=%d k=%d Scan: %v", g, k, err)
					}
					if name != fmt.Sprintf("r%d", k) {
						t.Errorf("g=%d k=%d name = %q, want r%d", g, k, name, k)
					}
				}
				rows.Close()
			}
		}(g)
	}
	wg.Wait()
}

// mustExec is a small helper that fails the test on Exec error.
func mustExec(t *testing.T, db *sql.DB, query string, args ...any) sql.Result {
	t.Helper()
	res, err := db.Exec(query, args...)
	if err != nil {
		t.Fatalf("Exec(%q): %v", query, err)
	}
	return res
}

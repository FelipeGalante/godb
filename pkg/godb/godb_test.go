package godb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func tempDB(t *testing.T, opts ...Option) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.godb")
	db, err := Open(path, opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, path
}

func ctx() context.Context { return context.Background() }

func TestOpenCreatesDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.godb")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("database file not created: %v", err)
	}
}

func TestOpenRejectsMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.godb")
	_, err := Open(path, WithCreateIfMissing(false))
	if err == nil {
		t.Fatal("Open: want error on missing file with WithCreateIfMissing(false)")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want wraps os.ErrNotExist", err)
	}
}

func TestExecCreateInsertSelectFullLoop(t *testing.T) {
	db, _ := tempDB(t)
	// CREATE TABLE
	res, err := db.Exec(ctx(), `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, active BOOLEAN)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("CREATE RowsAffected = %d, want 1", res.RowsAffected)
	}
	// Two INSERTs.
	res1, err := db.Exec(ctx(), `INSERT INTO users VALUES (?, ?, ?)`, 1, "Felipe", true)
	if err != nil {
		t.Fatalf("INSERT 1: %v", err)
	}
	if res1.LastInsertID != 1 {
		t.Errorf("LastInsertID after first insert = %d, want 1", res1.LastInsertID)
	}
	if _, err := db.Exec(ctx(), `INSERT INTO users VALUES (?, ?, ?)`, 2, "MG", false); err != nil {
		t.Fatalf("INSERT 2: %v", err)
	}
	// SELECT *
	rows, err := db.Query(ctx(), `SELECT * FROM users`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()
	got := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		var active bool
		if err := rows.Scan(&id, &name, &active); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		got[id] = name
	}
	if rows.Err() != nil {
		t.Fatalf("Rows.Err: %v", rows.Err())
	}
	if got[1] != "Felipe" || got[2] != "MG" {
		t.Errorf("rows = %v, want {1:Felipe 2:MG}", got)
	}
}

func TestQueryByPrimaryKey(t *testing.T) {
	db, _ := tempDB(t)
	db.Exec(ctx(), `CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`)
	for i := int64(1); i <= 5; i++ {
		db.Exec(ctx(), `INSERT INTO t VALUES (?, ?)`, i, fmt.Sprintf("r%d", i))
	}
	rows, err := db.Query(ctx(), `SELECT * FROM t WHERE id = ?`, int64(3))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		var name string
		rows.Scan(&id, &name)
		if id != 3 || name != "r3" {
			t.Errorf("got id=%d name=%q, want 3/r3", id, name)
		}
		count++
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestRowsScanIntoBool(t *testing.T) {
	db, _ := tempDB(t)
	db.Exec(ctx(), `CREATE TABLE t (id INTEGER PRIMARY KEY, active BOOLEAN)`)
	db.Exec(ctx(), `INSERT INTO t VALUES (?, ?)`, 1, true)
	rows, _ := db.Query(ctx(), `SELECT active FROM t`)
	defer rows.Close()
	rows.Next()
	var b bool
	if err := rows.Scan(&b); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !b {
		t.Errorf("got false, want true")
	}
}

func TestRowsScanIntoInterface(t *testing.T) {
	db, _ := tempDB(t)
	db.Exec(ctx(), `CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT, active BOOLEAN)`)
	db.Exec(ctx(), `INSERT INTO t VALUES (?, ?, ?)`, 1, nil, true)
	rows, _ := db.Query(ctx(), `SELECT id, name, active FROM t`)
	defer rows.Close()
	rows.Next()
	var id, name, active any
	if err := rows.Scan(&id, &name, &active); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if id != int64(1) {
		t.Errorf("id = %v (%T), want int64(1)", id, id)
	}
	if name != nil {
		t.Errorf("name = %v, want nil", name)
	}
	if active != true {
		t.Errorf("active = %v, want true", active)
	}
}

func TestRowsScanNullIntoNonNullableReturnsError(t *testing.T) {
	db, _ := tempDB(t)
	db.Exec(ctx(), `CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)`)
	db.Exec(ctx(), `INSERT INTO t VALUES (?, ?)`, 1, nil)
	rows, _ := db.Query(ctx(), `SELECT name FROM t`)
	defer rows.Close()
	rows.Next()
	var s string
	err := rows.Scan(&s)
	if !errors.Is(err, ErrScanNullIntoNonNullable) {
		t.Fatalf("err = %v, want ErrScanNullIntoNonNullable", err)
	}
	if rows.Err() != err {
		t.Errorf("Rows.Err = %v, want the scan error", rows.Err())
	}
}

func TestRowsScanTypeMismatchReturnsError(t *testing.T) {
	db, _ := tempDB(t)
	db.Exec(ctx(), `CREATE TABLE t (id INTEGER PRIMARY KEY)`)
	db.Exec(ctx(), `INSERT INTO t VALUES (?)`, 1)
	rows, _ := db.Query(ctx(), `SELECT id FROM t`)
	defer rows.Close()
	rows.Next()
	var s string // wrong type
	err := rows.Scan(&s)
	if !errors.Is(err, ErrScanTypeMismatch) {
		t.Fatalf("err = %v, want ErrScanTypeMismatch", err)
	}
}

func TestRowsScanWrongCount(t *testing.T) {
	db, _ := tempDB(t)
	db.Exec(ctx(), `CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)`)
	db.Exec(ctx(), `INSERT INTO t VALUES (?, ?)`, 1, "x")
	rows, _ := db.Query(ctx(), `SELECT * FROM t`)
	defer rows.Close()
	rows.Next()
	var id int64
	err := rows.Scan(&id) // missing destination for name
	if !errors.Is(err, ErrScanWrongCount) {
		t.Fatalf("err = %v, want ErrScanWrongCount", err)
	}
}

func TestRowsCloseIsIdempotent(t *testing.T) {
	db, _ := tempDB(t)
	db.Exec(ctx(), `CREATE TABLE t (id INTEGER PRIMARY KEY)`)
	rows, _ := db.Query(ctx(), `SELECT * FROM t`)
	if err := rows.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestExecReturnsLastInsertID(t *testing.T) {
	db, _ := tempDB(t)
	db.Exec(ctx(), `CREATE TABLE t (id INTEGER PRIMARY KEY, x TEXT)`)
	for i := int64(1); i <= 5; i++ {
		res, err := db.Exec(ctx(), `INSERT INTO t VALUES (?, ?)`, i, "r")
		if err != nil {
			t.Fatalf("Insert(%d): %v", i, err)
		}
		if res.LastInsertID != i {
			t.Errorf("LastInsertID = %d, want %d", res.LastInsertID, i)
		}
	}
}

func TestBeginReturnsErrTransactionsUnsupported(t *testing.T) {
	db, _ := tempDB(t)
	tx, err := db.Begin(ctx())
	if tx != nil {
		t.Errorf("Tx = %v, want nil", tx)
	}
	if !errors.Is(err, ErrTransactionsUnsupported) {
		t.Errorf("err = %v, want ErrTransactionsUnsupported", err)
	}
}

func TestExecOnClosedDB(t *testing.T) {
	db, _ := tempDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err := db.Exec(ctx(), `CREATE TABLE t (id INTEGER PRIMARY KEY)`)
	if !errors.Is(err, ErrDatabaseClosed) {
		t.Errorf("Exec after Close: err = %v, want ErrDatabaseClosed", err)
	}
	_, err = db.Query(ctx(), `SELECT * FROM t`)
	if !errors.Is(err, ErrDatabaseClosed) {
		t.Errorf("Query after Close: err = %v, want ErrDatabaseClosed", err)
	}
}

func TestExecAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.godb")
	{
		db, err := Open(path)
		if err != nil {
			t.Fatalf("first Open: %v", err)
		}
		if _, err := db.Exec(ctx(), `CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
			t.Fatalf("CREATE: %v", err)
		}
		if _, err := db.Exec(ctx(), `INSERT INTO t VALUES (?, ?)`, 1, "Felipe"); err != nil {
			t.Fatalf("INSERT: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	{
		db, err := Open(path)
		if err != nil {
			t.Fatalf("reopen Open: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		rows, err := db.Query(ctx(), `SELECT * FROM t WHERE id = ?`, int64(1))
		if err != nil {
			t.Fatalf("Query after reopen: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("Next returned false; expected one row")
		}
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if id != 1 || name != "Felipe" {
			t.Errorf("got id=%d name=%q, want 1/Felipe", id, name)
		}
	}
}

func TestQueryUnsupportedSQLPropagates(t *testing.T) {
	db, _ := tempDB(t)
	db.Exec(ctx(), `CREATE TABLE t (id INTEGER PRIMARY KEY)`)
	_, err := db.Query(ctx(), `SELECT * FROM t JOIN x ON id = x.id`)
	if !errors.Is(err, ErrUnsupportedSQL) {
		t.Fatalf("err = %v, want ErrUnsupportedSQL", err)
	}
}

func TestQueryNonPKWhereReturnsErrWhereOnlyPrimaryKey(t *testing.T) {
	db, _ := tempDB(t)
	db.Exec(ctx(), `CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`)
	_, err := db.Query(ctx(), `SELECT * FROM t WHERE name = ?`, "x")
	if !errors.Is(err, ErrWhereOnlyPrimaryKey) {
		t.Fatalf("err = %v, want ErrWhereOnlyPrimaryKey", err)
	}
}

func TestContextCancellationOnExec(t *testing.T) {
	db, _ := tempDB(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := db.Exec(cancelled, `CREATE TABLE t (id INTEGER PRIMARY KEY)`)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestQueryRejectsCreateTable(t *testing.T) {
	// Query is for SELECT only; DDL via Query should error early.
	db, _ := tempDB(t)
	_, err := db.Query(ctx(), `CREATE TABLE t (id INTEGER PRIMARY KEY)`)
	if err == nil {
		t.Fatal("Query of CREATE TABLE should error")
	}
}

func TestExecInsertGrowsTableRootSurvivesReopen(t *testing.T) {
	// End-to-end persistence test for the M6→M8 gap closure, this
	// time through the public API.
	path := filepath.Join(t.TempDir(), "rootgrow.godb")
	{
		db, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if _, err := db.Exec(ctx(), `CREATE TABLE w (id INTEGER PRIMARY KEY, payload TEXT NOT NULL)`); err != nil {
			t.Fatalf("CREATE: %v", err)
		}
		// 400-byte payloads force a root split well before 500 rows.
		var sb [400]byte
		for i := range sb {
			sb[i] = 'x'
		}
		largePayload := string(sb[:])
		for i := int64(1); i <= 500; i++ {
			if _, err := db.Exec(ctx(), `INSERT INTO w VALUES (?, ?)`, i, largePayload); err != nil {
				t.Fatalf("INSERT(%d): %v", i, err)
			}
		}
		if err := db.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	{
		db, err := Open(path)
		if err != nil {
			t.Fatalf("reopen Open: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		rows, err := db.Query(ctx(), `SELECT id FROM w WHERE id = ?`, int64(500))
		if err != nil {
			t.Fatalf("Query after reopen: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("row 500 not found after reopen — persistence broken")
		}
		var id int64
		rows.Scan(&id)
		if id != 500 {
			t.Errorf("id = %d, want 500", id)
		}
	}
}

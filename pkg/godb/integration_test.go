package godb

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// Integration tests drive multi-table scenarios through the public
// API. Every existing godb_test.go test uses one table; these are
// here specifically to pin behaviors that show up only with several
// tables coexisting in the same .godb file: independent PK spaces,
// catalog row ordering, cross-table reopen recovery.

func TestIntegrationTwoIndependentTables(t *testing.T) {
	db, _ := tempDB(t)
	mustE(t, db, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`)
	mustE(t, db, `CREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT NOT NULL)`)

	mustE(t, db, `INSERT INTO users VALUES (?, ?)`, int64(1), "Felipe")
	mustE(t, db, `INSERT INTO users VALUES (?, ?)`, int64(2), "MG")
	mustE(t, db, `INSERT INTO posts VALUES (?, ?)`, int64(1), "Hello, world")

	// users has 2 rows, posts has 1; primary-key spaces are independent.
	if got := scanCount(t, db, `SELECT * FROM users`); got != 2 {
		t.Errorf("users count = %d, want 2", got)
	}
	if got := scanCount(t, db, `SELECT * FROM posts`); got != 1 {
		t.Errorf("posts count = %d, want 1", got)
	}
	// users.id=1 and posts.id=1 are different rows.
	user := scanOneText(t, db, `SELECT name FROM users WHERE id = ?`, int64(1))
	if user != "Felipe" {
		t.Errorf("users id=1 name = %q, want Felipe", user)
	}
	post := scanOneText(t, db, `SELECT title FROM posts WHERE id = ?`, int64(1))
	if post != "Hello, world" {
		t.Errorf("posts id=1 title = %q, want %q", post, "Hello, world")
	}
}

func TestIntegrationThreeTablesDistinctPKSpaces(t *testing.T) {
	db, _ := tempDB(t)
	for _, name := range []string{"a", "b", "c"} {
		mustE(t, db, fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY, x TEXT NOT NULL)`, name))
	}
	// Insert ids 1,2,3 into each table; values are table-distinguishable.
	for _, name := range []string{"a", "b", "c"} {
		for id := int64(1); id <= 3; id++ {
			mustE(t, db, fmt.Sprintf(`INSERT INTO %s VALUES (?, ?)`, name), id, fmt.Sprintf("%s-%d", name, id))
		}
	}
	// Each table returns only its own rows.
	for _, name := range []string{"a", "b", "c"} {
		rows, err := db.Query(ctx(), fmt.Sprintf(`SELECT x FROM %s`, name))
		if err != nil {
			t.Fatalf("Query %s: %v", name, err)
		}
		var got []string
		for rows.Next() {
			var x string
			if err := rows.Scan(&x); err != nil {
				t.Fatalf("Scan: %v", err)
			}
			got = append(got, x)
		}
		rows.Close()
		want := []string{name + "-1", name + "-2", name + "-3"}
		if len(got) != 3 {
			t.Fatalf("%s count = %d, want 3", name, len(got))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s[%d] = %q, want %q", name, i, got[i], want[i])
			}
		}
	}
}

func TestIntegrationOneTableGrowsWhileOthersStaySmall(t *testing.T) {
	// One table root-splits; the other two stay tiny. Both small
	// tables must survive the large table's tree growth — the
	// catalog has to keep their RootPageIDs straight.
	db, _ := tempDB(t)
	mustE(t, db, `CREATE TABLE big (id INTEGER PRIMARY KEY, payload TEXT NOT NULL)`)
	mustE(t, db, `CREATE TABLE small (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`)
	mustE(t, db, `CREATE TABLE tiny (id INTEGER PRIMARY KEY)`)

	// big: 500 rows of 200-byte payloads → guaranteed root-split.
	payload := strings.Repeat("y", 200)
	for i := int64(1); i <= 500; i++ {
		mustE(t, db, `INSERT INTO big VALUES (?, ?)`, i, payload)
	}
	// small: a handful.
	for i := int64(1); i <= 5; i++ {
		mustE(t, db, `INSERT INTO small VALUES (?, ?)`, i, fmt.Sprintf("s%d", i))
	}
	// tiny: one row.
	mustE(t, db, `INSERT INTO tiny VALUES (?)`, int64(1))

	if got := scanCount(t, db, `SELECT id FROM big`); got != 500 {
		t.Errorf("big count = %d, want 500", got)
	}
	if got := scanCount(t, db, `SELECT id FROM small`); got != 5 {
		t.Errorf("small count = %d, want 5", got)
	}
	if got := scanCount(t, db, `SELECT id FROM tiny`); got != 1 {
		t.Errorf("tiny count = %d, want 1", got)
	}

	// Spot-check a deep row in big to confirm post-split layout works.
	got := scanOneText(t, db, `SELECT payload FROM big WHERE id = ?`, int64(500))
	if len(got) != 200 {
		t.Errorf("big.id=500 payload length = %d, want 200", len(got))
	}
}

func TestIntegrationFiveTableWorkloadSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fivetable.godb")

	rowsPerTable := map[string]int64{
		"users":  50,
		"posts":  100,
		"likes":  25,
		"tags":   10,
		"events": 200,
	}

	// Phase 1: create tables, populate them, close.
	{
		db, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		for name := range rowsPerTable {
			mustE(t, db, fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY, payload TEXT NOT NULL)`, name))
		}
		for name, n := range rowsPerTable {
			payload := strings.Repeat("p", 100) // 100-byte payload — keeps things modest
			for i := int64(1); i <= n; i++ {
				mustE(t, db, fmt.Sprintf(`INSERT INTO %s VALUES (?, ?)`, name), i, payload)
			}
		}
		if err := db.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}

	// Phase 2: reopen, verify every table's row count survives.
	{
		db, err := Open(path)
		if err != nil {
			t.Fatalf("Reopen: %v", err)
		}
		defer db.Close()
		for name, want := range rowsPerTable {
			got := scanCount(t, db, fmt.Sprintf(`SELECT id FROM %s`, name))
			if int64(got) != want {
				t.Errorf("%s count after reopen = %d, want %d", name, got, want)
			}
		}
		// Spot-check one PK lookup per table.
		for name, n := range rowsPerTable {
			payload := scanOneText(t, db, fmt.Sprintf(`SELECT payload FROM %s WHERE id = ?`, name), n)
			if len(payload) != 100 {
				t.Errorf("%s.id=%d payload length = %d, want 100", name, n, len(payload))
			}
		}
	}
}

func TestIntegrationInterleavedCreateAndInsertMixedOrder(t *testing.T) {
	// CREATE TABLE A, INSERT into A, CREATE TABLE B, INSERT into A and
	// B, CREATE TABLE C, INSERT into all three. Catalog rows must
	// survive being interleaved with table-tree writes (each Insert
	// may grow a table tree's root; CreateTable inserts into the
	// catalog tree, possibly growing its root).
	db, _ := tempDB(t)

	mustE(t, db, `CREATE TABLE a (id INTEGER PRIMARY KEY, v TEXT NOT NULL)`)
	mustE(t, db, `INSERT INTO a VALUES (?, ?)`, int64(1), "a1")

	mustE(t, db, `CREATE TABLE b (id INTEGER PRIMARY KEY, v TEXT NOT NULL)`)
	mustE(t, db, `INSERT INTO a VALUES (?, ?)`, int64(2), "a2")
	mustE(t, db, `INSERT INTO b VALUES (?, ?)`, int64(1), "b1")

	mustE(t, db, `CREATE TABLE c (id INTEGER PRIMARY KEY, v TEXT NOT NULL)`)
	mustE(t, db, `INSERT INTO a VALUES (?, ?)`, int64(3), "a3")
	mustE(t, db, `INSERT INTO b VALUES (?, ?)`, int64(2), "b2")
	mustE(t, db, `INSERT INTO c VALUES (?, ?)`, int64(1), "c1")

	if got := scanCount(t, db, `SELECT * FROM a`); got != 3 {
		t.Errorf("a count = %d, want 3", got)
	}
	if got := scanCount(t, db, `SELECT * FROM b`); got != 2 {
		t.Errorf("b count = %d, want 2", got)
	}
	if got := scanCount(t, db, `SELECT * FROM c`); got != 1 {
		t.Errorf("c count = %d, want 1", got)
	}
	// Spot-check that mid-INSERT into a didn't disrupt b.
	got := scanOneText(t, db, `SELECT v FROM b WHERE id = ?`, int64(2))
	if got != "b2" {
		t.Errorf("b.id=2 v = %q, want b2", got)
	}
}

// --- helpers -------------------------------------------------------------

func mustE(t *testing.T, db *DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(ctx(), query, args...); err != nil {
		t.Fatalf("Exec(%q): %v", query, err)
	}
}

// scanCount runs query and returns the number of rows. The query
// should return at least one column; the values aren't inspected.
func scanCount(t *testing.T, db *DB, query string, args ...any) int {
	t.Helper()
	rows, err := db.Query(ctx(), query, args...)
	if err != nil {
		t.Fatalf("Query(%q): %v", query, err)
	}
	defer rows.Close()
	cols := rows.Columns()
	scanDest := make([]any, len(cols))
	holders := make([]any, len(cols))
	for i := range scanDest {
		scanDest[i] = &holders[i]
	}
	count := 0
	for rows.Next() {
		if err := rows.Scan(scanDest...); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	return count
}

// scanOneText runs query and returns the first row's first column as
// a string. Fails the test if no rows are returned.
func scanOneText(t *testing.T, db *DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query(ctx(), query, args...)
	if err != nil {
		t.Fatalf("Query(%q): %v", query, err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("Query(%q): no rows", query)
	}
	var s string
	if err := rows.Scan(&s); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return s
}

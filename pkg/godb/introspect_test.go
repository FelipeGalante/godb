package godb

import "testing"

func TestTablesListsSortedByName(t *testing.T) {
	db, _ := tempDB(t)
	for _, stmt := range []string{
		`CREATE TABLE zebra (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE apple (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE mango (id INTEGER PRIMARY KEY)`,
	} {
		if _, err := db.Exec(ctx(), stmt); err != nil {
			t.Fatalf("Exec %q: %v", stmt, err)
		}
	}
	tables, err := db.Tables()
	if err != nil {
		t.Fatalf("Tables: %v", err)
	}
	gotNames := make([]string, len(tables))
	for i, ti := range tables {
		gotNames[i] = ti.Name
	}
	want := []string{"apple", "mango", "zebra"}
	if len(gotNames) != len(want) {
		t.Fatalf("Tables count = %d, want %d (%v)", len(gotNames), len(want), gotNames)
	}
	for i := range want {
		if gotNames[i] != want[i] {
			t.Errorf("Tables[%d] = %q, want %q", i, gotNames[i], want[i])
		}
	}
	// SQL round-trips the original CREATE text.
	if tables[0].SQL != `CREATE TABLE apple (id INTEGER PRIMARY KEY, name TEXT)` {
		t.Errorf("apple SQL = %q", tables[0].SQL)
	}
}

func TestTablesOnClosedDB(t *testing.T) {
	db, _ := tempDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := db.Tables(); err == nil {
		t.Fatal("Tables on closed DB: want error")
	}
}

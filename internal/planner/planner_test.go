package planner

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/felipegalante/godb/internal/catalog"
	"github.com/felipegalante/godb/internal/record"
	"github.com/felipegalante/godb/internal/sql"
	"github.com/felipegalante/godb/internal/storage"
)

// fixturePlanner builds a planner backed by a fresh in-memory catalog
// containing one users table:
//   CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, active BOOLEAN);
func fixturePlanner(t *testing.T) (*Planner, *catalog.Catalog) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "p.godb")
	p, err := storage.OpenPager(path, storage.PagerOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("OpenPager: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	c, err := catalog.Open(p)
	if err != nil {
		t.Fatalf("catalog.Open: %v", err)
	}
	if _, err := c.CreateTable("users", record.Schema{Columns: []record.Column{
		{Name: "id", Kind: record.KindInteger, NotNull: true, PrimaryKey: true, Position: 0},
		{Name: "name", Kind: record.KindText, NotNull: true, Position: 1},
		{Name: "active", Kind: record.KindBoolean, Position: 2},
	}}, "CREATE TABLE users (...)"); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	return New(c), c
}

func mustParse(t *testing.T, src string) sql.Statement {
	t.Helper()
	stmt, err := sql.Parse(src)
	if err != nil {
		t.Fatalf("sql.Parse(%q): %v", src, err)
	}
	return stmt
}

func TestPlanCreateTable(t *testing.T) {
	pl, _ := fixturePlanner(t)
	stmt := mustParse(t, `CREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT NOT NULL)`)
	plan, err := pl.Plan(stmt)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	ct, ok := plan.(*CreateTablePlan)
	if !ok {
		t.Fatalf("plan type = %T, want *CreateTablePlan", plan)
	}
	if ct.Name != "posts" {
		t.Errorf("Name = %q", ct.Name)
	}
	if len(ct.Schema.Columns) != 2 {
		t.Errorf("Schema column count = %d, want 2", len(ct.Schema.Columns))
	}
	if !ct.Schema.Columns[0].PrimaryKey {
		t.Errorf("posts.id.PrimaryKey = false")
	}
}

func TestPlanCreateTableRejectsDuplicateName(t *testing.T) {
	pl, _ := fixturePlanner(t)
	stmt := mustParse(t, `CREATE TABLE users (id INTEGER PRIMARY KEY)`)
	_, err := pl.Plan(stmt)
	if !errors.Is(err, ErrTableExists) {
		t.Fatalf("err = %v, want ErrTableExists", err)
	}
}

func TestPlanCreateTableRejectsNoPK(t *testing.T) {
	pl, _ := fixturePlanner(t)
	stmt := mustParse(t, `CREATE TABLE x (a INTEGER, b TEXT)`)
	_, err := pl.Plan(stmt)
	if !errors.Is(err, ErrInvalidSchema) {
		t.Fatalf("err = %v, want ErrInvalidSchema", err)
	}
}

func TestPlanCreateTableRejectsNonIntegerPK(t *testing.T) {
	pl, _ := fixturePlanner(t)
	stmt := mustParse(t, `CREATE TABLE x (id TEXT PRIMARY KEY)`)
	_, err := pl.Plan(stmt)
	if !errors.Is(err, ErrInvalidSchema) {
		t.Fatalf("err = %v, want ErrInvalidSchema", err)
	}
}

func TestPlanCreateTableRejectsMultiplePKs(t *testing.T) {
	pl, _ := fixturePlanner(t)
	stmt := mustParse(t, `CREATE TABLE x (a INTEGER PRIMARY KEY, b INTEGER PRIMARY KEY)`)
	_, err := pl.Plan(stmt)
	if !errors.Is(err, ErrInvalidSchema) {
		t.Fatalf("err = %v, want ErrInvalidSchema", err)
	}
}

func TestPlanInsertWithoutColumns(t *testing.T) {
	pl, _ := fixturePlanner(t)
	stmt := mustParse(t, `INSERT INTO users VALUES (?, ?, ?)`)
	plan, err := pl.Plan(stmt)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	ins, ok := plan.(*InsertPlan)
	if !ok {
		t.Fatalf("plan type = %T", plan)
	}
	if ins.Columns != nil {
		t.Errorf("Columns = %v, want nil (no explicit list)", ins.Columns)
	}
	if len(ins.Values) != 3 {
		t.Errorf("Values count = %d, want 3", len(ins.Values))
	}
}

func TestPlanInsertWithExplicitColumns(t *testing.T) {
	pl, _ := fixturePlanner(t)
	stmt := mustParse(t, `INSERT INTO users (name, id) VALUES (?, ?)`)
	plan, err := pl.Plan(stmt)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	ins := plan.(*InsertPlan)
	want := []int{1, 0} // name=1, id=0
	if len(ins.Columns) != 2 || ins.Columns[0] != want[0] || ins.Columns[1] != want[1] {
		t.Errorf("Columns = %v, want %v", ins.Columns, want)
	}
}

func TestPlanInsertRejectsUnknownColumn(t *testing.T) {
	pl, _ := fixturePlanner(t)
	stmt := mustParse(t, `INSERT INTO users (id, missing) VALUES (?, ?)`)
	_, err := pl.Plan(stmt)
	if !errors.Is(err, ErrColumnNotFound) {
		t.Fatalf("err = %v, want ErrColumnNotFound", err)
	}
}

func TestPlanInsertRejectsCountMismatch(t *testing.T) {
	pl, _ := fixturePlanner(t)
	// Explicit list has 2 columns, but 3 values.
	stmt := mustParse(t, `INSERT INTO users (id, name) VALUES (?, ?, ?)`)
	_, err := pl.Plan(stmt)
	if !errors.Is(err, ErrInsertCountMismatch) {
		t.Fatalf("err = %v, want ErrInsertCountMismatch", err)
	}
}

func TestPlanInsertRejectsCountMismatchImplicitColumns(t *testing.T) {
	pl, _ := fixturePlanner(t)
	// No explicit list; schema has 3 columns but only 2 values.
	stmt := mustParse(t, `INSERT INTO users VALUES (?, ?)`)
	_, err := pl.Plan(stmt)
	if !errors.Is(err, ErrInsertCountMismatch) {
		t.Fatalf("err = %v, want ErrInsertCountMismatch", err)
	}
}

func TestPlanInsertRejectsUnknownTable(t *testing.T) {
	pl, _ := fixturePlanner(t)
	stmt := mustParse(t, `INSERT INTO nope VALUES (?, ?)`)
	_, err := pl.Plan(stmt)
	if !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("err = %v, want ErrTableNotFound", err)
	}
}

func TestPlanSelectStar(t *testing.T) {
	pl, _ := fixturePlanner(t)
	stmt := mustParse(t, `SELECT * FROM users`)
	plan, err := pl.Plan(stmt)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	// No wrapping projection for SELECT *.
	if _, ok := plan.(*TableScanPlan); !ok {
		t.Fatalf("plan type = %T, want *TableScanPlan (SELECT * should not wrap a Projection)", plan)
	}
}

func TestPlanSelectColumns(t *testing.T) {
	pl, _ := fixturePlanner(t)
	stmt := mustParse(t, `SELECT name, id FROM users`)
	plan, err := pl.Plan(stmt)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	proj, ok := plan.(*ProjectionPlan)
	if !ok {
		t.Fatalf("plan type = %T, want *ProjectionPlan", plan)
	}
	if len(proj.Output) != 2 || proj.Output[0] != 1 || proj.Output[1] != 0 {
		t.Errorf("Output = %v, want [1 0]", proj.Output)
	}
	if len(proj.Names) != 2 || proj.Names[0] != "name" || proj.Names[1] != "id" {
		t.Errorf("Names = %v", proj.Names)
	}
	if _, ok := proj.Input.(*TableScanPlan); !ok {
		t.Errorf("Input = %T, want *TableScanPlan", proj.Input)
	}
}

func TestPlanSelectWherePK(t *testing.T) {
	pl, _ := fixturePlanner(t)
	stmt := mustParse(t, `SELECT * FROM users WHERE id = ?`)
	plan, err := pl.Plan(stmt)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	pkl, ok := plan.(*PrimaryKeyLookupPlan)
	if !ok {
		t.Fatalf("plan type = %T, want *PrimaryKeyLookupPlan", plan)
	}
	if _, ok := pkl.KeyExpr.(*sql.Placeholder); !ok {
		t.Errorf("KeyExpr = %T, want *sql.Placeholder", pkl.KeyExpr)
	}
}

func TestPlanSelectWherePKWithProjection(t *testing.T) {
	pl, _ := fixturePlanner(t)
	stmt := mustParse(t, `SELECT name FROM users WHERE id = 5`)
	plan, err := pl.Plan(stmt)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	proj := plan.(*ProjectionPlan)
	if len(proj.Output) != 1 || proj.Output[0] != 1 || proj.Names[0] != "name" {
		t.Errorf("Output/Names mismatch: %+v", proj)
	}
	if _, ok := proj.Input.(*PrimaryKeyLookupPlan); !ok {
		t.Errorf("Input = %T, want *PrimaryKeyLookupPlan", proj.Input)
	}
}

func TestPlanSelectWhereNonPK(t *testing.T) {
	pl, _ := fixturePlanner(t)
	stmt := mustParse(t, `SELECT * FROM users WHERE name = ?`)
	_, err := pl.Plan(stmt)
	if !errors.Is(err, ErrWhereOnlyPrimaryKey) {
		t.Fatalf("err = %v, want ErrWhereOnlyPrimaryKey", err)
	}
}

func TestPlanSelectRejectsUnknownColumn(t *testing.T) {
	pl, _ := fixturePlanner(t)
	stmt := mustParse(t, `SELECT bogus FROM users`)
	_, err := pl.Plan(stmt)
	if !errors.Is(err, ErrColumnNotFound) {
		t.Fatalf("err = %v, want ErrColumnNotFound", err)
	}
}

func TestPlanSelectRejectsUnknownTable(t *testing.T) {
	pl, _ := fixturePlanner(t)
	stmt := mustParse(t, `SELECT * FROM nope`)
	_, err := pl.Plan(stmt)
	if !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("err = %v, want ErrTableNotFound", err)
	}
}

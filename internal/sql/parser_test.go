package sql

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/felipegalante/godb/internal/record"
)

func mustParse(t *testing.T, src string) Statement {
	t.Helper()
	stmt, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	return stmt
}

func assertUnsupported(t *testing.T, src, wantFragment string) {
	t.Helper()
	_, err := Parse(src)
	if err == nil {
		t.Fatalf("Parse(%q): want error, got nil", src)
	}
	if !errors.Is(err, ErrUnsupportedSQL) {
		t.Fatalf("Parse(%q): want ErrUnsupportedSQL, got %v", src, err)
	}
	if !strings.Contains(err.Error(), wantFragment) {
		t.Errorf("Parse(%q): err = %q; want it to contain %q", src, err.Error(), wantFragment)
	}
}

func assertSyntax(t *testing.T, src, wantFragment string) {
	t.Helper()
	_, err := Parse(src)
	if err == nil {
		t.Fatalf("Parse(%q): want error, got nil", src)
	}
	if !errors.Is(err, ErrSyntax) {
		t.Fatalf("Parse(%q): want ErrSyntax, got %v", src, err)
	}
	if wantFragment != "" && !strings.Contains(err.Error(), wantFragment) {
		t.Errorf("Parse(%q): err = %q; want it to contain %q", src, err.Error(), wantFragment)
	}
}

// ---- CREATE TABLE -------------------------------------------------------

func TestParseCreateTable(t *testing.T) {
	stmt := mustParse(t, `CREATE TABLE users (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		active BOOLEAN
	);`)
	ct, ok := stmt.(*CreateTableStatement)
	if !ok {
		t.Fatalf("type = %T, want *CreateTableStatement", stmt)
	}
	if ct.Name != "users" {
		t.Errorf("Name = %q, want %q", ct.Name, "users")
	}
	if len(ct.Columns) != 3 {
		t.Fatalf("Columns count = %d, want 3", len(ct.Columns))
	}
	wantCols := []ColumnDef{
		{Name: "id", Kind: record.KindInteger, NotNull: false, PrimaryKey: true},
		{Name: "name", Kind: record.KindText, NotNull: true, PrimaryKey: false},
		{Name: "active", Kind: record.KindBoolean, NotNull: false, PrimaryKey: false},
	}
	for i, w := range wantCols {
		got := ct.Columns[i]
		if got.Name != w.Name || got.Kind != w.Kind || got.NotNull != w.NotNull || got.PrimaryKey != w.PrimaryKey {
			t.Errorf("[%d] got %+v, want %+v", i, got, w)
		}
	}
}

func TestParseCreateTableMinimal(t *testing.T) {
	stmt := mustParse(t, `CREATE TABLE t (a INTEGER PRIMARY KEY)`)
	ct := stmt.(*CreateTableStatement)
	if ct.Name != "t" || len(ct.Columns) != 1 || ct.Columns[0].Name != "a" {
		t.Errorf("got %+v", ct)
	}
}

func TestParseCreateTableConstraintOrderIsFlexible(t *testing.T) {
	cases := []string{
		`CREATE TABLE t (id INTEGER PRIMARY KEY NOT NULL)`,
		`CREATE TABLE t (id INTEGER NOT NULL PRIMARY KEY)`,
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			ct := mustParse(t, src).(*CreateTableStatement)
			if !ct.Columns[0].NotNull || !ct.Columns[0].PrimaryKey {
				t.Errorf("constraints lost: %+v", ct.Columns[0])
			}
		})
	}
}

func TestParseRejectsCreateIndex(t *testing.T) {
	assertUnsupported(t, `CREATE INDEX idx_name ON users(name)`, "CREATE INDEX")
}

func TestParseRejectsCreateView(t *testing.T) {
	assertUnsupported(t, `CREATE VIEW v AS SELECT * FROM users`, "CREATE VIEW")
}

func TestParseRejectsUnsupportedColumnType(t *testing.T) {
	assertUnsupported(t, `CREATE TABLE t (x REAL)`, "REAL")
}

func TestParseRejectsUnsupportedConstraint(t *testing.T) {
	for _, tc := range []struct {
		src, want string
	}{
		{`CREATE TABLE t (x INTEGER UNIQUE)`, "UNIQUE"},
		{`CREATE TABLE t (x INTEGER CHECK 1)`, "CHECK"},
		{`CREATE TABLE t (x INTEGER DEFAULT 0)`, "DEFAULT"},
		{`CREATE TABLE t (x INTEGER REFERENCES y(id))`, "REFERENCES"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			assertUnsupported(t, tc.src, tc.want)
		})
	}
}

func TestParseRejectsMalformedCreateTable(t *testing.T) {
	cases := []struct {
		src, want string
	}{
		{`CREATE TABLE`, "expected"},
		{`CREATE TABLE t`, "expected"},
		{`CREATE TABLE t (`, "expected"},
		{`CREATE TABLE t (id)`, "expected column type"},
		{`CREATE TABLE t (id INTEGER, )`, "expected"}, // trailing comma
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			assertSyntax(t, tc.src, tc.want)
		})
	}
}

// ---- INSERT -------------------------------------------------------------

func TestParseInsertWithExplicitColumns(t *testing.T) {
	stmt := mustParse(t, `INSERT INTO users (id, name) VALUES (1, 'Felipe');`)
	ins := stmt.(*InsertStatement)
	if ins.Table != "users" {
		t.Errorf("Table = %q", ins.Table)
	}
	if !reflect.DeepEqual(ins.Columns, []string{"id", "name"}) {
		t.Errorf("Columns = %v", ins.Columns)
	}
	if len(ins.Values) != 2 {
		t.Fatalf("Values count = %d", len(ins.Values))
	}
	if i, ok := ins.Values[0].(*IntegerLiteral); !ok || i.Value != 1 {
		t.Errorf("Values[0] = %+v, want IntegerLiteral{1}", ins.Values[0])
	}
	if s, ok := ins.Values[1].(*StringLiteral); !ok || s.Value != "Felipe" {
		t.Errorf("Values[1] = %+v, want StringLiteral{Felipe}", ins.Values[1])
	}
}

func TestParseInsertWithoutColumns(t *testing.T) {
	stmt := mustParse(t, `INSERT INTO users VALUES (1, 'Felipe', true);`)
	ins := stmt.(*InsertStatement)
	if len(ins.Columns) != 0 {
		t.Errorf("Columns should be empty, got %v", ins.Columns)
	}
	if len(ins.Values) != 3 {
		t.Fatalf("Values count = %d", len(ins.Values))
	}
	if b, ok := ins.Values[2].(*BooleanLiteral); !ok || !b.Value {
		t.Errorf("Values[2] = %+v, want BooleanLiteral{true}", ins.Values[2])
	}
}

func TestParseInsertWithPlaceholder(t *testing.T) {
	ins := mustParse(t, `INSERT INTO t VALUES (?, ?, ?)`).(*InsertStatement)
	if len(ins.Values) != 3 {
		t.Fatalf("Values count = %d", len(ins.Values))
	}
	for i, v := range ins.Values {
		if _, ok := v.(*Placeholder); !ok {
			t.Errorf("Values[%d] = %T, want *Placeholder", i, v)
		}
	}
}

func TestParseInsertWithNull(t *testing.T) {
	ins := mustParse(t, `INSERT INTO t VALUES (1, NULL)`).(*InsertStatement)
	if _, ok := ins.Values[1].(*NullLiteral); !ok {
		t.Errorf("Values[1] = %T, want *NullLiteral", ins.Values[1])
	}
}

func TestParseRejectsMalformedInsert(t *testing.T) {
	cases := []string{
		`INSERT users VALUES (1)`,            // missing INTO
		`INSERT INTO users VALUES`,           // missing (
		`INSERT INTO users (id) VALUES (`,    // unterminated
		`INSERT INTO users (id) VALUES (,)`,  // empty expression
		`INSERT INTO users (id,) VALUES (1)`, // trailing comma
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			assertSyntax(t, src, "")
		})
	}
}

// ---- SELECT -------------------------------------------------------------

func TestParseSelectStar(t *testing.T) {
	sel := mustParse(t, `SELECT * FROM users`).(*SelectStatement)
	if !sel.Wildcard {
		t.Errorf("Wildcard = false, want true")
	}
	if sel.Table != "users" {
		t.Errorf("Table = %q", sel.Table)
	}
	if sel.Where != nil {
		t.Errorf("Where = %v, want nil", sel.Where)
	}
}

func TestParseSelectColumnList(t *testing.T) {
	sel := mustParse(t, `SELECT id, name, active FROM users`).(*SelectStatement)
	if sel.Wildcard {
		t.Errorf("Wildcard = true, want false")
	}
	want := []string{"id", "name", "active"}
	if !reflect.DeepEqual(sel.Columns, want) {
		t.Errorf("Columns = %v, want %v", sel.Columns, want)
	}
}

func TestParseSelectWhereEqualsLiteral(t *testing.T) {
	sel := mustParse(t, `SELECT * FROM users WHERE id = 42`).(*SelectStatement)
	be, ok := sel.Where.(*BinaryExpr)
	if !ok {
		t.Fatalf("Where = %T, want *BinaryExpr", sel.Where)
	}
	if be.Op != "=" {
		t.Errorf("Op = %q", be.Op)
	}
	if id, ok := be.Left.(*Identifier); !ok || id.Name != "id" {
		t.Errorf("Left = %+v", be.Left)
	}
	if lit, ok := be.Right.(*IntegerLiteral); !ok || lit.Value != 42 {
		t.Errorf("Right = %+v", be.Right)
	}
}

func TestParseSelectWhereEqualsPlaceholder(t *testing.T) {
	sel := mustParse(t, `SELECT * FROM users WHERE id = ?`).(*SelectStatement)
	be := sel.Where.(*BinaryExpr)
	if _, ok := be.Right.(*Placeholder); !ok {
		t.Errorf("Right = %T, want *Placeholder", be.Right)
	}
}

func TestParseSelectWhereEqualsString(t *testing.T) {
	sel := mustParse(t, `SELECT * FROM users WHERE name = 'Felipe'`).(*SelectStatement)
	be := sel.Where.(*BinaryExpr)
	if lit, ok := be.Right.(*StringLiteral); !ok || lit.Value != "Felipe" {
		t.Errorf("Right = %+v", be.Right)
	}
}

func TestParseRejectsJoin(t *testing.T) {
	assertUnsupported(t, `SELECT * FROM users JOIN posts`, "JOIN")
}

func TestParseRejectsGroupBy(t *testing.T) {
	assertUnsupported(t, `SELECT * FROM users GROUP BY name`, "GROUP BY")
}

func TestParseRejectsOrderBy(t *testing.T) {
	assertUnsupported(t, `SELECT * FROM users ORDER BY id`, "ORDER BY")
}

func TestParseRejectsLimit(t *testing.T) {
	assertUnsupported(t, `SELECT * FROM users LIMIT 10`, "LIMIT")
}

func TestParseRejectsOrderByAfterWhere(t *testing.T) {
	assertUnsupported(t, `SELECT * FROM users WHERE id = 1 ORDER BY name`, "ORDER BY")
}

func TestParseRejectsAndOrInWhere(t *testing.T) {
	assertUnsupported(t, `SELECT * FROM users WHERE id = 1 AND name = 'x'`, "AND")
	assertUnsupported(t, `SELECT * FROM users WHERE id = 1 OR id = 2`, "OR")
}

func TestParseRejectsLikeAndIn(t *testing.T) {
	assertUnsupported(t, `SELECT * FROM users WHERE name LIKE 'F%'`, "LIKE")
	assertUnsupported(t, `SELECT * FROM users WHERE id IN 1`, "IN")
}

func TestParseRejectsSubqueryInFrom(t *testing.T) {
	// SELECT * FROM (SELECT ...) — the parser hits the LPAREN
	// expecting an identifier; that surfaces as a syntax error in
	// this small grammar, not as ErrUnsupportedSQL. Either is
	// acceptable as long as the error is clear; we assert "expected"
	// (matching the syntax-error message).
	assertSyntax(t, `SELECT * FROM (SELECT * FROM users)`, "expected")
}

// ---- Statement-level rejection -----------------------------------------

func TestParseRejectsUpdate(t *testing.T) {
	assertUnsupported(t, `UPDATE users SET name = 'x' WHERE id = 1`, "UPDATE")
}

func TestParseRejectsDelete(t *testing.T) {
	assertUnsupported(t, `DELETE FROM users WHERE id = 1`, "DELETE")
}

func TestParseRejectsAlterTable(t *testing.T) {
	assertUnsupported(t, `ALTER TABLE users ADD COLUMN age INTEGER`, "ALTER TABLE")
}

func TestParseRejectsDropTable(t *testing.T) {
	assertUnsupported(t, `DROP TABLE users`, "DROP")
}

// ---- Boundary cases -----------------------------------------------------

func TestParseAcceptsOptionalTrailingSemicolon(t *testing.T) {
	for _, src := range []string{
		`SELECT * FROM users`,
		`SELECT * FROM users;`,
	} {
		t.Run(src, func(t *testing.T) {
			if _, err := Parse(src); err != nil {
				t.Errorf("Parse(%q): %v", src, err)
			}
		})
	}
}

func TestParseAllMultipleStatements(t *testing.T) {
	stmts, err := ParseAll(`
		CREATE TABLE users (id INTEGER PRIMARY KEY);
		INSERT INTO users VALUES (1);
		SELECT * FROM users;
	`)
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if len(stmts) != 3 {
		t.Fatalf("got %d statements, want 3", len(stmts))
	}
	if _, ok := stmts[0].(*CreateTableStatement); !ok {
		t.Errorf("stmts[0] = %T", stmts[0])
	}
	if _, ok := stmts[1].(*InsertStatement); !ok {
		t.Errorf("stmts[1] = %T", stmts[1])
	}
	if _, ok := stmts[2].(*SelectStatement); !ok {
		t.Errorf("stmts[2] = %T", stmts[2])
	}
}

func TestParseAllEmptyInput(t *testing.T) {
	stmts, err := ParseAll("")
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if len(stmts) != 0 {
		t.Errorf("got %d statements, want 0", len(stmts))
	}
}

func TestParseRejectsTrailingTokens(t *testing.T) {
	// Parse should reject multi-statement input (use ParseAll instead).
	_, err := Parse(`SELECT * FROM a SELECT * FROM b`)
	if !errors.Is(err, ErrSyntax) {
		t.Fatalf("err = %v, want wraps ErrSyntax", err)
	}
}

func TestSQLErrorIncludesLineAndColumn(t *testing.T) {
	src := "SELECT *\nFROM (SELECT * FROM users)"
	_, err := Parse(src)
	if err == nil {
		t.Fatalf("Parse: want error")
	}
	var sqlErr *SQLError
	if !errors.As(err, &sqlErr) {
		t.Fatalf("err = %v, want *SQLError", err)
	}
	if sqlErr.Pos.Line != 2 {
		t.Errorf("Line = %d, want 2", sqlErr.Pos.Line)
	}
	if !strings.Contains(sqlErr.Error(), "line 2") {
		t.Errorf("error message missing 'line 2': %q", sqlErr.Error())
	}
}

func TestParsePreservesPositions(t *testing.T) {
	stmt := mustParse(t, "  SELECT * FROM users")
	sel := stmt.(*SelectStatement)
	if sel.Pos.Column != 3 {
		t.Errorf("SELECT column = %d, want 3 (after two leading spaces)", sel.Pos.Column)
	}
}

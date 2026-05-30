package exec

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/felipegalante/godb/internal/catalog"
	"github.com/felipegalante/godb/internal/planner"
	"github.com/felipegalante/godb/internal/record"
	"github.com/felipegalante/godb/internal/sql"
	"github.com/felipegalante/godb/internal/storage"
)

// fixture wires up a temp .godb with a users table and returns
// the planner + executor + catalog (for test inspection).
func fixture(t *testing.T) (*planner.Planner, *Executor, *catalog.Catalog, *storage.Pager) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "exec.godb")
	p, err := storage.OpenPager(path, storage.PagerOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("OpenPager: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	c, err := catalog.Open(p)
	if err != nil {
		t.Fatalf("catalog.Open: %v", err)
	}
	return planner.New(c), New(p, c), c, p
}

func planFor(t *testing.T, pl *planner.Planner, src string) planner.Plan {
	t.Helper()
	stmt, err := sql.Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	plan, err := pl.Plan(stmt)
	if err != nil {
		t.Fatalf("Plan(%q): %v", src, err)
	}
	return plan
}

func createUsersTable(t *testing.T, pl *planner.Planner, ex *Executor) {
	t.Helper()
	plan := planFor(t, pl, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, active BOOLEAN)`)
	if _, err := ex.Run(plan, nil, "CREATE TABLE users (...)"); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
}

func insertUser(t *testing.T, pl *planner.Planner, ex *Executor, id int64, name string, active bool) Result {
	t.Helper()
	plan := planFor(t, pl, `INSERT INTO users VALUES (?, ?, ?)`)
	res, err := ex.Run(plan, []any{id, name, active}, "")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	return res
}

func TestExecCreateTableCreatesTable(t *testing.T) {
	pl, ex, c, _ := fixture(t)
	createUsersTable(t, pl, ex)
	if _, err := c.LookupTable("users"); err != nil {
		t.Fatalf("LookupTable: %v", err)
	}
}

func TestExecInsertWritesRow(t *testing.T) {
	pl, ex, _, _ := fixture(t)
	createUsersTable(t, pl, ex)
	res := insertUser(t, pl, ex, 1, "Felipe", true)
	if res.RowsAffected != 1 || res.LastInsertID != 1 {
		t.Errorf("Result = %+v, want {RowsAffected:1 LastInsertID:1}", res)
	}
	rows, err := ex.RunQuery(planFor(t, pl, `SELECT * FROM users`), nil)
	if err != nil {
		t.Fatalf("RunQuery: %v", err)
	}
	if len(rows.Values) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows.Values))
	}
	if rows.Values[0][1].Text != "Felipe" {
		t.Errorf("name = %q, want Felipe", rows.Values[0][1].Text)
	}
}

func TestExecInsertRejectsTypeMismatch(t *testing.T) {
	pl, ex, _, _ := fixture(t)
	createUsersTable(t, pl, ex)
	plan := planFor(t, pl, `INSERT INTO users VALUES (?, ?, ?)`)
	// id is INTEGER; passing a string in its slot should fail at
	// schema validation.
	_, err := ex.Run(plan, []any{"not-an-int", "Felipe", true}, "")
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want ErrTypeMismatch", err)
	}
}

func TestExecInsertRejectsNullInNotNull(t *testing.T) {
	pl, ex, _, _ := fixture(t)
	createUsersTable(t, pl, ex)
	plan := planFor(t, pl, `INSERT INTO users VALUES (?, ?, ?)`)
	// name is NOT NULL.
	_, err := ex.Run(plan, []any{int64(1), nil, true}, "")
	if !errors.Is(err, ErrNullViolation) {
		t.Fatalf("err = %v, want ErrNullViolation", err)
	}
}

func TestExecInsertRejectsDuplicatePK(t *testing.T) {
	pl, ex, _, _ := fixture(t)
	createUsersTable(t, pl, ex)
	insertUser(t, pl, ex, 1, "Felipe", true)
	plan := planFor(t, pl, `INSERT INTO users VALUES (?, ?, ?)`)
	_, err := ex.Run(plan, []any{int64(1), "Other", false}, "")
	if !errors.Is(err, ErrDuplicatePrimaryKey) {
		t.Fatalf("err = %v, want ErrDuplicatePrimaryKey", err)
	}
}

func TestExecQueryScanReturnsAllRows(t *testing.T) {
	pl, ex, _, _ := fixture(t)
	createUsersTable(t, pl, ex)
	// Insert 5 rows in arbitrary id order.
	for _, id := range []int64{3, 1, 5, 2, 4} {
		insertUser(t, pl, ex, id, fmt.Sprintf("user-%d", id), id%2 == 0)
	}
	rows, err := ex.RunQuery(planFor(t, pl, `SELECT * FROM users`), nil)
	if err != nil {
		t.Fatalf("RunQuery: %v", err)
	}
	if len(rows.Values) != 5 {
		t.Fatalf("got %d rows, want 5", len(rows.Values))
	}
	// Tree.Scan returns in key order — verify.
	for i, want := range []int64{1, 2, 3, 4, 5} {
		if rows.Values[i][0].Int != want {
			t.Errorf("rows[%d].id = %d, want %d", i, rows.Values[i][0].Int, want)
		}
	}
}

func TestExecQueryByPKReturnsOneRow(t *testing.T) {
	pl, ex, _, _ := fixture(t)
	createUsersTable(t, pl, ex)
	insertUser(t, pl, ex, 1, "Felipe", true)
	insertUser(t, pl, ex, 2, "MG", true)
	insertUser(t, pl, ex, 3, "Jane", false)
	rows, err := ex.RunQuery(planFor(t, pl, `SELECT * FROM users WHERE id = ?`), []any{int64(2)})
	if err != nil {
		t.Fatalf("RunQuery: %v", err)
	}
	if len(rows.Values) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows.Values))
	}
	if rows.Values[0][1].Text != "MG" {
		t.Errorf("name = %q, want MG", rows.Values[0][1].Text)
	}
}

func TestExecQueryByPKMissingReturnsEmpty(t *testing.T) {
	pl, ex, _, _ := fixture(t)
	createUsersTable(t, pl, ex)
	insertUser(t, pl, ex, 1, "Felipe", true)
	rows, err := ex.RunQuery(planFor(t, pl, `SELECT * FROM users WHERE id = ?`), []any{int64(999)})
	if err != nil {
		t.Fatalf("RunQuery: %v", err)
	}
	if len(rows.Values) != 0 {
		t.Errorf("got %d rows, want 0", len(rows.Values))
	}
}

func TestExecProjectionPicksColumns(t *testing.T) {
	pl, ex, _, _ := fixture(t)
	createUsersTable(t, pl, ex)
	insertUser(t, pl, ex, 1, "Felipe", true)
	insertUser(t, pl, ex, 2, "MG", false)
	rows, err := ex.RunQuery(planFor(t, pl, `SELECT name, active FROM users`), nil)
	if err != nil {
		t.Fatalf("RunQuery: %v", err)
	}
	if len(rows.Columns) != 2 || rows.Columns[0] != "name" || rows.Columns[1] != "active" {
		t.Errorf("Columns = %v, want [name active]", rows.Columns)
	}
	if len(rows.Values) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows.Values))
	}
	if rows.Values[0][0].Text != "Felipe" {
		t.Errorf("rows[0].name = %q", rows.Values[0][0].Text)
	}
}

func TestExecParameterBinding(t *testing.T) {
	pl, ex, _, _ := fixture(t)
	createUsersTable(t, pl, ex)
	// Multiple Go arg types bound to placeholders.
	plan := planFor(t, pl, `INSERT INTO users VALUES (?, ?, ?)`)
	if _, err := ex.Run(plan, []any{int(42), "ok", true}, ""); err != nil {
		t.Fatalf("int + string + bool: %v", err)
	}
	if _, err := ex.Run(plan, []any{int32(43), "ok2", false}, ""); err != nil {
		t.Fatalf("int32 + string + bool: %v", err)
	}
}

func TestExecParameterBindingCountMismatch(t *testing.T) {
	pl, ex, _, _ := fixture(t)
	createUsersTable(t, pl, ex)
	plan := planFor(t, pl, `INSERT INTO users VALUES (?, ?, ?)`)
	// Too few args.
	_, err := ex.Run(plan, []any{int64(1), "Felipe"}, "")
	if !errors.Is(err, ErrPlaceholderCountMismatch) {
		t.Fatalf("too few: err = %v, want ErrPlaceholderCountMismatch", err)
	}
	// Too many args.
	_, err = ex.Run(plan, []any{int64(1), "Felipe", true, "extra"}, "")
	if !errors.Is(err, ErrPlaceholderCountMismatch) {
		t.Fatalf("too many: err = %v, want ErrPlaceholderCountMismatch", err)
	}
}

func TestExecParameterBindingUnsupportedType(t *testing.T) {
	pl, ex, _, _ := fixture(t)
	createUsersTable(t, pl, ex)
	plan := planFor(t, pl, `INSERT INTO users VALUES (?, ?, ?)`)
	_, err := ex.Run(plan, []any{int64(1), "Felipe", 3.14}, "") // float not supported
	if !errors.Is(err, ErrUnsupportedArgType) {
		t.Fatalf("err = %v, want ErrUnsupportedArgType", err)
	}
}

// TestExecInsertGrowsTableRootIsPersisted proves the M6 SetTableRoot
// gap (closed in M8 commit 1) actually works through the executor:
// insert enough rows to force a table tree root split; close and
// reopen the database; verify the data survives.
func TestExecInsertGrowsTableRootIsPersisted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rootgrow.godb")

	// Phase 1: open, create, insert enough rows to root-split.
	p, err := storage.OpenPager(path, storage.PagerOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("OpenPager: %v", err)
	}
	c, err := catalog.Open(p)
	if err != nil {
		t.Fatalf("catalog.Open: %v", err)
	}
	pl := planner.New(c)
	ex := New(p, c)

	createPlan := planFor(t, pl, `CREATE TABLE wide (id INTEGER PRIMARY KEY, payload TEXT NOT NULL)`)
	if _, err := ex.Run(createPlan, nil, "CREATE TABLE wide ..."); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	initialInfo, _ := c.LookupTable("wide")
	initialRoot := initialInfo.RootPageID

	// Insert rows with ~400-byte payloads — forces a root split well before 1000 rows.
	largePayload := ""
	for i := 0; i < 400; i++ {
		largePayload += "x"
	}
	const insertCount = 500
	insertPlan := planFor(t, pl, `INSERT INTO wide VALUES (?, ?)`)
	for i := int64(1); i <= insertCount; i++ {
		if _, err := ex.Run(insertPlan, []any{i, largePayload}, ""); err != nil {
			t.Fatalf("INSERT(%d): %v", i, err)
		}
	}
	postInsertInfo, _ := c.LookupTable("wide")
	if postInsertInfo.RootPageID == initialRoot {
		t.Fatalf("root never changed after %d inserts; can't test persistence (test setup may need bigger payloads)", insertCount)
	}
	if err := c.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Phase 2: reopen and confirm the rows are still retrievable
	// via the persisted catalog root.
	p2, err := storage.OpenPager(path, storage.PagerOptions{})
	if err != nil {
		t.Fatalf("reopen OpenPager: %v", err)
	}
	t.Cleanup(func() { _ = p2.Close() })
	c2, err := catalog.Open(p2)
	if err != nil {
		t.Fatalf("reopen catalog.Open: %v", err)
	}
	pl2 := planner.New(c2)
	ex2 := New(p2, c2)

	// The catalog row should now show the post-split root.
	info2, _ := c2.LookupTable("wide")
	if info2.RootPageID != postInsertInfo.RootPageID {
		t.Errorf("reopened RootPageID = %d, want %d (persisted post-split root)",
			info2.RootPageID, postInsertInfo.RootPageID)
	}
	// Spot-check a row that almost certainly lives in a non-root
	// leaf (since the root grew, every row lives below the new
	// internal root).
	rows, err := ex2.RunQuery(planFor(t, pl2, `SELECT * FROM wide WHERE id = ?`), []any{int64(insertCount)})
	if err != nil {
		t.Fatalf("RunQuery after reopen: %v", err)
	}
	if len(rows.Values) != 1 {
		t.Fatalf("got %d rows after reopen, want 1 — persistence broken?", len(rows.Values))
	}
	if rows.Values[0][0].Int != insertCount {
		t.Errorf("id = %d, want %d", rows.Values[0][0].Int, insertCount)
	}
	if rows.Values[0][1].Kind != record.KindText {
		t.Errorf("payload kind = %v, want TEXT", rows.Values[0][1].Kind)
	}
}

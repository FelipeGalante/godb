package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run drives Run with the given stdin and args, returning the exit code
// and captured stdout/stderr.
func run(stdin string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

const schema = `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, active BOOLEAN);
INSERT INTO users VALUES (1, 'Felipe', true);
INSERT INTO users VALUES (2, 'O''Brien', false);
INSERT INTO users VALUES (3, 'NoActive', NULL);
`

func TestExecQueryDump(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "t.godb")
	script := writeFile(t, "schema.sql", schema)

	if code, _, errb := run("", db, "exec", script); code != 0 {
		t.Fatalf("exec exit=%d stderr=%q", code, errb)
	}

	// Table output.
	code, out, errb := run("", db, "query", "SELECT * FROM users")
	if code != 0 {
		t.Fatalf("query exit=%d stderr=%q", code, errb)
	}
	for _, want := range []string{"id | name", "Felipe", "O'Brien", "NoActive", "NULL"} {
		if !strings.Contains(out, want) {
			t.Errorf("query table output missing %q:\n%s", want, out)
		}
	}

	// CSV output.
	code, out, _ = run("", "-format", "csv", db, "query", "SELECT * FROM users")
	if code != 0 || !strings.Contains(out, "id,name,active") || !strings.Contains(out, "2,O'Brien,false") {
		t.Errorf("csv output wrong (exit %d):\n%s", code, out)
	}

	// Dump and reload into a fresh database, then query it back.
	code, dump, _ := run("", db, "dump")
	if code != 0 || !strings.Contains(dump, "INSERT INTO users") {
		t.Fatalf("dump wrong (exit %d):\n%s", code, dump)
	}
	dumpFile := writeFile(t, "dump.sql", dump)
	db2 := filepath.Join(dir, "t2.godb")
	if code, _, errb := run("", db2, "exec", dumpFile); code != 0 {
		t.Fatalf("reload exit=%d stderr=%q", code, errb)
	}
	_, out2, _ := run("", db2, "query", "SELECT * FROM users")
	if !strings.Contains(out2, "O'Brien") || !strings.Contains(out2, "NoActive") {
		t.Errorf("reloaded db query wrong:\n%s", out2)
	}
}

func TestInspectAndCheck(t *testing.T) {
	db := filepath.Join(t.TempDir(), "t.godb")
	script := writeFile(t, "schema.sql", schema)
	if code, _, errb := run("", db, "exec", script); code != 0 {
		t.Fatalf("exec exit=%d stderr=%q", code, errb)
	}

	if _, out, _ := run("", db, "inspect", "header"); !strings.Contains(out, "magic:              GODB") || !strings.Contains(out, "page count:") {
		t.Errorf("inspect header:\n%s", out)
	}
	if _, out, _ := run("", db, "inspect", "tree"); !strings.Contains(out, `table "users"`) || !strings.Contains(out, "leaf page") {
		t.Errorf("inspect tree:\n%s", out)
	}
	if _, out, _ := run("", db, "inspect", "page", "0"); !strings.Contains(out, "file header") {
		t.Errorf("inspect page 0:\n%s", out)
	}
	if _, out, _ := run("", db, "inspect", "page", "1"); !strings.Contains(out, "type:") {
		t.Errorf("inspect page 1:\n%s", out)
	}

	code, out, _ := run("", db, "check")
	if code != 0 {
		t.Errorf("check exit=%d (want 0):\n%s", code, out)
	}
	if !strings.Contains(out, "catalog tree: OK") || !strings.Contains(out, `table "users": OK`) {
		t.Errorf("check output:\n%s", out)
	}
}

func TestShellSession(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.godb")
	input := strings.Join([]string{
		"CREATE TABLE t (",
		"  id INTEGER PRIMARY KEY,",
		"  note TEXT",
		");",
		"INSERT INTO t VALUES (1, 'hi;there');",
		".tables",
		".schema t",
		"SELECT * FROM t;",
		".exit",
	}, "\n") + "\n"

	code, out, errb := run(input, db, "shell")
	if code != 0 {
		t.Fatalf("shell exit=%d stderr=%q", code, errb)
	}
	// .tables prints the name; the SELECT keeps the embedded ';' intact.
	if !strings.Contains(out, "t\n") {
		t.Errorf("shell .tables missing table name:\n%s", out)
	}
	if !strings.Contains(out, "hi;there") {
		t.Errorf("shell SELECT lost embedded semicolon:\n%s", out)
	}
	if !strings.Contains(out, "CREATE TABLE t") {
		t.Errorf("shell .schema missing CREATE:\n%s", out)
	}
}

func TestUsageAndErrors(t *testing.T) {
	if code, _, _ := run("", "-version"); code != 0 {
		t.Errorf("-version exit=%d, want 0", code)
	}
	if code, _, _ := run(""); code != 2 {
		t.Errorf("no args exit=%d, want 2", code)
	}
	db := filepath.Join(t.TempDir(), "x.godb")
	if code, _, _ := run("", db, "bogus"); code != 2 {
		t.Errorf("unknown command exit=%d, want 2", code)
	}
	missing := filepath.Join(t.TempDir(), "missing.godb")
	if code, _, errb := run("", missing, "query", "SELECT 1"); code != 1 || errb == "" {
		t.Errorf("missing-db query exit=%d stderr=%q, want exit 1 with message", code, errb)
	}
}

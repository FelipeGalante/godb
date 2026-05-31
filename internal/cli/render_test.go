package cli

import (
	"bytes"
	"testing"
)

func TestParseFormat(t *testing.T) {
	for _, s := range []string{"table", "TABLE", "csv", "CSV"} {
		if _, err := parseFormat(s); err != nil {
			t.Errorf("parseFormat(%q) unexpected error: %v", s, err)
		}
	}
	if _, err := parseFormat("json"); err == nil {
		t.Error("parseFormat(json): want error")
	}
}

func TestDisplayAndCSVCell(t *testing.T) {
	cases := []struct {
		v               any
		wantDisp, wantC string
	}{
		{nil, "NULL", ""},
		{int64(42), "42", "42"},
		{true, "true", "true"},
		{"hi", "hi", "hi"},
	}
	for _, c := range cases {
		if got := displayCell(c.v); got != c.wantDisp {
			t.Errorf("displayCell(%v) = %q, want %q", c.v, got, c.wantDisp)
		}
		if got := csvCell(c.v); got != c.wantC {
			t.Errorf("csvCell(%v) = %q, want %q", c.v, got, c.wantC)
		}
	}
}

func TestSQLLiteral(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{nil, "NULL"},
		{int64(7), "7"},
		{true, "TRUE"},
		{false, "FALSE"},
		{"plain", "'plain'"},
		{"O'Brien", "'O''Brien'"},
	}
	for _, c := range cases {
		if got := sqlLiteral(c.v); got != c.want {
			t.Errorf("sqlLiteral(%v) = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestRenderTable(t *testing.T) {
	var buf bytes.Buffer
	cols := []string{"id", "name"}
	data := [][]any{{int64(1), "Felipe"}, {int64(2), nil}}
	if err := renderTable(&buf, cols, data); err != nil {
		t.Fatalf("renderTable: %v", err)
	}
	want := "id | name\n---+-------\n1  | Felipe\n2  | NULL\n"
	if buf.String() != want {
		t.Errorf("renderTable:\n got %q\nwant %q", buf.String(), want)
	}
}

func TestRenderCSV(t *testing.T) {
	var buf bytes.Buffer
	cols := []string{"id", "name"}
	data := [][]any{{int64(1), "a,b"}, {int64(2), nil}}
	if err := renderCSV(&buf, cols, data); err != nil {
		t.Fatalf("renderCSV: %v", err)
	}
	want := "id,name\n1,\"a,b\"\n2,\n"
	if buf.String() != want {
		t.Errorf("renderCSV:\n got %q\nwant %q", buf.String(), want)
	}
}

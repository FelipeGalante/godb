package cli

import (
	"reflect"
	"testing"
)

func TestSplitStatements(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "two simple statements",
			src:  "CREATE TABLE t (id INTEGER PRIMARY KEY); INSERT INTO t VALUES (1);",
			want: []string{"CREATE TABLE t (id INTEGER PRIMARY KEY)", "INSERT INTO t VALUES (1)"},
		},
		{
			name: "semicolon inside string literal is not a separator",
			src:  "INSERT INTO t VALUES ('a;b'); INSERT INTO t VALUES ('c');",
			want: []string{"INSERT INTO t VALUES ('a;b')", "INSERT INTO t VALUES ('c')"},
		},
		{
			name: "doubled-quote escape stays in string",
			src:  "INSERT INTO t VALUES ('O''Brien;x');",
			want: []string{"INSERT INTO t VALUES ('O''Brien;x')"},
		},
		{
			name: "line comment hides a semicolon",
			src:  "SELECT 1 -- not a sep; really\n;",
			want: []string{"SELECT 1 -- not a sep; really"},
		},
		{
			name: "trailing statement without terminator",
			src:  "SELECT 1; SELECT 2",
			want: []string{"SELECT 1", "SELECT 2"},
		},
		{
			name: "empty and whitespace-only chunks dropped",
			src:  " ;  ; \n ;",
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitStatements(tc.src)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("splitStatements(%q)\n got = %#v\nwant = %#v", tc.src, got, tc.want)
			}
		})
	}
}

func TestLastTopLevelSemicolon(t *testing.T) {
	tests := []struct {
		src  string
		want int
	}{
		{"SELECT 1;", 9},
		{"SELECT 1; SELECT 2", 9},
		{"SELECT 1; SELECT 2;", 19},
		{"INSERT INTO t VALUES ('a;b')", -1},
		{"no terminator here", -1},
		{"-- ; in a comment\nSELECT 1", -1},
	}
	for _, tc := range tests {
		if got := lastTopLevelSemicolon(tc.src); got != tc.want {
			t.Errorf("lastTopLevelSemicolon(%q) = %d, want %d", tc.src, got, tc.want)
		}
	}
}

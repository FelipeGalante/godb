package sql

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
)

// drain consumes the lexer until EOF or error, returning the token
// stream. Lexer-error tokens stop the drain immediately.
func drain(t *testing.T, src string) ([]Token, error) {
	t.Helper()
	l := NewLexer(src)
	var out []Token
	for {
		tok, err := l.Next()
		if err != nil {
			return out, err
		}
		out = append(out, tok)
		if tok.Type == TokenEOF {
			return out, nil
		}
	}
}

func TestLexerTokenizesPunctuation(t *testing.T) {
	tokens, err := drain(t, "( ) , ; * = ?")
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	want := []TokenType{
		TokenLeftParen, TokenRightParen, TokenComma, TokenSemicolon,
		TokenAsterisk, TokenEqual, TokenPlaceholder, TokenEOF,
	}
	if len(tokens) != len(want) {
		t.Fatalf("got %d tokens, want %d", len(tokens), len(want))
	}
	for i, w := range want {
		if tokens[i].Type != w {
			t.Errorf("[%d] type = %v, want %v", i, tokens[i].Type, w)
		}
	}
}

func TestLexerSkipsWhitespaceAndComments(t *testing.T) {
	src := "  SELECT  -- this is a comment\n  *\n  FROM users -- trailing"
	tokens, err := drain(t, src)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	wantTypes := []TokenType{
		TokenKeywordSelect, TokenAsterisk, TokenKeywordFrom, TokenIdentifier, TokenEOF,
	}
	if len(tokens) != len(wantTypes) {
		t.Fatalf("got %d tokens, want %d (%v)", len(tokens), len(wantTypes), tokenTypes(tokens))
	}
	for i, w := range wantTypes {
		if tokens[i].Type != w {
			t.Errorf("[%d] type = %v, want %v", i, tokens[i].Type, w)
		}
	}
	// The asterisk should be on line 2.
	if tokens[1].Pos.Line != 2 {
		t.Errorf("'*' line = %d, want 2", tokens[1].Pos.Line)
	}
}

func TestLexerKeywordsAreCaseInsensitive(t *testing.T) {
	for _, s := range []string{"SELECT", "select", "Select", "sElEcT"} {
		l := NewLexer(s)
		tok, err := l.Next()
		if err != nil {
			t.Fatalf("Next(%q): %v", s, err)
		}
		if tok.Type != TokenKeywordSelect {
			t.Errorf("%q: type = %v, want TokenKeywordSelect", s, tok.Type)
		}
		// Lexeme preserves the original spelling.
		if tok.Lexeme != s {
			t.Errorf("%q: lexeme = %q, want %q", s, tok.Lexeme, s)
		}
	}
}

func TestLexerIdentifiersAreCaseSensitive(t *testing.T) {
	tokens, err := drain(t, "users Users USERS")
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	// Three identifiers + EOF.
	if len(tokens) != 4 {
		t.Fatalf("got %d tokens, want 4", len(tokens))
	}
	for i, w := range []string{"users", "Users", "USERS"} {
		if tokens[i].Type != TokenIdentifier {
			t.Errorf("[%d] type = %v, want TokenIdentifier", i, tokens[i].Type)
		}
		if tokens[i].Lexeme != w {
			t.Errorf("[%d] lexeme = %q, want %q", i, tokens[i].Lexeme, w)
		}
	}
}

func TestLexerIntegerLiterals(t *testing.T) {
	cases := []struct {
		src  string
		want int64
	}{
		{"0", 0},
		{"1", 1},
		{"42", 42},
		{strconv.FormatInt(math.MaxInt64, 10), math.MaxInt64},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			tok, err := NewLexer(tc.src).Next()
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if tok.Type != TokenInteger {
				t.Errorf("type = %v, want TokenInteger", tok.Type)
			}
			if tok.IntValue != tc.want {
				t.Errorf("IntValue = %d, want %d", tok.IntValue, tc.want)
			}
		})
	}
}

func TestLexerRejectsIntegerOverflow(t *testing.T) {
	// MaxInt64 + 1
	_, err := NewLexer("9223372036854775808").Next()
	if !errors.Is(err, ErrSyntax) {
		t.Fatalf("err = %v, want wraps ErrSyntax", err)
	}
}

func TestLexerStringLiterals(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"''", ""},
		{"'hello'", "hello"},
		{"'it''s'", "it's"},
		{"'a\nb'", "a\nb"},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			tok, err := NewLexer(tc.src).Next()
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if tok.Type != TokenString {
				t.Errorf("type = %v, want TokenString", tok.Type)
			}
			if tok.StrValue != tc.want {
				t.Errorf("StrValue = %q, want %q", tok.StrValue, tc.want)
			}
		})
	}
}

func TestLexerRejectsUnterminatedString(t *testing.T) {
	_, err := NewLexer("'oops").Next()
	var sqlErr *SQLError
	if !errors.As(err, &sqlErr) {
		t.Fatalf("err = %v, want *SQLError", err)
	}
	if !errors.Is(err, ErrSyntax) {
		t.Fatalf("err = %v, want wraps ErrSyntax", err)
	}
	if !strings.Contains(sqlErr.Message, "unterminated") {
		t.Errorf("message = %q, want it to mention 'unterminated'", sqlErr.Message)
	}
}

func TestLexerRejectsUnknownCharacter(t *testing.T) {
	_, err := NewLexer("SELECT @ FROM x").Next()
	// First Next returns SELECT successfully, second Next should error.
	l := NewLexer("SELECT @ FROM x")
	if _, err := l.Next(); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	_, err = l.Next()
	if !errors.Is(err, ErrSyntax) {
		t.Fatalf("err = %v, want wraps ErrSyntax", err)
	}
}

func TestLexerTracksLineAndColumn(t *testing.T) {
	src := "SELECT\n  *\n  FROM users"
	tokens, err := drain(t, src)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	want := []struct{ line, col int }{
		{1, 1},  // SELECT
		{2, 3},  // *
		{3, 3},  // FROM
		{3, 8},  // users
		{3, 13}, // EOF
	}
	for i, w := range want {
		if tokens[i].Pos.Line != w.line || tokens[i].Pos.Column != w.col {
			t.Errorf("[%d] pos = (%d,%d), want (%d,%d) — token %q",
				i, tokens[i].Pos.Line, tokens[i].Pos.Column,
				w.line, w.col, tokens[i].Lexeme)
		}
	}
}

func TestLexerPeekDoesNotAdvance(t *testing.T) {
	l := NewLexer("SELECT *")
	tok1, _ := l.Peek()
	tok2, _ := l.Peek()
	if tok1.Type != tok2.Type {
		t.Errorf("two Peeks differ: %v vs %v", tok1.Type, tok2.Type)
	}
	tok3, _ := l.Next()
	if tok3.Type != tok1.Type {
		t.Errorf("Next after Peek differs: %v vs %v", tok3.Type, tok1.Type)
	}
	tok4, _ := l.Next()
	if tok4.Type != TokenAsterisk {
		t.Errorf("second Next type = %v, want TokenAsterisk", tok4.Type)
	}
}

func TestLexerHandlesEOF(t *testing.T) {
	for _, src := range []string{"", "   ", "\n\n\t", "-- only comment"} {
		t.Run(strconv.Quote(src), func(t *testing.T) {
			tok, err := NewLexer(src).Next()
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if tok.Type != TokenEOF {
				t.Errorf("type = %v, want TokenEOF", tok.Type)
			}
		})
	}
}

func TestLexerKeywordTypes(t *testing.T) {
	cases := []struct {
		src  string
		want TokenType
	}{
		{"CREATE", TokenKeywordCreate},
		{"TABLE", TokenKeywordTable},
		{"INSERT", TokenKeywordInsert},
		{"INTO", TokenKeywordInto},
		{"VALUES", TokenKeywordValues},
		{"FROM", TokenKeywordFrom},
		{"WHERE", TokenKeywordWhere},
		{"PRIMARY", TokenKeywordPrimary},
		{"KEY", TokenKeywordKey},
		{"NOT", TokenKeywordNot},
		{"NULL", TokenKeywordNull},
		{"TRUE", TokenKeywordTrue},
		{"FALSE", TokenKeywordFalse},
		{"INTEGER", TokenKeywordInteger},
		{"TEXT", TokenKeywordText},
		{"BOOLEAN", TokenKeywordBoolean},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			tok, err := NewLexer(tc.src).Next()
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if tok.Type != tc.want {
				t.Errorf("type = %v, want %v", tok.Type, tc.want)
			}
		})
	}
}

func TestLexerRejectsOverlongIdentifier(t *testing.T) {
	huge := strings.Repeat("a", maxIdentLen+1)
	_, err := NewLexer(huge).Next()
	if !errors.Is(err, ErrSyntax) {
		t.Fatalf("err = %v, want wraps ErrSyntax", err)
	}
}

// tokenTypes is a tiny helper for nicer test failure messages.
func tokenTypes(toks []Token) []TokenType {
	out := make([]TokenType, len(toks))
	for i, t := range toks {
		out[i] = t.Type
	}
	return out
}

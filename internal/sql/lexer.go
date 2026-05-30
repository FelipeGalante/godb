package sql

import (
	"strconv"
	"strings"
)

// maxIdentLen caps identifier length to match catalog.maxNameLen. The
// parser doesn't reject long identifiers — the catalog will — but
// rejecting at the lexer prevents producing an absurd token that
// surfaces a confusing error one layer later.
const maxIdentLen = 255

// keywordTable maps lowercased keyword text to its TokenType. Matching
// is case-insensitive (SELECT == select == Select); identifiers are
// case-sensitive (users != Users).
var keywordTable = map[string]TokenType{
	"create":  TokenKeywordCreate,
	"table":   TokenKeywordTable,
	"insert":  TokenKeywordInsert,
	"into":    TokenKeywordInto,
	"values":  TokenKeywordValues,
	"select":  TokenKeywordSelect,
	"from":    TokenKeywordFrom,
	"where":   TokenKeywordWhere,
	"primary": TokenKeywordPrimary,
	"key":     TokenKeywordKey,
	"not":     TokenKeywordNot,
	"null":    TokenKeywordNull,
	"true":    TokenKeywordTrue,
	"false":   TokenKeywordFalse,
	"integer": TokenKeywordInteger,
	"text":    TokenKeywordText,
	"boolean": TokenKeywordBoolean,
}

// Lexer turns SQL source text into a stream of Tokens. One-token
// lookahead via Peek. Position tracking is 1-indexed for both line
// and column.
type Lexer struct {
	src    string
	pos    int  // byte offset into src
	line   int  // 1-based
	col    int  // 1-based
	peeked *Token
	peekErr error
}

// NewLexer returns a Lexer reading src. The source is not copied — keep
// it alive for the lexer's lifetime if it came from a transient buffer.
func NewLexer(src string) *Lexer {
	return &Lexer{src: src, line: 1, col: 1}
}

// Peek returns the next token without consuming it. Repeated Peek calls
// return the same token.
func (l *Lexer) Peek() (Token, error) {
	if l.peeked != nil {
		return *l.peeked, l.peekErr
	}
	tok, err := l.scan()
	l.peeked = &tok
	l.peekErr = err
	return tok, err
}

// Next returns and consumes the next token.
func (l *Lexer) Next() (Token, error) {
	if l.peeked != nil {
		tok := *l.peeked
		err := l.peekErr
		l.peeked = nil
		l.peekErr = nil
		return tok, err
	}
	return l.scan()
}

// scan is the inner one-token scanner. Skips whitespace + comments,
// then dispatches on the first significant character.
func (l *Lexer) scan() (Token, error) {
	if err := l.skipTrivia(); err != nil {
		return Token{}, err
	}
	if l.pos >= len(l.src) {
		return Token{Type: TokenEOF, Pos: l.curPos()}, nil
	}

	startPos := l.curPos()
	ch := l.src[l.pos]

	switch {
	case ch == '(':
		l.advance(1)
		return Token{Type: TokenLeftParen, Lexeme: "(", Pos: startPos}, nil
	case ch == ')':
		l.advance(1)
		return Token{Type: TokenRightParen, Lexeme: ")", Pos: startPos}, nil
	case ch == ',':
		l.advance(1)
		return Token{Type: TokenComma, Lexeme: ",", Pos: startPos}, nil
	case ch == ';':
		l.advance(1)
		return Token{Type: TokenSemicolon, Lexeme: ";", Pos: startPos}, nil
	case ch == '*':
		l.advance(1)
		return Token{Type: TokenAsterisk, Lexeme: "*", Pos: startPos}, nil
	case ch == '=':
		l.advance(1)
		return Token{Type: TokenEqual, Lexeme: "=", Pos: startPos}, nil
	case ch == '?':
		l.advance(1)
		return Token{Type: TokenPlaceholder, Lexeme: "?", Pos: startPos}, nil
	case ch == '\'':
		return l.scanString(startPos)
	case ch >= '0' && ch <= '9':
		return l.scanInteger(startPos)
	case isIdentStart(ch):
		return l.scanIdentOrKeyword(startPos)
	default:
		return Token{}, newSyntaxError(startPos, "unexpected character %q", ch)
	}
}

// skipTrivia advances over whitespace and line comments. Returns an
// error only if a comment turns out to be malformed (none in v0.1, so
// always nil; the signature reserves room for /* */ comments later).
func (l *Lexer) skipTrivia() error {
	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\r':
			l.advance(1)
		case ch == '\n':
			l.advance(1)
		case ch == '-' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '-':
			// Line comment: skip to end of line (or EOF).
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.advance(1)
			}
		default:
			return nil
		}
	}
	return nil
}

// scanString reads a '…' literal. SQL standard: a single quote inside
// the string is written as ''. The decoded value (with '' → ') is
// stored in StrValue; Lexeme keeps the original including the quotes.
func (l *Lexer) scanString(startPos Position) (Token, error) {
	start := l.pos // byte offset of the opening quote
	// Consume the opening quote.
	l.advance(1)
	var sb strings.Builder
	for {
		if l.pos >= len(l.src) {
			return Token{}, newSyntaxError(startPos, "unterminated string literal")
		}
		ch := l.src[l.pos]
		if ch == '\'' {
			// Check for the '' escape.
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '\'' {
				sb.WriteByte('\'')
				l.advance(2)
				continue
			}
			// Closing quote.
			l.advance(1)
			return Token{
				Type:     TokenString,
				Lexeme:   l.src[start:l.pos],
				StrValue: sb.String(),
				Pos:      startPos,
			}, nil
		}
		sb.WriteByte(ch)
		l.advance(1)
	}
}

// scanInteger reads a [0-9]+ literal into IntValue. Overflow returns an
// error.
func (l *Lexer) scanInteger(startPos Position) (Token, error) {
	start := l.pos
	for l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
		l.advance(1)
	}
	lexeme := l.src[start:l.pos]
	v, err := strconv.ParseInt(lexeme, 10, 64)
	if err != nil {
		return Token{}, newSyntaxError(startPos, "integer literal out of range: %s", lexeme)
	}
	return Token{
		Type:     TokenInteger,
		Lexeme:   lexeme,
		IntValue: v,
		Pos:      startPos,
	}, nil
}

// scanIdentOrKeyword reads an identifier and looks it up against the
// keyword table. The original Lexeme is preserved (case-sensitive) so
// the parser sees the user's spelling; the TokenType is the keyword's
// if matched.
func (l *Lexer) scanIdentOrKeyword(startPos Position) (Token, error) {
	start := l.pos
	for l.pos < len(l.src) && isIdentContinue(l.src[l.pos]) {
		l.advance(1)
	}
	lexeme := l.src[start:l.pos]
	if len(lexeme) > maxIdentLen {
		return Token{}, newSyntaxError(startPos, "identifier exceeds maximum length of %d bytes", maxIdentLen)
	}
	tt := TokenIdentifier
	if kw, ok := keywordTable[strings.ToLower(lexeme)]; ok {
		tt = kw
	}
	return Token{Type: tt, Lexeme: lexeme, Pos: startPos}, nil
}

// curPos returns the lexer's current 1-indexed source position.
func (l *Lexer) curPos() Position {
	return Position{Line: l.line, Column: l.col}
}

// advance moves the byte position forward by n, updating line+col.
// Caller must ensure pos+n <= len(src).
func (l *Lexer) advance(n int) {
	for i := 0; i < n; i++ {
		if l.pos >= len(l.src) {
			return
		}
		if l.src[l.pos] == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		l.pos++
	}
}

// isIdentStart reports whether ch can start an identifier ([A-Za-z_]).
func isIdentStart(ch byte) bool {
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || ch == '_'
}

// isIdentContinue reports whether ch can appear inside an identifier
// after the first byte: letters, digits, or underscore.
func isIdentContinue(ch byte) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}

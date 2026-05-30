// Package sql tokenizes and parses GoDB's SQL subset into a typed AST.
//
// The grammar is deliberately small (see ADR-0015 and docs/book/09):
// CREATE TABLE, INSERT INTO, SELECT — with a restricted WHERE clause.
// Everything outside that grammar is recognized and rejected with
// ErrUnsupportedSQL rather than a confusing syntax error.
//
// This package does NO execution. It produces an AST that M9's
// executor will (eventually) consume.
package sql

// TokenType identifies a token's kind. Values are private to the
// package — callers compare tokens by Type, never by ordinal.
type TokenType int

const (
	TokenEOF TokenType = iota
	TokenIdentifier
	TokenInteger
	TokenString
	TokenPlaceholder // ?
	TokenComma       // ,
	TokenSemicolon   // ;
	TokenLeftParen   // (
	TokenRightParen  // )
	TokenAsterisk    // *
	TokenEqual       // =

	// Keywords. Matched case-insensitively by the lexer; the original
	// lexeme is preserved on the Token for error formatting.
	TokenKeywordCreate
	TokenKeywordTable
	TokenKeywordInsert
	TokenKeywordInto
	TokenKeywordValues
	TokenKeywordSelect
	TokenKeywordFrom
	TokenKeywordWhere
	TokenKeywordPrimary
	TokenKeywordKey
	TokenKeywordNot
	TokenKeywordNull
	TokenKeywordTrue
	TokenKeywordFalse
	TokenKeywordInteger
	TokenKeywordText
	TokenKeywordBoolean
)

// String returns a human-readable label for use in error messages.
func (t TokenType) String() string {
	switch t {
	case TokenEOF:
		return "EOF"
	case TokenIdentifier:
		return "identifier"
	case TokenInteger:
		return "integer literal"
	case TokenString:
		return "string literal"
	case TokenPlaceholder:
		return "'?'"
	case TokenComma:
		return "','"
	case TokenSemicolon:
		return "';'"
	case TokenLeftParen:
		return "'('"
	case TokenRightParen:
		return "')'"
	case TokenAsterisk:
		return "'*'"
	case TokenEqual:
		return "'='"
	case TokenKeywordCreate:
		return "'CREATE'"
	case TokenKeywordTable:
		return "'TABLE'"
	case TokenKeywordInsert:
		return "'INSERT'"
	case TokenKeywordInto:
		return "'INTO'"
	case TokenKeywordValues:
		return "'VALUES'"
	case TokenKeywordSelect:
		return "'SELECT'"
	case TokenKeywordFrom:
		return "'FROM'"
	case TokenKeywordWhere:
		return "'WHERE'"
	case TokenKeywordPrimary:
		return "'PRIMARY'"
	case TokenKeywordKey:
		return "'KEY'"
	case TokenKeywordNot:
		return "'NOT'"
	case TokenKeywordNull:
		return "'NULL'"
	case TokenKeywordTrue:
		return "'TRUE'"
	case TokenKeywordFalse:
		return "'FALSE'"
	case TokenKeywordInteger:
		return "'INTEGER'"
	case TokenKeywordText:
		return "'TEXT'"
	case TokenKeywordBoolean:
		return "'BOOLEAN'"
	default:
		return "unknown"
	}
}

// Position is the source location of a token or AST node, 1-indexed
// for both Line and Column (matching standard editor conventions).
type Position struct {
	Line   int
	Column int
}

// Token is one tokenized lexeme.
type Token struct {
	Type     TokenType
	Lexeme   string   // original source text
	IntValue int64    // valid when Type == TokenInteger
	StrValue string   // valid when Type == TokenString (with '' unescaped to ')
	Pos      Position // 1-indexed source position of the first character
}

package sql

import (
	"strings"

	"github.com/felipegalante/godb/internal/record"
)

// Parse parses exactly one SQL statement from src. Returns an error if
// src is malformed, if the construct is recognized but unsupported, or
// if there are trailing tokens after the statement (use ParseAll for
// multi-statement scripts).
func Parse(src string) (Statement, error) {
	p, err := newParser(src)
	if err != nil {
		return nil, err
	}
	stmt, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	// Optional trailing semicolon.
	if _, ok, err := p.consume(TokenSemicolon); err != nil {
		return nil, err
	} else if ok {
		// fine
	}
	if p.cur.Type != TokenEOF {
		return nil, newSyntaxError(p.cur.Pos,
			"unexpected %s after statement (use ParseAll for multi-statement input)",
			describeToken(p.cur))
	}
	return stmt, nil
}

// ParseAll parses zero or more statements from src, separated by `;`.
// A trailing semicolon is allowed. Empty input returns an empty slice.
func ParseAll(src string) ([]Statement, error) {
	p, err := newParser(src)
	if err != nil {
		return nil, err
	}
	var out []Statement
	for p.cur.Type != TokenEOF {
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		out = append(out, stmt)
		// Consume the optional `;` between statements.
		if _, ok, err := p.consume(TokenSemicolon); err != nil {
			return nil, err
		} else if !ok && p.cur.Type != TokenEOF {
			return nil, newSyntaxError(p.cur.Pos,
				"expected ';' or end of input between statements, got %s",
				describeToken(p.cur))
		}
	}
	return out, nil
}

// ---- parser internals ---------------------------------------------------

type parser struct {
	lex *Lexer
	cur Token // current token (already loaded)
}

func newParser(src string) (*parser, error) {
	p := &parser{lex: NewLexer(src)}
	if err := p.advance(); err != nil {
		return nil, err
	}
	return p, nil
}

// advance pulls the next token into p.cur.
func (p *parser) advance() error {
	tok, err := p.lex.Next()
	if err != nil {
		return err
	}
	p.cur = tok
	return nil
}

// expect consumes the current token if it matches t, returns it, and
// advances. Otherwise returns a syntax error.
func (p *parser) expect(t TokenType, context string) (Token, error) {
	if p.cur.Type != t {
		return Token{}, newSyntaxError(p.cur.Pos,
			"expected %s%s, got %s", t, contextSuffix(context), describeToken(p.cur))
	}
	tok := p.cur
	if err := p.advance(); err != nil {
		return Token{}, err
	}
	return tok, nil
}

// consume advances past the current token if it matches t, reporting
// whether it did. Returns an error only on lexer failures while
// advancing.
func (p *parser) consume(t TokenType) (Token, bool, error) {
	if p.cur.Type != t {
		return Token{}, false, nil
	}
	tok := p.cur
	if err := p.advance(); err != nil {
		return Token{}, false, err
	}
	return tok, true, nil
}

// parseStatement dispatches on the leading keyword. Recognizes (and
// politely rejects) the major unsupported statement-kinds via
// ErrUnsupportedSQL.
func (p *parser) parseStatement() (Statement, error) {
	switch p.cur.Type {
	case TokenKeywordCreate:
		return p.parseCreate()
	case TokenKeywordInsert:
		return p.parseInsert()
	case TokenKeywordSelect:
		return p.parseSelect()
	case TokenIdentifier:
		// Common unsupported leading keywords arrive as identifiers
		// because the lexer doesn't tokenize them as keywords.
		switch strings.ToUpper(p.cur.Lexeme) {
		case "UPDATE":
			return nil, newUnsupportedError(p.cur.Pos, "UPDATE is not supported in GoDB v0.1")
		case "DELETE":
			return nil, newUnsupportedError(p.cur.Pos, "DELETE is not supported in GoDB v0.1")
		case "ALTER":
			return nil, newUnsupportedError(p.cur.Pos, "ALTER TABLE is not supported in GoDB v0.1")
		case "DROP":
			return nil, newUnsupportedError(p.cur.Pos, "DROP is not supported in GoDB v0.1")
		case "REPLACE":
			return nil, newUnsupportedError(p.cur.Pos, "REPLACE is not supported in GoDB v0.1")
		}
	}
	return nil, newSyntaxError(p.cur.Pos,
		"expected start of a statement (CREATE/INSERT/SELECT), got %s", describeToken(p.cur))
}

// parseCreate handles the CREATE keyword. The only supported follower
// is TABLE; CREATE INDEX is recognized and rejected.
func (p *parser) parseCreate() (Statement, error) {
	createPos := p.cur.Pos
	if err := p.advance(); err != nil {
		return nil, err
	}
	// What's next?
	switch p.cur.Type {
	case TokenKeywordTable:
		return p.parseCreateTableTail(createPos)
	case TokenIdentifier:
		// CREATE INDEX, CREATE VIEW, etc.
		switch strings.ToUpper(p.cur.Lexeme) {
		case "INDEX":
			return nil, newUnsupportedError(p.cur.Pos, "CREATE INDEX is not supported in GoDB v0.1")
		case "VIEW":
			return nil, newUnsupportedError(p.cur.Pos, "CREATE VIEW is not supported in GoDB v0.1")
		case "TRIGGER":
			return nil, newUnsupportedError(p.cur.Pos, "CREATE TRIGGER is not supported in GoDB v0.1")
		}
	}
	return nil, newSyntaxError(p.cur.Pos, "expected 'TABLE' after 'CREATE', got %s", describeToken(p.cur))
}

// parseCreateTableTail reads `<name> ( column_def {, column_def} )`
// after the `CREATE TABLE` prefix.
func (p *parser) parseCreateTableTail(createPos Position) (Statement, error) {
	if err := p.advance(); err != nil { // consume TABLE
		return nil, err
	}
	nameTok, err := p.expect(TokenIdentifier, " (table name)")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenLeftParen, " (start of column list)"); err != nil {
		return nil, err
	}
	var cols []ColumnDef
	for {
		def, err := p.parseColumnDef()
		if err != nil {
			return nil, err
		}
		cols = append(cols, def)
		if _, ok, err := p.consume(TokenComma); err != nil {
			return nil, err
		} else if !ok {
			break
		}
	}
	if _, err := p.expect(TokenRightParen, " (end of column list)"); err != nil {
		return nil, err
	}
	return &CreateTableStatement{Name: nameTok.Lexeme, Columns: cols, Pos: createPos}, nil
}

// parseColumnDef reads `name <type> {constraint}`.
func (p *parser) parseColumnDef() (ColumnDef, error) {
	nameTok, err := p.expect(TokenIdentifier, " (column name)")
	if err != nil {
		return ColumnDef{}, err
	}
	def := ColumnDef{Name: nameTok.Lexeme, Pos: nameTok.Pos}
	// Type.
	switch p.cur.Type {
	case TokenKeywordInteger:
		def.Kind = record.KindInteger
	case TokenKeywordText:
		def.Kind = record.KindText
	case TokenKeywordBoolean:
		def.Kind = record.KindBoolean
	default:
		// Reject types we don't support (REAL, FLOAT, BLOB, NUMERIC, etc.).
		if p.cur.Type == TokenIdentifier {
			return ColumnDef{}, newUnsupportedError(p.cur.Pos,
				"column type %q is not supported in GoDB v0.1 (use INTEGER, TEXT, or BOOLEAN)",
				p.cur.Lexeme)
		}
		return ColumnDef{}, newSyntaxError(p.cur.Pos,
			"expected column type (INTEGER, TEXT, or BOOLEAN), got %s", describeToken(p.cur))
	}
	if err := p.advance(); err != nil {
		return ColumnDef{}, err
	}
	// Constraints, in any order, until the next ',' or ')'.
	for {
		switch p.cur.Type {
		case TokenKeywordNot:
			if err := p.advance(); err != nil {
				return ColumnDef{}, err
			}
			if _, err := p.expect(TokenKeywordNull, " (after NOT)"); err != nil {
				return ColumnDef{}, err
			}
			def.NotNull = true
		case TokenKeywordPrimary:
			if err := p.advance(); err != nil {
				return ColumnDef{}, err
			}
			if _, err := p.expect(TokenKeywordKey, " (after PRIMARY)"); err != nil {
				return ColumnDef{}, err
			}
			def.PrimaryKey = true
		case TokenIdentifier:
			// Recognize and reject common unsupported constraints.
			switch strings.ToUpper(p.cur.Lexeme) {
			case "UNIQUE":
				return ColumnDef{}, newUnsupportedError(p.cur.Pos, "UNIQUE constraint is not supported in GoDB v0.1")
			case "CHECK":
				return ColumnDef{}, newUnsupportedError(p.cur.Pos, "CHECK constraint is not supported in GoDB v0.1")
			case "DEFAULT":
				return ColumnDef{}, newUnsupportedError(p.cur.Pos, "DEFAULT is not supported in GoDB v0.1")
			case "REFERENCES":
				return ColumnDef{}, newUnsupportedError(p.cur.Pos, "REFERENCES (foreign keys) is not supported in GoDB v0.1")
			case "COLLATE":
				return ColumnDef{}, newUnsupportedError(p.cur.Pos, "COLLATE is not supported in GoDB v0.1")
			}
			// Otherwise an unexpected identifier ends the column.
			return def, nil
		default:
			return def, nil
		}
	}
}

// parseInsert handles `INSERT INTO name [ ( col {, col} ) ] VALUES ( expr {, expr} )`.
func (p *parser) parseInsert() (Statement, error) {
	startPos := p.cur.Pos
	if err := p.advance(); err != nil { // consume INSERT
		return nil, err
	}
	if _, err := p.expect(TokenKeywordInto, " (after INSERT)"); err != nil {
		return nil, err
	}
	tableTok, err := p.expect(TokenIdentifier, " (table name)")
	if err != nil {
		return nil, err
	}
	stmt := &InsertStatement{Table: tableTok.Lexeme, Pos: startPos}
	// Optional explicit column list.
	if _, ok, err := p.consume(TokenLeftParen); err != nil {
		return nil, err
	} else if ok {
		for {
			tok, err := p.expect(TokenIdentifier, " (column name in INSERT list)")
			if err != nil {
				return nil, err
			}
			stmt.Columns = append(stmt.Columns, tok.Lexeme)
			if _, ok, err := p.consume(TokenComma); err != nil {
				return nil, err
			} else if !ok {
				break
			}
		}
		if _, err := p.expect(TokenRightParen, " (end of INSERT column list)"); err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(TokenKeywordValues, " (before INSERT values)"); err != nil {
		return nil, err
	}
	if _, err := p.expect(TokenLeftParen, " (start of INSERT values)"); err != nil {
		return nil, err
	}
	for {
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		stmt.Values = append(stmt.Values, expr)
		if _, ok, err := p.consume(TokenComma); err != nil {
			return nil, err
		} else if !ok {
			break
		}
	}
	if _, err := p.expect(TokenRightParen, " (end of INSERT values)"); err != nil {
		return nil, err
	}
	return stmt, nil
}

// parseSelect handles `SELECT (* | col {, col}) FROM name [WHERE expr]`.
func (p *parser) parseSelect() (Statement, error) {
	startPos := p.cur.Pos
	if err := p.advance(); err != nil { // consume SELECT
		return nil, err
	}
	stmt := &SelectStatement{Pos: startPos}
	if _, ok, err := p.consume(TokenAsterisk); err != nil {
		return nil, err
	} else if ok {
		stmt.Wildcard = true
	} else {
		for {
			tok, err := p.expect(TokenIdentifier, " (column in SELECT list)")
			if err != nil {
				return nil, err
			}
			stmt.Columns = append(stmt.Columns, tok.Lexeme)
			if _, ok, err := p.consume(TokenComma); err != nil {
				return nil, err
			} else if !ok {
				break
			}
		}
	}
	if _, err := p.expect(TokenKeywordFrom, " (after SELECT list)"); err != nil {
		return nil, err
	}
	tableTok, err := p.expect(TokenIdentifier, " (table name after FROM)")
	if err != nil {
		return nil, err
	}
	stmt.Table = tableTok.Lexeme

	// Reject FROM subqueries: SELECT * FROM (SELECT ...).
	// (We consumed FROM and expected an identifier; an LPAREN would
	// have produced a syntax error above. But if a SQL author wrote
	// SELECT * FROM (...), we want a nicer message — peek before
	// expecting the identifier.)
	// This message is reached by the explicit parseSubquery check below.

	// Reject JOIN if it appears after the table name.
	if p.cur.Type == TokenIdentifier {
		switch strings.ToUpper(p.cur.Lexeme) {
		case "JOIN", "INNER", "LEFT", "RIGHT", "FULL", "CROSS":
			return nil, newUnsupportedError(p.cur.Pos,
				"JOIN is not supported in GoDB v0.1")
		case "GROUP":
			return nil, newUnsupportedError(p.cur.Pos,
				"GROUP BY is not supported in GoDB v0.1")
		case "ORDER":
			return nil, newUnsupportedError(p.cur.Pos,
				"ORDER BY is not supported in GoDB v0.1")
		case "LIMIT":
			return nil, newUnsupportedError(p.cur.Pos,
				"LIMIT is not supported in GoDB v0.1")
		case "HAVING":
			return nil, newUnsupportedError(p.cur.Pos,
				"HAVING is not supported in GoDB v0.1")
		case "OFFSET":
			return nil, newUnsupportedError(p.cur.Pos,
				"OFFSET is not supported in GoDB v0.1")
		}
	}

	// Optional WHERE.
	if _, ok, err := p.consume(TokenKeywordWhere); err != nil {
		return nil, err
	} else if ok {
		where, err := p.parseWhere()
		if err != nil {
			return nil, err
		}
		stmt.Where = where
	}

	// After WHERE (or in its absence), the same family of trailing
	// unsupported clauses must be rejected. Check once more.
	if p.cur.Type == TokenIdentifier {
		switch strings.ToUpper(p.cur.Lexeme) {
		case "GROUP":
			return nil, newUnsupportedError(p.cur.Pos, "GROUP BY is not supported in GoDB v0.1")
		case "ORDER":
			return nil, newUnsupportedError(p.cur.Pos, "ORDER BY is not supported in GoDB v0.1")
		case "LIMIT":
			return nil, newUnsupportedError(p.cur.Pos, "LIMIT is not supported in GoDB v0.1")
		case "HAVING":
			return nil, newUnsupportedError(p.cur.Pos, "HAVING is not supported in GoDB v0.1")
		case "OFFSET":
			return nil, newUnsupportedError(p.cur.Pos, "OFFSET is not supported in GoDB v0.1")
		}
	}
	return stmt, nil
}

// parseWhere parses `column = expression`. Compound predicates with
// AND/OR and non-equality operators are recognized and rejected.
func (p *parser) parseWhere() (Expression, error) {
	leftTok, err := p.expect(TokenIdentifier, " (column name in WHERE)")
	if err != nil {
		return nil, err
	}
	left := &Identifier{Name: leftTok.Lexeme, Pos: leftTok.Pos}

	// Equality operator.
	if p.cur.Type != TokenEqual {
		// Non-equality comparison operators or compound predicates.
		if p.cur.Type == TokenIdentifier {
			switch strings.ToUpper(p.cur.Lexeme) {
			case "AND":
				return nil, newUnsupportedError(p.cur.Pos, "compound predicates with AND are not supported in GoDB v0.1")
			case "OR":
				return nil, newUnsupportedError(p.cur.Pos, "compound predicates with OR are not supported in GoDB v0.1")
			case "LIKE", "IN", "BETWEEN", "IS":
				return nil, newUnsupportedError(p.cur.Pos,
					"comparison %q is not supported in GoDB v0.1 (only '=' is)", strings.ToUpper(p.cur.Lexeme))
			}
		}
		// Punctuation-style comparisons (<, >, !=) aren't tokenized in
		// M7, so we can detect by lexeme/character — but since they're
		// not lexer tokens, the lex step itself would have errored on
		// the first unknown character. We'll generate that path from
		// the lexer's unknown-character branch; here we just report
		// the unexpected current token.
		return nil, newSyntaxError(p.cur.Pos,
			"expected '=' after column in WHERE, got %s", describeToken(p.cur))
	}
	eqPos := p.cur.Pos
	if err := p.advance(); err != nil {
		return nil, err
	}
	right, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	// Reject compound predicates after the rhs.
	if p.cur.Type == TokenIdentifier {
		switch strings.ToUpper(p.cur.Lexeme) {
		case "AND":
			return nil, newUnsupportedError(p.cur.Pos, "compound predicates with AND are not supported in GoDB v0.1")
		case "OR":
			return nil, newUnsupportedError(p.cur.Pos, "compound predicates with OR are not supported in GoDB v0.1")
		}
	}
	return &BinaryExpr{Op: "=", Left: left, Right: right, Pos: eqPos}, nil
}

// parseExpression parses one primary expression — a literal, a
// placeholder, or an identifier. Arithmetic and compound predicates
// are out of scope (v0.2+).
func (p *parser) parseExpression() (Expression, error) {
	switch p.cur.Type {
	case TokenInteger:
		tok := p.cur
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &IntegerLiteral{Value: tok.IntValue, Pos: tok.Pos}, nil
	case TokenString:
		tok := p.cur
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &StringLiteral{Value: tok.StrValue, Pos: tok.Pos}, nil
	case TokenKeywordTrue:
		pos := p.cur.Pos
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &BooleanLiteral{Value: true, Pos: pos}, nil
	case TokenKeywordFalse:
		pos := p.cur.Pos
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &BooleanLiteral{Value: false, Pos: pos}, nil
	case TokenKeywordNull:
		pos := p.cur.Pos
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &NullLiteral{Pos: pos}, nil
	case TokenPlaceholder:
		pos := p.cur.Pos
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &Placeholder{Pos: pos}, nil
	case TokenIdentifier:
		tok := p.cur
		if err := p.advance(); err != nil {
			return nil, err
		}
		return &Identifier{Name: tok.Lexeme, Pos: tok.Pos}, nil
	case TokenLeftParen:
		return nil, newUnsupportedError(p.cur.Pos,
			"parenthesized expressions and subqueries are not supported in GoDB v0.1")
	}
	return nil, newSyntaxError(p.cur.Pos, "expected an expression, got %s", describeToken(p.cur))
}

// describeToken formats a token for error messages.
func describeToken(t Token) string {
	if t.Type == TokenEOF {
		return "end of input"
	}
	if t.Lexeme != "" {
		// For an identifier or keyword the lexeme is the user's text;
		// quote it so messages like `expected ')' got 'WHERE'` look
		// right. For punctuation t.Type already includes quotes.
		switch t.Type {
		case TokenIdentifier, TokenInteger, TokenString:
			return "'" + t.Lexeme + "'"
		default:
			return t.Type.String()
		}
	}
	return t.Type.String()
}

func contextSuffix(context string) string {
	if context == "" {
		return ""
	}
	return context
}

package sql

// Expression is the common interface for every parsed expression.
// Implementations carry a Position for error messages.
//
// In v0.1 the expression grammar is tiny: literals (integer, string,
// boolean, NULL), the `?` placeholder, identifier references, and
// equality (`=`) — only inside the WHERE clause. No arithmetic, no
// function calls, no compound predicates with AND/OR. Each future
// milestone may extend this with new node types and Op values.
type Expression interface {
	expressionNode()
	Position() Position
}

// IntegerLiteral is `42`, `0`, etc. Always int64.
type IntegerLiteral struct {
	Value int64
	Pos   Position
}

func (e *IntegerLiteral) expressionNode()    {}
func (e *IntegerLiteral) Position() Position { return e.Pos }

// StringLiteral is `'…'` after `''` un-escaping.
type StringLiteral struct {
	Value string
	Pos   Position
}

func (e *StringLiteral) expressionNode()    {}
func (e *StringLiteral) Position() Position { return e.Pos }

// BooleanLiteral is `TRUE` or `FALSE`.
type BooleanLiteral struct {
	Value bool
	Pos   Position
}

func (e *BooleanLiteral) expressionNode()    {}
func (e *BooleanLiteral) Position() Position { return e.Pos }

// NullLiteral is `NULL`. It carries no value beyond its position.
type NullLiteral struct {
	Pos Position
}

func (e *NullLiteral) expressionNode()    {}
func (e *NullLiteral) Position() Position { return e.Pos }

// Placeholder is the anonymous bind parameter `?`. The executor (M9)
// matches placeholders to caller-supplied args by occurrence order.
type Placeholder struct {
	Pos Position
}

func (e *Placeholder) expressionNode()    {}
func (e *Placeholder) Position() Position { return e.Pos }

// Identifier references a column (or, in some contexts, a table). The
// parser doesn't distinguish; the planner/executor resolves names.
type Identifier struct {
	Name string
	Pos  Position
}

func (e *Identifier) expressionNode()    {}
func (e *Identifier) Position() Position { return e.Pos }

// BinaryExpr is a binary infix expression. In v0.1 Op is always "=";
// the field is a string so v0.2+ can add "<", "<=", ">", ">=", "!="
// without changing the type.
type BinaryExpr struct {
	Op    string
	Left  Expression
	Right Expression
	Pos   Position
}

func (e *BinaryExpr) expressionNode()    {}
func (e *BinaryExpr) Position() Position { return e.Pos }

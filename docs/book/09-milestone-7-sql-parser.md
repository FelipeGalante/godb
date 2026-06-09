# Chapter 09 — The SQL Frontend (M7)

## Where we are

By the end of [Chapter 08](08-milestone-6-catalog.md) the engine could hold multiple named tables with persistent metadata, and you could create + look up + iterate them — but only by writing Go code that called `catalog.CreateTable(name, schema, sql)` and `btree.Tree.Insert(rowid, payload)` directly. The engine was complete enough to be a database internally; it was not yet a database *to a user*.

M7 fills that gap. We add a SQL **lexer** that turns the bytes `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL);` into a stream of tokens, and a recursive-descent **parser** that turns the token stream into a typed Abstract Syntax Tree:

```
CreateTableStatement{
  Name: "users",
  Columns: [
    ColumnDef{Name: "id",   Kind: INTEGER, PrimaryKey: true},
    ColumnDef{Name: "name", Kind: TEXT,    NotNull: true},
  ],
}
```

Nothing more. The parser produces an AST and returns. No tables get created, no rows get inserted. That's M9's job (the executor). M7's deliverable is "this string parses into this tree, and these strings don't."

This is the kind of milestone that sounds modest from outside and is satisfying from inside. The grammar is deliberately small ([ADR-0015](../adr/0015-sql-grammar-scope.md)), so the code is small, so the tests are sharp. By the end of M7 the engine can *read* SQL, which is the prerequisite for M8 (the public Go API) and M9 (actually running the SQL).

## Foundation

### Why a lexer and a parser are separate phases

A SQL string is a sequence of bytes. Some of those bytes are significant (`SELECT`, `*`, `users`) and some are not (whitespace, comments). The first step in turning bytes into meaning is to identify the **lexemes** — the smallest units of source text that carry information — and label them with their **token type**.

For example, `SELECT * FROM users` is six lexemes: `SELECT` (keyword), `*` (punctuation), `FROM` (keyword), `users` (identifier), plus the two whitespace runs that get discarded, plus the implicit end-of-input.

Tokenization is mechanical: scan one character at a time, build up runs of letters and digits, emit a token whenever you've collected something complete. It can be done with a state machine, a hand-written loop, or a generated DFA. GoDB uses a hand-written loop because the token set is tiny.

The **parser** then reads the token stream and builds the AST. It doesn't look at bytes; it looks at typed tokens. This separation is the standard "two-phase compiler" model. The benefits are real:

- The parser's logic is about grammar, not character classes. It doesn't have to know that `S` could begin `SELECT` or could begin `SAVEPOINT` — the lexer already decided which.
- Error messages get clearer because the lexer can say "I saw `'oops` at line 5, column 3 and couldn't find a closing quote" — the parser would never reach a token in that case.
- The lexer can be reused (e.g. for a future syntax-highlighting tool or interactive shell) without dragging the parser along.

### What recursive descent is and why we picked it

A **recursive-descent parser** is the simplest top-down parser shape: one function per grammar production, calling each other recursively. For GoDB's tiny grammar that looks like:

```
parseStatement()      → routes to one of:
parseCreateTable()    → "CREATE", "TABLE", identifier, "(", parseColumnDef(), ...
parseInsert()         → "INSERT", "INTO", identifier, optional "(", "VALUES", ...
parseSelect()         → "SELECT", parseSelectList(), "FROM", identifier, optional WHERE
parseColumnDef()      → identifier, type, optional constraints
parseExpression()     → literal | placeholder | identifier
parseWhere()          → identifier, "=", parseExpression()
```

Each function consumes tokens off the lexer, returns an AST node, and reports errors with the source position attached. The structure of the code mirrors the structure of the grammar. Reading the parser top to bottom is a guided tour of "what SQL we accept."

The alternative was a generated parser (`goyacc`, `participle`, or a PEG library). Those are great when the grammar is large or changes often. GoDB's grammar is neither. Hand-written code is easier to read, easier to walk through in this book, and adds no build-time dependency. [ADR-0015](../adr/0015-sql-grammar-scope.md) records the choice.

### Why our grammar is small (and how we reject the rest)

Look at the full SQLite SQL syntax sometime: hundreds of productions, dozens of clauses, every comparison operator and aggregate function and window-function variation. None of that is in GoDB v0.1. We accept:

- `CREATE TABLE name (col_def, ...)`
- `INSERT INTO name [(cols)] VALUES (...)`
- `SELECT (* | cols) FROM name [WHERE col = expr]`

That's it. Three statement kinds. Three column types (`INTEGER`, `TEXT`, `BOOLEAN`). Two constraints (`NOT NULL`, `PRIMARY KEY`). One comparison operator (`=`). Anonymous `?` placeholders. The rest of SQL is *outside* v0.1 and will arrive (some of it, deliberately, never) in later milestones.

Everything outside the grammar is **explicitly rejected** with `ErrUnsupportedSQL` and a clear message naming what we don't support yet. A query with `WHERE id = 1 AND name = 'x'` fails with:

```
sql: unsupported SQL feature at line 1, column 23: compound predicates with AND are not supported in GoDB v0.1
```

…not with the confusing alternative of `expected ';' got 'AND'`. The parser knows it saw `AND` and that `AND` isn't in scope; saying so is friendlier than pretending we don't recognize the construct.

This pattern — recognize, refuse, explain — is the contract every later milestone inherits. When v0.2 adds `AND`/`OR`, the `if AND-then-reject` branch is the line of code that changes. When v0.3 adds `JOIN`, same shape.

## Decisions

- **Grammar scope** is what's documented above and codified in [ADR-0015](../adr/0015-sql-grammar-scope.md). The scope is deliberately small.
- **Hand-written recursive descent**, not a generated parser. Also ADR-0015.
- **Keywords are case-insensitive** (`SELECT` == `select` == `Select`); **identifiers are case-sensitive** (`users` ≠ `Users`). Matches the SQL standard, not SQLite's full-fold. The lexer stores the original lexeme on every token, so error messages preserve the user's spelling.
- **Positions on every node.** Tokens carry 1-indexed `Line`/`Column`; statements and expressions carry the same. Error messages include source positions; future tooling (M10 CLI, IDEs) can use the positions for highlighting.
- **`?` placeholders are anonymous.** No numbered placeholders (`?1`, `$1`, `:name`). M9's executor will match `?` occurrences to argument positions by order.
- **`--` line comments.** No block comments (`/* */`) in v0.1.
- **No identifier quoting** (`"users"`, `` `users` ``). Reserved words can't appear as identifiers — the grammar is small enough that the conflict set is well within practical bounds.
- **`ErrUnsupportedSQL` vs `ErrSyntax`.** Two sentinels. `ErrUnsupportedSQL` for "this is recognizable SQL but we don't support it"; `ErrSyntax` for "we couldn't make sense of the bytes." Both wrap a `*SQLError` carrying a source position. Callers do `errors.Is(err, ErrUnsupportedSQL)` to dispatch.

## The code

Three files, ~1300 lines total including tests.

### [`internal/sql/lexer.go`](../../internal/sql/lexer.go)

The tokenizer. The interesting parts:

```go
type Lexer struct { src string; pos, line, col int; peeked *Token; peekErr error }

func NewLexer(src string) *Lexer
func (l *Lexer) Next() (Token, error)
func (l *Lexer) Peek() (Token, error)
```

`scan()` is the inner loop: skip whitespace and `-- comments`, then dispatch on the first significant character. Punctuation tokens (`(`, `)`, `,`, `;`, `*`, `=`, `?`) are one byte each. Numbers, strings, and identifiers run a sub-scanner. The keyword table maps lowercased keyword text to a `TokenType`; the original lexeme is preserved on the `Token` for error messages.

Two design choices visible in the code:

1. **`Peek` is implemented by stashing one token of lookahead.** The first `Peek` calls `scan` once and caches the result; subsequent `Peek`s return the cached token; `Next` after `Peek` returns and clears the cache. Simple and correct.
2. **String escape rules.** A `'` inside a string is encoded as `''`. The decoded `StrValue` has the escape un-applied; the `Lexeme` keeps the original including both quotes. No backslash escapes — keeping the grammar small.

### [`internal/sql/ast.go`](../../internal/sql/ast.go) and [`internal/sql/expressions.go`](../../internal/sql/expressions.go)

The AST node types. Three statement types (`CreateTableStatement`, `InsertStatement`, `SelectStatement`) and seven expression types (`IntegerLiteral`, `StringLiteral`, `BooleanLiteral`, `NullLiteral`, `Placeholder`, `Identifier`, `BinaryExpr`). Each implements the `Statement` or `Expression` interface and a `Position()` accessor.

The interesting helper:

```go
func ColumnDefsToSchema(defs []ColumnDef) record.Schema
```

This is the bridge between the parser and the catalog. A parsed `CREATE TABLE` has a slice of `ColumnDef`s (parser-shaped, with positions). The catalog wants a `record.Schema` (storage-shaped, no positions). `ColumnDefsToSchema` does the trivial mapping. M9's executor calls it when translating a parsed `CREATE TABLE` into a `catalog.CreateTable` call.

### [`internal/sql/parser.go`](../../internal/sql/parser.go)

The recursive-descent parser. The public surface:

```go
func Parse(src string) (Statement, error)
func ParseAll(src string) ([]Statement, error)
```

`Parse` returns a single statement and rejects trailing tokens. `ParseAll` accepts multiple statements separated by `;`. Both share the same internal `parser` struct (which holds the lexer and the current token) and dispatch logic.

The dispatch in `parseStatement` is straightforward:

```go
switch p.cur.Type {
case TokenKeywordCreate: return p.parseCreate()
case TokenKeywordInsert: return p.parseInsert()
case TokenKeywordSelect: return p.parseSelect()
case TokenIdentifier:
    // Common unsupported leading keywords arrive as identifiers
    // (UPDATE, DELETE, ALTER, DROP, REPLACE).
    switch strings.ToUpper(p.cur.Lexeme) {
    case "UPDATE": return nil, newUnsupportedError(..., "UPDATE is not supported in GoDB v0.1")
    // ...
    }
}
return nil, newSyntaxError(...)
```

`parseSelect` is the function that does the most work because `SELECT` has the most variants. It accepts the wildcard `*` or a column list, requires `FROM` + table name, optionally accepts a `WHERE` clause, and rejects every trailing clause we don't support (JOIN, GROUP BY, ORDER BY, LIMIT, HAVING, OFFSET) twice — once before WHERE and once after — because that's where each could legally appear in fuller SQL.

`parseWhere` is the smallest expression-shape function: it expects an identifier (the column), an `=`, and one more expression (the value). It also rejects `AND`/`OR`/`LIKE`/`IN`/`BETWEEN`/`IS` at this position with `ErrUnsupportedSQL`.

### [`internal/sql/errors.go`](../../internal/sql/errors.go)

The error types:

```go
var ErrSyntax = errors.New("sql: syntax error")
var ErrUnsupportedSQL = errors.New("sql: unsupported SQL feature")

type SQLError struct {
    Sentinel error  // one of the two above
    Message  string
    Pos      Position
}

func (e *SQLError) Error() string  // formats with line/column
func (e *SQLError) Unwrap() error  // returns Sentinel, so errors.Is works
```

Every error from the lexer or parser is a `*SQLError`. `errors.Is(err, ErrUnsupportedSQL)` works because `Unwrap` returns the sentinel.

## Tests as proof

The 50ish sql tests sit in [`internal/sql/lexer_test.go`](../../internal/sql/lexer_test.go), [`internal/sql/ast_test.go`](../../internal/sql/ast_test.go), and [`internal/sql/parser_test.go`](../../internal/sql/parser_test.go). A few worth pointing at specifically:

- **`TestParseCreateTable`** is the headline: a realistic 3-column CREATE TABLE round-trips into the AST with every field intact.
- **`TestParseCreateTableConstraintOrderIsFlexible`** pins the small but useful detail that `INTEGER PRIMARY KEY NOT NULL` and `INTEGER NOT NULL PRIMARY KEY` are both accepted.
- **`TestParseRejectsJoin` / `TestParseRejectsGroupBy` / …** prove the unsupported-feature contract — each construct produces `ErrUnsupportedSQL` (not a confusing syntax error) with a message naming the feature. The same family of tests covers UPDATE, DELETE, ALTER, DROP, AND/OR in WHERE, LIKE/IN, UNIQUE/CHECK/DEFAULT/REFERENCES constraints, REAL column type, CREATE INDEX, CREATE VIEW, subqueries.
- **`TestParseAllMultipleStatements`** confirms the multi-statement path: a 3-statement script parses into 3 statements.
- **`TestSQLErrorIncludesLineAndColumn`** is the proof that errors are useful — a multi-line input with an error on line 2 produces an error message mentioning line 2.
- **`TestColumnDefsToSchemaMapsCorrectly`** is the M7→M6 bridge test: the parser's `ColumnDef` slice round-trips into a `record.Schema` that `Schema.Validate` then accepts on a realistic row.

## What this layer cannot do yet

- **No execution.** `sql.Parse` returns a `Statement`; nothing in M7 looks up tables, opens trees, or returns rows. M9 closes that loop with the executor.
- **No public Go API.** `pkg/godb` is still empty. M8.
- **No planner.** M8 introduces the planner (AST → logical plan). M7 stops at AST.
- **No prepared statements / statement caching.** Each `Parse` call does the full work. v0.2 or later.
- **No `UPDATE`, `DELETE`, `ALTER TABLE`, `DROP TABLE`.** Recognized and rejected.
- **No `JOIN`, `GROUP BY`, `ORDER BY`, `LIMIT`, `HAVING`, `OFFSET`.** Recognized and rejected.
- **No compound `WHERE` predicates with `AND` / `OR`.** Recognized and rejected.
- **No comparison operators other than `=`.** v0.2.
- **No functions.** v0.3.
- **No subqueries.** Out of scope for GoDB.
- **No identifier quoting.** v0.2+ if it becomes useful.
- **No `DEFAULT`, `CHECK`, `UNIQUE`, `REFERENCES`** column constraints. v0.2+.
- **No type affinity / implicit conversions.** The grammar admits exactly three column types; the executor (M9) will reject mismatches.

## Further reading

- *Crafting Interpreters* by Robert Nystrom — the parser chapters (3–8) are an excellent recursive-descent tutorial, free online, in the same style as this book.
- Niklaus Wirth's *Algorithms + Data Structures = Programs* — the original recursive-descent reference, decades old, still readable.
- The SQLite [`lemon` parser](https://www.sqlite.org/lemon.html) — different approach (LALR generator). Useful as contrast: same goal, very different style.
- The Go standard library's [`go/parser`](https://pkg.go.dev/go/parser) and [`go/scanner`](https://pkg.go.dev/go/scanner) — both hand-written, both larger than GoDB's, but the layered shape (scanner + parser) is the same.

## Where the next chapter picks up

You can now read SQL but not do anything with it. M8 closes the gap on the *outside*: it adds the public Go API (`pkg/godb`) that wraps the storage stack + catalog + parser into the stable user-facing surface. `godb.Open(path)`, `db.Exec(ctx, sql, args...)`, `db.Query(ctx, sql, args...)`. M8 also introduces a tiny planner (AST → logical plan) because the public API needs something to execute against; M9 then deepens the executor with proper plan dispatch.

By the end of M8 a Go program can `import "github.com/felipegalante/godb/pkg/godb"` and run SQL — even if M9 is still where some plan branches actually return rows. By the end of M9 the loop closes fully.

That's where the next chapter picks up.

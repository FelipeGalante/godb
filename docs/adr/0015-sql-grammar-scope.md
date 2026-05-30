# ADR-0015: SQL grammar is deliberately small; parser is hand-written recursive descent

- Status: Accepted
- Date: 2026-05-30
- Tags: sql, parser, scope

## Context

Building a SQL parser is the kind of work that can grow without bound. Every comparison operator, every aggregate, every join type, every clause adds a grammar production, an AST node, tests, and documentation. Hobby database projects often spend their whole budget on the parser and ship without a storage engine.

GoDB starts from the opposite end. The storage stack (M1–M6) lands first; SQL arrives last among the "internal" layers. By the time M7 starts, every key design decision about what the engine *can* do has already been made — and that set is small ([ADR-0004 (no SQLite compatibility)](0004-no-sqlite-compatibility.md), [ADR-0005 (bottom-up build order)](0005-bottom-up-build-order.md), the [PRD](../prd.md) §4 v0.1 list). The parser exists to *expose* the engine, not to grow it.

The decision this ADR records: *exactly* what SQL syntax is in scope for v0.1, and *how* the parser is implemented.

## Decision

### Supported grammar

GoDB v0.1 parses the following SQL subset and **rejects everything else** with `ErrUnsupportedSQL`:

```
statement      := create_table | insert | select

create_table   := "CREATE" "TABLE" identifier "(" column_def {"," column_def} ")"
column_def     := identifier column_type {column_constraint}
column_type    := "INTEGER" | "TEXT" | "BOOLEAN"
column_constraint := "NOT" "NULL" | "PRIMARY" "KEY"

insert         := "INSERT" "INTO" identifier ["(" identifier {"," identifier} ")"]
                  "VALUES" "(" expression {"," expression} ")"

select         := "SELECT" select_list "FROM" identifier ["WHERE" where_expr]
select_list    := "*" | identifier {"," identifier}
where_expr     := identifier "=" expression
expression     := integer_literal | string_literal | boolean_literal
                | "NULL" | placeholder | identifier
```

Trailing semicolons are optional. `--` starts a line comment. Keywords are case-insensitive; identifiers are case-sensitive.

### Rejection style

Statements and clauses that fall *outside* this grammar but are clearly recognizable SQL (`JOIN`, `GROUP BY`, `UPDATE`, `DELETE`, `ALTER TABLE`, `CREATE INDEX`, `AND`/`OR` in `WHERE`, comparison operators other than `=`, etc.) are explicitly detected by the parser and rejected with `ErrUnsupportedSQL`. The error message names the feature. For example:

- `unsupported SQL feature at line 1, column 23: JOIN is not supported in GoDB v0.1`
- `unsupported SQL feature at line 1, column 19: UNIQUE constraint is not supported in GoDB v0.1`

This is intentional: a `WHERE id = 1 AND name = 'x'` query failing with "expected ';' got 'AND'" would confuse the reader into thinking the engine just has a parse bug. "Compound predicates with AND are not supported in GoDB v0.1" tells them which milestone to wait for.

### Parser implementation

The parser is **hand-written recursive descent**: one function per grammar production, one token of lookahead via the lexer's `Peek`/`Next` pair. No external parser-generator dependency. The code lives at [`internal/sql/parser.go`](../../internal/sql/parser.go).

## Consequences

**Enables.** The parser is easy to read top-to-bottom; each function maps to a grammar production. Adding a new feature is a localized change. The error messages can be specific because the parser knows exactly what construct it was looking at when something went wrong. No build-time dependency.

**Constrains.** The parser is not table-driven, so a future feature touching many productions (e.g. compound predicates with `AND`/`OR` everywhere expressions are allowed) is a multi-function change. The lexer and parser duplicate small amounts of "is this a keyword?" logic. Both costs are accepted for the simplicity of the resulting code.

**Reversibility.** Switching to a generated parser (`goyacc`, `participle`) later is possible but expensive — it would be a rewrite, not a refactor. The on-disk surface doesn't change, so no file-format compatibility risk.

The supported-grammar scope is also reversible in the obvious direction (we can always add features). It is *not* reversible in the removal direction once something ships, because removing a feature would break callers. v0.1 errs on the side of "ship less, add features deliberately."

## Alternatives considered

**`goyacc` (yacc-style LALR parser generator).** Standard tool. Auto-generates a table-driven parser from a grammar file. Trade-offs: faster to extend for grammar that changes often, harder to read top-to-bottom (the parser is tables + a generic driver), pulls a build-time dependency. Rejected: GoDB's grammar isn't expected to change often, the readability matters for the book chapter and ADR-style documentation, and "no external dependency" is a small but real plus.

**A PEG parser (e.g. `participle`).** Declarative, library-driven. Similar trade-offs to `goyacc` — easier extension, harder reading. Rejected for the same reasons.

**An expression-grammar (Pratt) parser.** Useful when the operator-precedence story is rich. GoDB v0.1 has *no* operator precedence (the only operator is `=`, always inside `WHERE`); a Pratt parser would be over-engineered.

**A "permissive" parser that accepts more than the executor supports.** Some engines parse a superset of what they can run and reject at planning/execution time. Rejected because it produces worse error UX — the parser is the natural place to say "this isn't supported," and saying it earlier (with a position) is friendlier.

## Related

- Code: [`internal/sql/parser.go`](../../internal/sql/parser.go), [`internal/sql/lexer.go`](../../internal/sql/lexer.go), [`internal/sql/ast.go`](../../internal/sql/ast.go), [`internal/sql/errors.go`](../../internal/sql/errors.go).
- Book: [Chapter 09 — The SQL Frontend (M7)](../book/09-milestone-7-sql-parser.md).
- See also: [ADR-0004 (no SQLite compatibility)](0004-no-sqlite-compatibility.md) — the lineage. M7 honors what ADR-0004 declared as scope discipline.
- See also: [ADR-0005 (bottom-up build order)](0005-bottom-up-build-order.md) — why SQL arrives this late.
- PRD §4 (functional requirements by version).

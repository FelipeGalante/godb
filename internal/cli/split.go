package cli

import "strings"

// splitStatements breaks SQL source into individual statement strings
// on top-level semicolons. It mirrors the lexer's rules for the two
// constructs that can legitimately contain a ';': single-quoted string
// literals (with the ” escape) and -- line comments. The trailing ';'
// is not included in each returned statement, and surrounding
// whitespace is trimmed. Statements that are empty after trimming
// (e.g. a stray ';' or a comment-only chunk) are dropped here; chunks
// that contain only comments still survive trimming and are skipped
// later by the executor, which sees an EOF as the first token.
func splitStatements(src string) []string {
	var stmts []string
	start := 0
	inString := false
	n := len(src)
	for i := 0; i < n; {
		c := src[i]
		if inString {
			if c == '\'' {
				if i+1 < n && src[i+1] == '\'' {
					i += 2
					continue
				}
				inString = false
			}
			i++
			continue
		}
		switch {
		case c == '\'':
			inString = true
			i++
		case c == '-' && i+1 < n && src[i+1] == '-':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == ';':
			if stmt := strings.TrimSpace(src[start:i]); stmt != "" {
				stmts = append(stmts, stmt)
			}
			i++
			start = i
		default:
			i++
		}
	}
	if tail := strings.TrimSpace(src[start:]); tail != "" {
		stmts = append(stmts, tail)
	}
	return stmts
}

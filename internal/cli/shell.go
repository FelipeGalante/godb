package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/felipegalante/godb/pkg/godb"
)

const shellHelp = `Commands (SQL statements end with ';'):
  .help            show this help
  .tables          list table names
  .schema [name]   show CREATE statements (all tables, or one)
  .mode table|csv  set result output format
  .dump            print SQL to recreate the database
  .exit / .quit    leave the shell
`

// runShell opens the database (creating it if missing) and runs an
// interactive read-eval-print loop. SQL statements may span multiple
// lines and are executed once terminated by ';'. Lines beginning with
// '.' (when no statement is pending) are meta-commands. Prompts and
// status go to stderr; result rows go to stdout.
func runShell(dbPath string, stdin io.Reader, stdout, stderr io.Writer, format outputFormat) error {
	db, err := openDB(dbPath, true)
	if err != nil {
		return err
	}
	defer db.Close()

	fmt.Fprintf(stderr, "%s\nConnected to %s — .help for commands, .exit to quit.\n", version, dbPath)

	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var buf strings.Builder
	prompt := func() {
		if strings.TrimSpace(buf.String()) == "" {
			fmt.Fprint(stderr, "godb> ")
		} else {
			fmt.Fprint(stderr, "  ...> ")
		}
	}

	prompt()
	for sc.Scan() {
		line := sc.Text()
		// Meta-command: only when no statement is mid-entry.
		if strings.TrimSpace(buf.String()) == "" && strings.HasPrefix(strings.TrimSpace(line), ".") {
			done, err := runMeta(db, strings.TrimSpace(line), &format, stdout, stderr)
			if err != nil {
				fmt.Fprintf(stderr, "godb: %v\n", err)
			}
			if done {
				return nil
			}
			prompt()
			continue
		}

		buf.WriteString(line)
		buf.WriteByte('\n')
		if idx := lastTopLevelSemicolon(buf.String()); idx >= 0 {
			full := buf.String()
			for _, stmt := range splitStatements(full[:idx]) {
				if err := runStatement(db, stmt, stdout, stderr, format); err != nil {
					fmt.Fprintf(stderr, "godb: %v\n", err)
				}
			}
			rest := full[idx:]
			buf.Reset()
			buf.WriteString(rest)
		}
		prompt()
	}
	if err := sc.Err(); err != nil {
		return err
	}
	// Run any trailing statement entered without a terminating ';'.
	if tail := strings.TrimSpace(buf.String()); tail != "" {
		if err := runStatement(db, tail, stdout, stderr, format); err != nil {
			fmt.Fprintf(stderr, "godb: %v\n", err)
		}
	}
	fmt.Fprintln(stderr)
	return nil
}

// runMeta handles a '.'-prefixed shell command. It returns done=true
// when the shell should exit (.exit/.quit).
func runMeta(db *godb.DB, line string, format *outputFormat, stdout, stderr io.Writer) (done bool, err error) {
	fields := strings.Fields(line)
	cmd := fields[0]
	args := fields[1:]
	switch cmd {
	case ".exit", ".quit":
		return true, nil
	case ".help":
		fmt.Fprint(stderr, shellHelp)
		return false, nil
	case ".tables":
		tables, err := db.Tables()
		if err != nil {
			return false, err
		}
		for _, t := range tables {
			fmt.Fprintln(stdout, t.Name)
		}
		return false, nil
	case ".schema":
		return false, metaSchema(db, args, stdout)
	case ".mode":
		if len(args) != 1 {
			return false, fmt.Errorf(".mode: want one of: table, csv")
		}
		f, err := parseFormat(args[0])
		if err != nil {
			return false, err
		}
		*format = f
		return false, nil
	case ".dump":
		return false, dumpAll(db, stdout)
	default:
		return false, fmt.Errorf("unknown meta-command %q (.help for the list)", cmd)
	}
}

func metaSchema(db *godb.DB, args []string, out io.Writer) error {
	tables, err := db.Tables()
	if err != nil {
		return err
	}
	var want string
	if len(args) == 1 {
		want = args[0]
	}
	found := false
	for _, t := range tables {
		if want != "" && t.Name != want {
			continue
		}
		found = true
		fmt.Fprintf(out, "%s;\n", strings.TrimRight(strings.TrimSpace(t.SQL), ";"))
	}
	if want != "" && !found {
		return fmt.Errorf(".schema: no such table: %s", want)
	}
	return nil
}

// lastTopLevelSemicolon returns the index just past the last top-level
// ';' in src (so src[:idx] holds complete statements and src[idx:] is
// the remainder), or -1 if there is no top-level ';'. It respects
// single-quoted strings (” escape) and -- line comments, matching
// splitStatements.
func lastTopLevelSemicolon(src string) int {
	last := -1
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
			i++
			last = i
		default:
			i++
		}
	}
	return last
}

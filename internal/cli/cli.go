// Package cli implements the godb command-line interface: an
// interactive shell plus exec/query/inspect/check/dump subcommands. It
// is a thin layer over pkg/godb for SQL and over the internal storage,
// btree, and catalog packages for the introspection commands.
//
// The package is stdlib-only (no third-party CLI framework) to match
// the project's zero-dependency stance, and all command handlers take
// injected io.Reader/io.Writer so they can be driven from tests.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

// usageError marks a command-line misuse (bad/missing arguments,
// unknown command) so Run can exit with code 2 rather than the
// code-1 used for runtime failures.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error {
	return &usageError{msg: fmt.Sprintf(format, a...)}
}

const version = "godb 0.1.0"

const usageText = `godb — a SQLite-inspired database engine

Usage:
  godb [-format table|csv] <db> [command] [args]

Commands:
  (none)                 open an interactive shell on <db>
  shell                  open an interactive shell on <db>
  exec <file.sql>        run the SQL statements in a file
  query "<sql>"          run a single SQL statement and print the result
  inspect header         dump the database file header
  inspect page <n>       dump one page's header
  inspect tree           walk every table's B+tree
  check                  validate the catalog and every table tree
  dump                   print SQL (CREATE TABLE + INSERTs) to stdout

Flags:
  -format table|csv      output format for query/dump rows (default table)
  -version               print version and exit
  -help                  print this help and exit

Examples:
  godb data.godb
  godb data.godb exec schema.sql
  godb data.godb query "SELECT * FROM users"
  godb -format csv data.godb query "SELECT * FROM users"
  godb data.godb inspect tree
`

// Run is the CLI entry point. It parses args (excluding the program
// name), dispatches to a command handler, and returns a process exit
// code: 0 on success, 1 on a runtime error, 2 on a usage error.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("godb", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usageText) }

	formatStr := fs.String("format", "table", "output format: table or csv")
	showVersion := fs.Bool("version", false, "print version and exit")

	if err := fs.Parse(args); err != nil {
		// flag already printed the error + usage.
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}

	format, err := parseFormat(*formatStr)
	if err != nil {
		fmt.Fprintf(stderr, "godb: %v\n", err)
		return 2
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return 2
	}

	dbPath := rest[0]
	var sub string
	var cmdArgs []string
	if len(rest) > 1 {
		sub = rest[1]
		cmdArgs = rest[2:]
	}

	if err := dispatch(dbPath, sub, cmdArgs, format, stdin, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "godb: %v\n", err)
		var ue *usageError
		if errors.As(err, &ue) {
			return 2
		}
		return 1
	}
	return 0
}

func dispatch(dbPath, sub string, cmdArgs []string, format outputFormat, stdin io.Reader, stdout, stderr io.Writer) error {
	switch sub {
	case "", "shell":
		return runShell(dbPath, stdin, stdout, stderr, format)
	case "exec":
		if len(cmdArgs) != 1 {
			return usagef("exec: expected a single <file.sql> argument")
		}
		return runExecFile(dbPath, cmdArgs[0], stdout, stderr, format)
	case "query":
		if len(cmdArgs) != 1 {
			return usagef("query: expected a single quoted \"<sql>\" argument")
		}
		return runQuery(dbPath, cmdArgs[0], stdout, stderr, format)
	case "inspect":
		return runInspect(dbPath, cmdArgs, stdout)
	case "check":
		return runCheck(dbPath, stdout)
	case "dump":
		return runDump(dbPath, stdout)
	default:
		return usagef("unknown command %q (run with -help)", sub)
	}
}

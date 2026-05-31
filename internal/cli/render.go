package cli

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/felipegalante/godb/pkg/godb"
)

// outputFormat selects how query/dump rows are rendered.
type outputFormat int

const (
	formatTable outputFormat = iota
	formatCSV
)

func parseFormat(s string) (outputFormat, error) {
	switch strings.ToLower(s) {
	case "table":
		return formatTable, nil
	case "csv":
		return formatCSV, nil
	default:
		return formatTable, fmt.Errorf("unknown -format %q (want table or csv)", s)
	}
}

// renderRows drains rows into out in the requested format and returns
// the number of data rows written. The caller owns reporting the count
// and closing rows.
func renderRows(out io.Writer, rows *godb.Rows, format outputFormat) (int, error) {
	cols := rows.Columns()
	var data [][]any
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return len(data), err
		}
		data = append(data, cells)
	}
	if err := rows.Err(); err != nil {
		return len(data), err
	}

	switch format {
	case formatCSV:
		return len(data), renderCSV(out, cols, data)
	default:
		return len(data), renderTable(out, cols, data)
	}
}

func renderTable(out io.Writer, cols []string, data [][]any) error {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = len(c)
	}
	strData := make([][]string, len(data))
	for r, row := range data {
		strRow := make([]string, len(cols))
		for i, v := range row {
			s := displayCell(v)
			strRow[i] = s
			if len(s) > widths[i] {
				widths[i] = len(s)
			}
		}
		strData[r] = strRow
	}

	pad := func(cells []string) string {
		parts := make([]string, len(cells))
		for i, s := range cells {
			parts[i] = fmt.Sprintf("%-*s", widths[i], s)
		}
		return strings.TrimRight(strings.Join(parts, " | "), " ")
	}

	if _, err := fmt.Fprintln(out, pad(cols)); err != nil {
		return err
	}
	seps := make([]string, len(cols))
	for i, w := range widths {
		seps[i] = strings.Repeat("-", w)
	}
	if _, err := fmt.Fprintln(out, strings.Join(seps, "-+-")); err != nil {
		return err
	}
	for _, row := range strData {
		if _, err := fmt.Fprintln(out, pad(row)); err != nil {
			return err
		}
	}
	return nil
}

func renderCSV(out io.Writer, cols []string, data [][]any) error {
	w := csv.NewWriter(out)
	if err := w.Write(cols); err != nil {
		return err
	}
	for _, row := range data {
		rec := make([]string, len(row))
		for i, v := range row {
			rec[i] = csvCell(v)
		}
		if err := w.Write(rec); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// displayCell formats a scanned value for the aligned text table.
func displayCell(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case int64:
		return strconv.FormatInt(x, 10)
	case bool:
		return strconv.FormatBool(x)
	case string:
		return x
	default:
		return fmt.Sprintf("%v", x)
	}
}

// csvCell formats a scanned value for CSV. NULL becomes an empty field
// (CSV has no NULL; this is documented in the CLI guide).
func csvCell(v any) string {
	if v == nil {
		return ""
	}
	return displayCell(v)
}

// sqlLiteral formats a scanned value as a SQL literal for dump output.
func sqlLiteral(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case int64:
		return strconv.FormatInt(x, 10)
	case bool:
		if x {
			return "TRUE"
		}
		return "FALSE"
	case string:
		return "'" + strings.ReplaceAll(x, "'", "''") + "'"
	default:
		return fmt.Sprintf("%v", x)
	}
}

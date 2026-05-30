package record

import "fmt"

// Column describes one column of a table. Position must match the
// column's index in Schema.Columns.
type Column struct {
	Name       string
	Kind       Kind
	NotNull    bool
	PrimaryKey bool
	Position   int
}

// Schema is an ordered set of columns. It is the contract a row of
// values must satisfy before encoding (or after decoding) at the
// catalog/executor layer.
type Schema struct {
	Columns []Column
}

// Validate checks that values satisfies the schema:
//   - len(values) == len(s.Columns)
//   - NULL only where the column is nullable
//   - non-NULL value kinds match the column's declared kind
//
// It does not check uniqueness, foreign keys, or other constraints
// beyond column-level type and nullability — those belong to higher
// layers when they exist.
func (s *Schema) Validate(values []Value) error {
	if len(values) != len(s.Columns) {
		return fmt.Errorf("%w: got %d values, schema has %d columns",
			ErrColumnCountMismatch, len(values), len(s.Columns))
	}
	for i, col := range s.Columns {
		v := values[i]
		if v.IsNull() {
			if col.NotNull {
				return fmt.Errorf("%w: column %q at position %d",
					ErrNullViolation, col.Name, i)
			}
			continue
		}
		if v.Kind != col.Kind {
			return fmt.Errorf("%w: column %q at position %d wants %s, got %s",
				ErrTypeMismatch, col.Name, i, col.Kind, v.Kind)
		}
	}
	return nil
}

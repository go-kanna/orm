package impl

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Changes holds the explicitly assigned columns of an update. Its zero value assigns nothing.
type Changes[M any] struct {
	values []assignment
}

type assignment struct {
	column string
	value  any
}

// Set returns a new change set with column assigned to value, including nil and zero values.
// Generated setters supply the column name and enforce its Go type.
func (c Changes[M]) Set(column string, value any) Changes[M] {
	c.values = slices.Clone(c.values)
	for i, a := range c.values {
		if a.column == column {
			c.values[i].value = value
			return c
		}
	}
	c.values = append(c.values, assignment{column, value})
	return c
}

// RenderUpdate renders assignments followed by conditions, rejecting an empty update or an unbounded write.
// allowed contains the mapped, writable columns supplied by the generated repository.
func (c Changes[M]) RenderUpdate(d Dialect, table string, allowed []string, q Of[M]) (string, []any, error) {
	if len(c.values) == 0 {
		return "", nil, errors.New("orm: Update requires at least one assigned column")
	}
	if err := q.ConditionsOnly("Update"); err != nil {
		return "", nil, err
	}
	sets := make([]string, 0, len(c.values))
	args := make([]any, 0, len(c.values))
	for _, a := range c.values {
		if !slices.Contains(allowed, a.column) {
			return "", nil, fmt.Errorf("orm: Update cannot assign column %s", a.column)
		}
		args = append(args, a.value)
		placeholder := "?"
		if d == Postgres {
			placeholder = fmt.Sprintf("$%d", len(args))
		}
		sets = append(sets, quoted(d, a.column)+" = "+placeholder)
	}
	rd, err := q.Render(d, len(args)+1, nil)
	if err != nil {
		return "", nil, err
	}
	if rd.Where() == "" {
		return "", nil, fmt.Errorf("orm: Update would update every row of %s: give it a condition", table)
	}
	text := "UPDATE " + quoted(d, table) + " SET " + strings.Join(sets, ", ") + rd.Where()
	return text, append(args, rd.Args()...), nil
}

// UpsertValues is what method, an upsert taking c, writes over the row it meets: for each of allowed, in its order, a
// flag telling whether c assigns the column, and the value it assigns, for a statement that writes each column the
// flag of which is true and keeps the others. A column of key, which the row is met by, or one not allowed, is an
// error.
func (c Changes[M]) UpsertValues(method string, allowed, key []string) ([]any, error) {
	for _, a := range c.values {
		switch {
		case slices.Contains(key, a.column):
			return nil, fmt.Errorf("orm: %s meets its row by %s, which its change set cannot write", method, a.column)
		case !slices.Contains(allowed, a.column):
			return nil, fmt.Errorf("orm: %s cannot assign column %s", method, a.column)
		}
	}
	values := make([]any, 0, 2*len(allowed))
	for _, column := range allowed {
		i := slices.IndexFunc(c.values, func(a assignment) bool { return a.column == column })
		if i < 0 {
			values = append(values, false, nil)
			continue
		}
		values = append(values, true, c.values[i].value)
	}
	return values, nil
}

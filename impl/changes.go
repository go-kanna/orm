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

// RenderUpdate renders assignments followed by conditions, rejecting an empty update, an unbounded write, or a scope
// holding more than conditions, as that of an Update writing the rows its keys name does.
// allowed contains the mapped, writable columns supplied by the generated repository.
func (c Changes[M]) RenderUpdate(d Dialect, table string, allowed []string, q Of[M]) (string, []any, error) {
	if err := q.ConditionsOnly("Update"); err != nil {
		return "", nil, err
	}
	return c.RenderUpdateSelected(d, table, allowed, nil, "", q)
}

// RenderUpdateSelected renders assignments to the rows q selects, as RenderDelete selects those it deletes, rejecting
// an empty update or an unbounded write. lock is the locking clause of the subquery that selects the rows by key,
// which the statement has whether or not q pages them, or nothing.
func (c Changes[M]) RenderUpdateSelected(d Dialect, table string, allowed, key []string, lock string, q Of[M]) (
	string, []any, error,
) {
	if len(c.values) == 0 {
		return "", nil, errors.New("orm: Update requires at least one assigned column")
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
	where, rest, err := q.targets(d, "Update", table, key, lock, len(args)+1)
	if err != nil {
		return "", nil, err
	}
	return "UPDATE " + quoted(d, table) + " SET " + strings.Join(sets, ", ") + where, append(args, rest...), nil
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

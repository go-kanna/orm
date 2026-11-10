package impl

import (
	"fmt"
	"slices"
	"strings"

	"github.com/go-kanna/orm"
)

// Changes holds the explicitly assigned columns of an update. Its zero value assigns nothing.
type Changes[M any] struct {
	values []assignment
}

type assignment struct {
	column string
	value  any
	// add tells that value is added to the stored value of column, rather than written over it.
	add bool
}

// Set returns a new change set with column assigned to value, including nil and zero values, in place of what it
// assigned column before. Generated setters supply the column name and enforce its Go type.
func (c Changes[M]) Set(column string, value any) Changes[M] {
	return c.with(assignment{column: column, value: value})
}

// Add returns a new change set adding n to the stored value of column, in place of what it assigned column before.
// Generated setters supply the column name, of a column of numbers that is never NULL, and enforce its Go type.
func (c Changes[M]) Add(column string, n any) Changes[M] {
	return c.with(assignment{column: column, value: n, add: true})
}

// Touch returns a new change set assigning value to column, the update time of a row, when c assigns any column, and c
// itself when it assigns none, so that a write of nothing still writes nothing.
func (c Changes[M]) Touch(column string, value any) Changes[M] {
	if len(c.values) == 0 {
		return c
	}
	return c.Set(column, value)
}

func (c Changes[M]) with(a assignment) Changes[M] {
	c.values = slices.Clone(c.values)
	if i := slices.IndexFunc(c.values, func(b assignment) bool { return b.column == a.column }); i >= 0 {
		c.values[i] = a
		return c
	}
	c.values = append(c.values, a)
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
		return "", nil, orm.ErrEmptyChangeSet
	}
	sets := make([]string, 0, len(c.values))
	args := make([]any, 0, len(c.values))
	for _, a := range c.values {
		if !slices.Contains(allowed, a.column) {
			return "", nil, fmt.Errorf("orm: Update cannot assign column %s", a.column)
		}
		args = append(args, a.value)
		value := "?"
		if d == Postgres {
			value = fmt.Sprintf("$%d", len(args))
		}
		if a.add {
			value = quoted(d, a.column) + " + " + value
		}
		sets = append(sets, quoted(d, a.column)+" = "+value)
	}
	where, rest, err := q.targets(d, "Update", table, key, lock, len(args)+1)
	if err != nil {
		return "", nil, err
	}
	return "UPDATE " + quoted(d, table) + " SET " + strings.Join(sets, ", ") + where, append(args, rest...), nil
}

// UpsertValues is what method, an upsert taking c, writes over the row it meets: for each of allowed, in its order, a
// flag telling whether c sets the column, and the value it sets, and then, for a column of added, a flag telling
// whether c adds to the column, and the value it adds; for a statement that writes each column the flag of which is
// true and keeps the others. A column of key, which the row is met by, one not allowed, or one not of added that c
// adds to, is an error.
func (c Changes[M]) UpsertValues(method string, allowed, added, key []string) ([]any, error) {
	for _, a := range c.values {
		switch {
		case slices.Contains(key, a.column):
			return nil, fmt.Errorf("orm: %s meets its row by %s, which its change set cannot write", method, a.column)
		case !slices.Contains(allowed, a.column):
			return nil, fmt.Errorf("orm: %s cannot assign column %s", method, a.column)
		case a.add && !slices.Contains(added, a.column):
			return nil, fmt.Errorf("orm: %s cannot add to column %s", method, a.column)
		}
	}
	values := make([]any, 0, 2*(len(allowed)+len(added)))
	for _, column := range allowed {
		set, add := []any{false, nil}, []any{false, nil}
		if i := slices.IndexFunc(c.values, func(a assignment) bool { return a.column == column }); i >= 0 {
			if c.values[i].add {
				add = []any{true, c.values[i].value}
			} else {
				set = []any{true, c.values[i].value}
			}
		}
		values = append(values, set...)
		if slices.Contains(added, column) {
			values = append(values, add...)
		}
	}
	return values, nil
}

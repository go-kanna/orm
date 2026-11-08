package impl

import (
	"fmt"
	"strings"
)

// RenderDelete selects the rows to delete using the scope's conditions and pagination, as a read of the scope would
// select them. key names a non-null unique key: an order is made to end in it, as a read's is in the primary key, and
// a page of rows is selected by it.
func (q Of[M]) RenderDelete(d Dialect, table string, key []string) (string, []any, error) {
	where, args, err := q.targets(d, "Delete", table, key, "", 1)
	if err != nil {
		return "", nil, err
	}
	return "DELETE FROM " + quoted(d, table) + where, args, nil
}

// targets is the WHERE clause of what, a statement writing the rows of table q selects, and the arguments of its
// placeholders, numbered from first. The rows are those a read of q selects: key names a non-null unique key, which an
// order is made to end in, and by which a subquery selects a page of rows, or rows it locks by lock, a locking clause.
func (q Of[M]) targets(d Dialect, what, table string, key []string, lock string, first int) (string, []any, error) {
	if len(q.preloads) > 0 {
		return "", nil, fmt.Errorf("orm: %s cannot load relations", what)
	}
	rd, err := q.Render(d, first, key)
	if err != nil {
		return "", nil, err
	}
	if rd.Where() == "" {
		return "", nil, fmt.Errorf("orm: %s would %s every row of %s: give it a condition", what,
			strings.ToLower(what), table)
	}
	paged := q.hasLimit || q.hasOffset
	if !paged && len(q.orders) > 0 && !q.seeking {
		return "", nil, fmt.Errorf("orm: %s ordering requires Limit or Offset", what)
	}
	if !paged && lock == "" {
		return rd.Where(), rd.Args(), nil
	}
	if len(key) == 0 {
		how := "paged"
		if !paged {
			how = "locked"
		}
		return "", nil, fmt.Errorf("orm: %s %s requires a primary or non-null unique key", how, what)
	}
	columns := make([]string, len(key))
	for i, name := range key {
		columns[i] = quoted(d, name)
	}
	names := strings.Join(columns, ", ")
	targets := "SELECT " + names + " FROM " + quoted(d, table) + rd.Where() + rd.OrderBy() + rd.Limit() + lock
	if d == MySQL {
		// LIMIT keeps the derived table materialized, and the outer IN subquery has no LIMIT of its own.
		targets = "SELECT " + names + " FROM (" + targets + ") AS " +
			quoted(d, "orm_"+strings.ToLower(what)+"_targets")
	}
	identity := names
	if len(key) > 1 {
		identity = "(" + identity + ")"
	}
	return " WHERE " + identity + " IN (" + targets + ")", rd.Args(), nil
}

package impl

import (
	"errors"
	"fmt"
	"strings"
)

// RenderDelete selects the rows to delete using the scope's conditions and pagination.
// key names a non-null unique key, needed only when a page of rows is selected.
func (q Of[M]) RenderDelete(d Dialect, table string, key []string) (string, []any, error) {
	if len(q.preloads) > 0 || len(q.projections) > 0 || len(q.batches) > 0 {
		return "", nil, errors.New("orm: Delete cannot load relations")
	}
	rd, err := q.Render(d, 1, nil)
	if err != nil {
		return "", nil, err
	}
	if rd.Where() == "" {
		return "", nil, fmt.Errorf("orm: Delete would delete every row of %s: give it a condition", table)
	}
	base := "DELETE FROM " + quoted(d, table)
	if !q.hasLimit && !q.hasOffset {
		if len(q.orders) > 0 && !q.seeking {
			return "", nil, errors.New("orm: Delete ordering requires Limit or Offset")
		}
		return base + rd.Where(), rd.Args(), nil
	}
	if len(key) == 0 {
		return "", nil, errors.New("orm: paged Delete requires a primary or non-null unique key")
	}
	if len(q.orders) == 0 && !q.seeking {
		return "", nil, errors.New("orm: paged Delete requires an order")
	}
	columns := make([]string, len(key))
	for i, name := range key {
		columns[i] = quoted(d, name)
	}
	names := strings.Join(columns, ", ")
	targets := "SELECT " + names + " FROM " + quoted(d, table) + rd.Where() + rd.OrderBy() + rd.Limit()
	if d == MySQL {
		// LIMIT keeps the derived table materialized, and the outer IN subquery has no LIMIT of its own.
		targets = "SELECT " + names + " FROM (" + targets + ") AS " + quoted(d, "orm_delete_targets")
	}
	identity := names
	if len(key) > 1 {
		identity = "(" + identity + ")"
	}
	return base + " WHERE " + identity + " IN (" + targets + ")", rd.Args(), nil
}

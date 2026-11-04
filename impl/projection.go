package impl

import (
	"fmt"
	"slices"
	"strings"
)

// RenderProjection renders plain, the read of the columns of a projection from its table, with q's conditions, order,
// and limit. key is as Render takes it.
func (q Of[M]) RenderProjection(d Dialect, plain string, key []string) (string, []any, bool, error) {
	if err := q.ProjectionOnly("projection"); err != nil {
		return "", nil, false, err
	}
	rd, err := q.Render(d, 1, key)
	if err != nil {
		return "", nil, false, err
	}
	return plain + rd.Where() + rd.OrderBy() + rd.Limit(), rd.Args(), rd.Reversed(), nil
}

// RenderJoinedProjection renders the read of a projection of the rows of table that q matches, joining other tables:
// selected, the SELECT of its columns; joins, the JOIN clauses; needed, the columns of table that the two read; and
// columns, all those of table. The rows q matches go in a derived table of the columns needed, so that its conditions
// read those of table alone, and its order, by columns of table, and its limit follow the joins. key is as Render
// takes it.
func (q Of[M]) RenderJoinedProjection(d Dialect, table, selected, joins string, needed, columns, key []string) (
	string, []any, bool, error,
) {
	if err := q.ProjectionOnly("projection"); err != nil {
		return "", nil, false, err
	}
	rd, err := q.Render(d, 1, key)
	if err != nil {
		return "", nil, false, err
	}
	needed = slices.Clone(needed)
	orders := q.orders
	switch {
	case q.seeking:
		orders = q.ordersWith(q.seek.key)
	case len(orders) > 0:
		orders = q.ordersWith(key)
	}
	ordering := make([]string, 0, len(orders))
	for _, order := range orders {
		name := strings.Trim(order.column, "`\"")
		if !slices.Contains(columns, name) {
			return "", nil, false, fmt.Errorf("orm: a projection joining tables is ordered by columns of %s alone, "+
				"not by %s", table, order.column)
		}
		if !slices.Contains(needed, name) {
			needed = append(needed, name)
		}
		part := qualified(d, table, name)
		if order.desc != rd.Reversed() {
			part += " DESC"
		}
		ordering = append(ordering, part)
	}
	from := quoted(d, table)
	if rd.Where() != "" {
		// The conditions read the columns of table, and the joins and the order follow.
		names := make([]string, len(needed))
		for i, name := range needed {
			names[i] = quoted(d, name)
		}
		from = "(SELECT " + strings.Join(names, ", ") + " FROM " + from + rd.Where() + ") AS " + quoted(d, table)
	}
	text := selected + " FROM " + from + joins
	if len(ordering) > 0 {
		text += " ORDER BY " + strings.Join(ordering, ", ")
	}
	return text + rd.Limit(), rd.Args(), rd.Reversed(), nil
}

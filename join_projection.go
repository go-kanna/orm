package orm

import (
	"fmt"
	"slices"
	"strings"

	"github.com/go-kanna/orm/join"
)

type tableJoin struct {
	table string
	on    []join.Condition
}

// JoinTable adds an INNER JOIN for a read projection. Without conditions it uses
// the unique direct foreign key; explicit conditions are checked against the snapshot.
func (q Of[M]) JoinTable(table string, on ...join.Condition) Of[M] {
	for _, join := range q.joins {
		if join.table == table {
			if !slices.Equal(join.on, on) {
				q.err = fmt.Errorf("orm: conflicting conditions for Join(%q)", table)
			}
			return q
		}
	}
	q.joins = append(slices.Clip(q.joins), tableJoin{table: table, on: slices.Clone(on)})
	return q
}

// SelectColumn describes a projection source for generated queries.
type SelectColumn struct {
	Table, Column string
	// Name is the unqualified name inferred from the projection field, if any.
	Name string
}

// TableJoin describes snapshot columns and an optional foreign key for generated queries.
type TableJoin struct {
	Table                  string
	ParentColumns, Columns []string
	Fields                 []string
}

// RenderProjection renders a projection and its explicit joins. plain is the generated
// SELECT used when no joins are requested, preserving ordinary scope rendering.
func (q Of[M]) RenderProjection(d Dialect, table string, columns []SelectColumn, joins []TableJoin,
	baseColumns []string, plain string,
) (string, []any, bool, error) {
	if err := q.ProjectionOnly("projection"); err != nil {
		return "", nil, false, err
	}
	active, err := q.projectionJoins(table, columns, joins, baseColumns)
	if err != nil {
		return "", nil, false, err
	}
	base := q
	base.joins = nil
	rd, err := base.Render(d, 1)
	if err != nil {
		return "", nil, false, err
	}
	if len(active) == 0 {
		return plain + rd.Where() + rd.OrderBy() + rd.Limit(), rd.Args(), rd.Reversed(), nil
	}
	names := make([]string, len(columns))
	for i, c := range columns {
		names[i] = qualified(d, c.Table, c.Column)
	}
	needed := []string{}
	need := func(name string) {
		if !slices.Contains(needed, name) {
			needed = append(needed, name)
		}
	}
	for _, c := range columns {
		if c.Table == table {
			need(c.Column)
		}
	}
	for _, join := range active {
		for _, name := range join.ParentColumns {
			need(name)
		}
	}
	orders := q.orders
	if q.seeking {
		orders = q.ordersWith(q.seek.key)
	}
	ordering := make([]string, 0, len(orders))
	for _, order := range orders {
		name := strings.Trim(order.column, "`\"")
		if !slices.Contains(baseColumns, name) {
			return "", nil, false, fmt.Errorf("orm: joined projection ordering must name a base column: %s", order.column)
		}
		need(name)
		part := qualified(d, table, name)
		if order.desc != rd.Reversed() {
			part += " DESC"
		}
		ordering = append(ordering, part)
	}
	from := quoted(d, table)
	if rd.Where() != "" {
		// Predicates keep their base-table namespace; pagination follows the joins.
		fields := make([]string, len(needed))
		for i, name := range needed {
			fields[i] = quoted(d, name)
		}
		from = "(SELECT " + strings.Join(fields, ", ") + " FROM " + from + rd.Where() + ") AS " + quoted(d, table)
	}
	var text strings.Builder
	text.WriteString("SELECT " + strings.Join(names, ", ") + " FROM " + from)
	for _, join := range active {
		if len(join.Columns) == 0 || len(join.Columns) != len(join.ParentColumns) {
			return "", nil, false, fmt.Errorf("orm: invalid foreign key for Join(%q)", join.Table)
		}
		on := make([]string, len(join.Columns))
		for i, name := range join.Columns {
			on[i] = qualified(d, table, join.ParentColumns[i]) + " = " + qualified(d, join.Table, name)
		}
		text.WriteString(" INNER JOIN " + quoted(d, join.Table) + " ON " + strings.Join(on, " AND "))
	}
	if len(ordering) > 0 {
		text.WriteString(" ORDER BY " + strings.Join(ordering, ", "))
	}
	text.WriteString(rd.Limit())
	return text.String(), rd.Args(), rd.Reversed(), nil
}

func (q Of[M]) projectionJoins(table string, columns []SelectColumn, joins []TableJoin,
	baseColumns []string,
) ([]TableJoin, error) {
	active := make([]TableJoin, 0, len(q.joins))
	for _, request := range q.joins {
		var matches []TableJoin
		for _, join := range joins {
			if join.Table == request.table {
				matches = append(matches, join)
			}
		}
		if request.table == table || len(matches) == 0 {
			return nil, fmt.Errorf("orm: Join(%q) requires a different table present in the snapshot", request.table)
		}
		if len(request.on) == 0 {
			if len(matches) != 1 || len(matches[0].Columns) == 0 {
				return nil, fmt.Errorf("orm: Join(%q) requires exactly one foreign key from %s or explicit On conditions",
					request.table, table)
			}
			active = append(active, matches[0])
			continue
		}
		join := TableJoin{Table: request.table, Fields: matches[0].Fields}
		for _, on := range request.on {
			if !slices.Contains(baseColumns, on.BaseColumn) || !slices.Contains(join.Fields, on.JoinedColumn) {
				return nil, fmt.Errorf("orm: Join(%q) On(%q, %q) must name snapshot columns of %s and %s",
					request.table, on.BaseColumn, on.JoinedColumn, table, request.table)
			}
			join.ParentColumns = append(join.ParentColumns, on.BaseColumn)
			join.Columns = append(join.Columns, on.JoinedColumn)
		}
		active = append(active, join)
	}
	for _, c := range columns {
		if c.Table != table && !slices.ContainsFunc(active, func(join TableJoin) bool { return join.Table == c.Table }) {
			return nil, fmt.Errorf("orm: projection column %s.%s requires Join(%q)", c.Table, c.Column, c.Table)
		}
		if c.Name == "" {
			continue
		}
		for _, join := range active {
			if join.Table != c.Table && slices.Contains(join.Fields, c.Name) {
				return nil, fmt.Errorf("orm: projection column %s is ambiguous; qualify its orm tag", c.Name)
			}
		}
	}
	return active, nil
}

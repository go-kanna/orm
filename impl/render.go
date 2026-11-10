package impl

import (
	"slices"
	"strconv"
	"strings"
)

// Rendered is a scope as SQL: the pieces a query is put together from, and the arguments of their placeholders. The
// clauses are parts of one string, written at once.
type Rendered struct {
	where, conds, orderBy, orders, limit string
	args                                 []any
	reversed                             bool
}

// Render writes q as SQL for d, numbering the placeholders of PostgreSQL from first, so that they can follow those of
// the query it goes into. key is the primary key of the table, which an order is made to end in, so that rows equal in
// the order come in one, the same on the first page as on those After continues to; it is nil for a table without one.
func (q Of[M]) Render(d Dialect, first int, key []string) (Rendered, error) {
	if q.err != nil {
		return Rendered{}, q.err
	}
	var r Rendered
	conds, orders := q.conds, q.orders
	if q.seeking {
		if len(orders) == 0 {
			return Rendered{}, errUnordered
		}
		orders = q.ordersWith(q.seek.key)
		c, err := q.seek.cond(orders, d)
		if err != nil {
			return Rendered{}, err
		}
		conds = append(slices.Clip(conds), c)
		if q.seek.backward {
			for i := range orders {
				orders[i].desc = !orders[i].desc
			}
			r.reversed = true
		}
		key = nil
	}

	size, args := renderedSize(conds, orders, key)
	if size == 0 && !q.hasLimit && !q.hasOffset {
		return r, nil
	}
	if args > 0 {
		r.args = make([]any, 0, args)
	}
	var (
		b      strings.Builder
		digits [20]byte
		n      = first
	)
	b.Grow(size + 64)
	placeholder := func(arg any) {
		r.args = append(r.args, arg)
		if d == MySQL {
			b.WriteByte('?')
			return
		}
		b.WriteByte('$')
		b.Write(strconv.AppendInt(digits[:0], int64(n), 10))
		n++
	}
	for i, c := range conds {
		if i == 0 {
			b.WriteString(" WHERE ")
		} else {
			b.WriteString(" AND ")
		}
		b.WriteByte('(')
		b.WriteString(c.pieces[0])
		for j, arg := range c.args {
			if l, ok := arg.(list); ok {
				if len(l.values) == 0 {
					b.WriteString(l.none)
				}
				for k, v := range l.values {
					if k > 0 {
						b.WriteString(", ")
					}
					placeholder(v)
				}
			} else {
				placeholder(arg)
			}
			b.WriteString(c.pieces[j+1])
		}
		b.WriteByte(')')
	}
	where := b.Len()
	if len(orders) > 0 {
		b.WriteString(" ORDER BY ")
		writeOrders(&b, orders, key)
	}
	order := b.Len()
	switch {
	case q.hasLimit:
		b.WriteString(" LIMIT ")
		b.Write(strconv.AppendInt(digits[:0], int64(q.limit), 10))
	case q.hasOffset && d == MySQL:
		b.WriteString(" LIMIT 18446744073709551615") // MySQL has no OFFSET without a LIMIT.
	}
	if q.hasOffset {
		b.WriteString(" OFFSET ")
		b.Write(strconv.AppendInt(digits[:0], int64(q.offset), 10))
	}

	s := b.String()
	r.where, r.orderBy, r.limit = s[:where], s[where:order], s[order:]
	if where > 0 {
		r.conds = s[len(" WHERE "):where]
	}
	if order > where {
		r.orders = s[where+len(" ORDER BY ") : order]
	}
	return r, nil
}

// renderedSize is about how long the SQL of conds and orders, ended in key, is, and how many arguments conds have.
func renderedSize(conds []cond, orders []order, key []string) (size, args int) {
	for _, c := range conds {
		size += len(" AND ()")
		for _, p := range c.pieces {
			size += len(p)
		}
		for _, a := range c.args {
			if l, ok := a.(list); ok {
				args += len(l.values)
				size += len(l.none) + 8*len(l.values)
				continue
			}
			args++
			size += 6
		}
	}
	if len(orders) > 0 {
		size += len(" ORDER BY ")
		for _, o := range orders {
			size += len(o.column) + len(", DESC")
		}
		for _, k := range key {
			size += len(k) + len(", DESC")
		}
	}
	return size, args
}

// writeOrders writes orders, then those of the columns of key it has none of, each the way the last of orders goes:
// orders ended in key, as ordersWith makes them.
func writeOrders(b *strings.Builder, orders []order, key []string) {
	desc := orders[len(orders)-1].desc
	for i, o := range orders {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(o.column)
		if o.desc {
			b.WriteString(" DESC")
		}
	}
	for _, k := range key {
		if slices.ContainsFunc(orders, func(o order) bool { return o.column == k }) {
			continue
		}
		b.WriteString(", ")
		b.WriteString(k)
		if desc {
			b.WriteString(" DESC")
		}
	}
}

// Where returns the conditions as a WHERE clause, or nothing when there are none.
func (r Rendered) Where() string { return r.where }

// And returns the conditions for a WHERE clause the query has of its own, or nothing when there are none.
func (r Rendered) And() string {
	if r.conds == "" {
		return ""
	}
	return " AND " + r.conds
}

// OrderBy returns the order as an ORDER BY clause, or nothing when there is none.
func (r Rendered) OrderBy() string { return r.orderBy }

// Limit returns the LIMIT and OFFSET clauses, or nothing when the scope keeps every row.
func (r Rendered) Limit() string { return r.limit }

// Args returns the arguments of the placeholders, in order.
func (r Rendered) Args() []any { return r.args }

// Reversed reports whether the rows come in the reverse of the scope's order, as they do for Before, for the caller to
// turn them around.
func (r Rendered) Reversed() bool { return r.reversed }

func qualified(d Dialect, alias, column string) string {
	return quoted(d, alias) + "." + quoted(d, column)
}

func quoted(d Dialect, name string) string {
	if d == MySQL {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return "\"" + strings.ReplaceAll(name, "\"", "\"\"") + "\""
}

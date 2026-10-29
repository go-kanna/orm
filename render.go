package orm

import (
	"strconv"
	"strings"
)

// Rendered is a scope as SQL: the pieces a query is put together from, and the arguments of their placeholders.
type Rendered struct {
	conds, orders, limit string
	args                 []any
}

// Render writes q as SQL for d, numbering the placeholders of PostgreSQL from first, so that they can follow those of
// the query it goes into.
func (q Of[M]) Render(d Dialect, first int) (Rendered, error) {
	if q.err != nil {
		return Rendered{}, q.err
	}

	var (
		r Rendered
		b strings.Builder
		n = first
	)
	placeholder := func(arg any) {
		r.args = append(r.args, arg)
		if d == MySQL {
			b.WriteByte('?')
			return
		}
		b.WriteString("$" + strconv.Itoa(n))
		n++
	}
	for i, c := range q.conds {
		if i > 0 {
			b.WriteString(" AND ")
		}
		b.WriteString("(" + c.pieces[0])
		for j, arg := range c.args {
			if l, ok := arg.(list); ok {
				if len(l) == 0 {
					b.WriteString("NULL")
				}
				for k, v := range l {
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
	r.conds = b.String()

	orders := make([]string, 0, len(q.orders))
	for _, o := range q.orders {
		if o.desc {
			orders = append(orders, o.column+" DESC")
		} else {
			orders = append(orders, o.column)
		}
	}
	r.orders = strings.Join(orders, ", ")

	switch {
	case q.hasLimit:
		r.limit = " LIMIT " + strconv.Itoa(q.limit)
	case q.hasOffset && d == MySQL:
		r.limit = " LIMIT 18446744073709551615" // MySQL has no OFFSET without a LIMIT.
	}
	if q.hasOffset {
		r.limit += " OFFSET " + strconv.Itoa(q.offset)
	}
	return r, nil
}

// Where returns the conditions as a WHERE clause, or nothing when there are none.
func (r Rendered) Where() string { return r.clause(" WHERE ") }

// And returns the conditions for a WHERE clause the query has of its own, or nothing when there are none.
func (r Rendered) And() string { return r.clause(" AND ") }

// OrderBy returns the order as an ORDER BY clause, or nothing when there is none.
func (r Rendered) OrderBy() string {
	if r.orders == "" {
		return ""
	}
	return " ORDER BY " + r.orders
}

// Limit returns the LIMIT and OFFSET clauses, or nothing when the scope keeps every row.
func (r Rendered) Limit() string { return r.limit }

// Args returns the arguments of the placeholders, in order.
func (r Rendered) Args() []any { return r.args }

func (r Rendered) clause(keyword string) string {
	if r.conds == "" {
		return ""
	}
	return keyword + r.conds
}

// Package orm is what the code kanna-orm generates runs on: the scope a query is narrowed, ordered, and paged by,
// and the SQL it becomes. Code written by hand needs ErrNotFound alone.
package orm

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
)

// ErrNotFound is what a repository returns, wrapped, for a row that is not there.
var ErrNotFound = errors.New("orm: not found")

// Dialect is the database a scope is rendered for.
type Dialect int

const (
	Postgres Dialect = iota
	MySQL
)

// Of is the scope of a query reading Ms: its conditions, the order of its rows, and how many of them it keeps. Every
// method returns a new scope and leaves its receiver as it was, so a scope built halfway can be shared.
type Of[M any] struct {
	conds               []cond
	orders              []order
	limit, offset       int
	hasLimit, hasOffset bool
	preloads            []preload
	err                 error
}

// cond is a condition: pieces of SQL, with an argument between each two.
type cond struct {
	pieces []string
	args   []any
}

type order struct {
	column string
	desc   bool
}

// Where narrows to the rows a condition holds for. The condition is SQL with its arguments in between, as in
// Where("status <> ", v) or Where("date_trunc('month', placed_at) = date_trunc('month', ", at, ")").
func (q Of[M]) Where(parts ...any) Of[M] {
	if len(parts) == 0 {
		panic("orm: Where without a condition")
	}
	var c cond
	for i, p := range parts {
		if i%2 == 1 {
			c.args = append(c.args, p)
			continue
		}
		s, ok := p.(string)
		if !ok {
			panic(fmt.Sprintf("orm: Where wants SQL as its part %d, not a %T", i, p))
		}
		c.pieces = append(c.pieces, s)
	}
	if len(c.pieces) == len(c.args) {
		c.pieces = append(c.pieces, "")
	}
	q.conds = append(slices.Clip(q.conds), c)
	return q
}

// OrderBy orders the rows by column, after any order given before.
func (q Of[M]) OrderBy(column string, desc bool) Of[M] {
	q.orders = append(slices.Clip(q.orders), order{column, desc})
	return q
}

// Limit keeps the first n rows.
func (q Of[M]) Limit(n int) Of[M] {
	if n < 0 {
		q.err = cmp.Or(q.err, fmt.Errorf("orm: a limit of %d rows", n))
	}
	q.limit, q.hasLimit = n, true
	return q
}

// Offset skips the first n rows.
func (q Of[M]) Offset(n int) Of[M] {
	if n < 0 {
		q.err = cmp.Or(q.err, fmt.Errorf("orm: an offset of %d rows", n))
	}
	q.offset, q.hasOffset = n, true
	return q
}

// Or narrows to the rows any of alternatives matches, each holding conditions alone. Without alternatives, it
// matches no row.
func (q Of[M]) Or(alternatives ...Of[M]) Of[M] {
	for _, a := range alternatives {
		if err := a.conditionsOnly("Or"); err != nil {
			q.err = cmp.Or(q.err, err)
			return q
		}
		if len(a.conds) == 0 {
			return q // This alternative matches every row, and so does Or.
		}
	}
	if len(alternatives) == 0 {
		return q.Where("1 = 0")
	}
	parts := make([]cond, 0, len(alternatives))
	for _, a := range alternatives {
		parts = append(parts, joinConds(a.conds, " AND "))
	}
	q.conds = append(slices.Clip(q.conds), joinConds(parts, " OR "))
	return q
}

// Not narrows to the rows s, which holds conditions alone, does not match.
func (q Of[M]) Not(s Of[M]) Of[M] {
	if err := s.conditionsOnly("Not"); err != nil {
		q.err = cmp.Or(q.err, err)
		return q
	}
	if len(s.conds) == 0 {
		return q.Where("1 = 0")
	}
	c := cond{pieces: []string{"NOT ("}}
	c.append(joinConds(s.conds, " AND "))
	c.text(")")
	q.conds = append(slices.Clip(q.conds), c)
	return q
}

// Join makes one scope of qs, as if each were chained onto the one before: their conditions and orders add up, and a
// later limit or offset replaces an earlier one.
func Join[M any](qs ...Of[M]) Of[M] {
	var q Of[M]
	for _, s := range qs {
		q.conds = slices.Concat(q.conds, s.conds)
		q.orders = slices.Concat(q.orders, s.orders)
		if s.hasLimit {
			q.limit, q.hasLimit = s.limit, true
		}
		if s.hasOffset {
			q.offset, q.hasOffset = s.offset, true
		}
		for _, p := range s.preloads {
			q.preloads = addPreload(q.preloads, p)
		}
		q.err = cmp.Or(q.err, s.err)
	}
	return q
}

// conditionsOnly reports why q, given to what, holds more than conditions.
func (q Of[M]) conditionsOnly(what string) error {
	switch {
	case q.err != nil:
		return q.err
	case len(q.orders) > 0:
		return fmt.Errorf("orm: %s takes conditions alone, but its scope orders the rows", what)
	case q.hasLimit || q.hasOffset:
		return fmt.Errorf("orm: %s takes conditions alone, but its scope keeps only some of the rows", what)
	case len(q.preloads) > 0:
		return fmt.Errorf("orm: %s takes conditions alone, but its scope loads %s", what, q.preloads[0].relation)
	}
	return nil
}

// joinConds makes one condition of cs, sep between them, each in parentheses when there is more than one.
func joinConds(cs []cond, sep string) cond {
	out := cond{pieces: []string{""}}
	for i, c := range cs {
		if i > 0 {
			out.text(sep)
		}
		if len(cs) > 1 {
			out.text("(")
		}
		out.append(c)
		if len(cs) > 1 {
			out.text(")")
		}
	}
	return out
}

func (c *cond) text(s string) {
	c.pieces[len(c.pieces)-1] += s
}

func (c *cond) append(d cond) {
	c.text(d.pieces[0])
	for i, a := range d.args {
		c.args = append(c.args, a)
		c.pieces = append(c.pieces, d.pieces[i+1])
	}
}

// List is an argument standing for each of vs in turn, so that a condition can say IN (...) of it: it renders as a
// placeholder for each, and as NULL, which matches nothing, when there are none.
func List[T any](vs []T) any {
	l := make(list, len(vs))
	for i, v := range vs {
		l[i] = v
	}
	return l
}

type list []any

// Package impl is what the code kanna-orm generates runs on: the scope a query is narrowed, ordered, and paged by,
// and the SQL it becomes. Only generated code imports it. Its API changes with the generator, which ships in the
// same module.
package impl

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

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
	seek                seek
	seeking             bool
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
// Where("status <> ", v) or Where("date_trunc('month', placed_at) = date_trunc('month', ", at, "::timestamptz)").
func (q Of[M]) Where(parts ...any) Of[M] {
	if len(parts) == 0 {
		panic("orm: Where without a condition")
	}
	c := cond{pieces: make([]string, 0, len(parts)/2+1)}
	if len(parts) > 1 {
		c.args = make([]any, 0, len(parts)/2)
	}
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

// Fail makes err the error of the scope, which every query of it returns, unless it holds one already.
func (q Of[M]) Fail(err error) Of[M] {
	q.err = cmp.Or(q.err, err)
	return q
}

// Or narrows to the rows any of alternatives matches, each holding conditions alone. Without alternatives, it
// matches no row.
func (q Of[M]) Or(alternatives ...Of[M]) Of[M] {
	for _, a := range alternatives {
		if err := a.ConditionsOnly("Or"); err != nil {
			q.err = cmp.Or(q.err, err)
			return q
		}
	}
	if len(alternatives) == 0 {
		return q.Where("1 = 0")
	}
	parts := make([]cond, 0, len(alternatives))
	for _, a := range alternatives {
		if len(a.conds) == 0 {
			return q // This alternative matches every row, and so does Or.
		}
		parts = append(parts, joinConds(a.conds, " AND "))
	}
	q.conds = append(slices.Clip(q.conds), joinConds(parts, " OR "))
	return q
}

// Not narrows to the rows s, which holds conditions alone, does not match. A condition on a NULL is unknown, not
// false, so its row is in neither s nor Not(s).
func (q Of[M]) Not(s Of[M]) Of[M] {
	if err := s.ConditionsOnly("Not"); err != nil {
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

// Combine makes one scope of qs, as if each were chained onto the one before: their conditions and orders add up, a
// later limit or offset replaces an earlier one, and a row to continue from in more than one is an error.
func Combine[M any](qs ...Of[M]) Of[M] {
	if len(qs) == 1 {
		return qs[0]
	}
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
		if s.seeking {
			q = q.from(s.seek)
		}
		q.err = cmp.Or(q.err, s.err)
	}
	return q
}

// ConditionsOnly reports why q, given to what, holds more than conditions: an order, a limit, an offset, a row to
// continue from, or a relation to load, which what would otherwise pass over.
// A repository's Count and Exists read the conditions alone.
func (q Of[M]) ConditionsOnly(what string) error {
	switch {
	case q.err != nil:
		return q.err
	case len(q.orders) > 0:
		return fmt.Errorf("orm: %s takes conditions alone, but its scope orders the rows", what)
	case q.hasLimit || q.hasOffset || q.seeking:
		return fmt.Errorf("orm: %s takes conditions alone, but its scope keeps only some of the rows", what)
	case len(q.preloads) > 0:
		return fmt.Errorf("orm: %s takes conditions alone, but its scope loads %s", what, q.preloads[0].relation)
	}
	return nil
}

// Unpaged reports why q, given to what, a read of one row by its key, keeps only some of the rows: a limit, an
// offset, or a row to continue from, which could leave out the very row the key names.
func (q Of[M]) Unpaged(what string) error {
	if q.hasLimit || q.hasOffset || q.seeking {
		return fmt.Errorf("orm: %s reads one row by its key, but its scope keeps only some of the rows", what)
	}
	return nil
}

// Forward reports why q, given to what, a read of the first row in its order, goes the other way, as Before does: the
// row it would read is the one right before a row, not the first of the page before it.
func (q Of[M]) Forward(what string) error {
	if q.seeking && q.seek.backward {
		return fmt.Errorf("orm: %s reads the first row in its scope's order, but its scope goes the other way, "+
			"to the rows before a row", what)
	}
	return nil
}

// DefaultOrder orders q by key, the primary key of its table, when q has no order of its own, so that the first of
// its rows is one row and not any. A nil key leaves q as it is, and so does a row to continue from, which needs an
// order of the scope's own.
func (q Of[M]) DefaultOrder(key []string) Of[M] {
	if len(q.orders) > 0 || q.seeking {
		return q
	}
	for _, k := range key {
		q = q.OrderBy(k, false)
	}
	return q
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
func List[T any](vs []T) any { return newList(vs, "NULL") }

// InList is List as IN (...) takes it, right between the parentheses: with no values, it renders as a subquery of no
// rows, so that IN of it matches no row, and NOT IN of it every row, as of the empty set.
func InList[T any](vs []T) any { return newList(vs, "SELECT NULL FROM DUAL WHERE FALSE") }

// list is the values of a List or an InList, and what it renders as when there are none.
type list struct {
	values []any
	none   string
}

func newList[T any](vs []T, none string) list {
	l := list{values: make([]any, len(vs)), none: none}
	for i, v := range vs {
		l.values[i] = v
	}
	return l
}

// Expand writes a statement of MySQL from pieces, the SQL around its parameters, and args, one for each: a ? for an
// argument, and a ? for each of the values of a List or an InList, or what it renders as when it has none, with the
// arguments of the statement in their order.
func Expand(pieces []string, args ...any) (string, []any) {
	var (
		b   strings.Builder
		out = make([]any, 0, len(args))
	)
	b.WriteString(pieces[0])
	for i, arg := range args {
		l, ok := arg.(list)
		switch {
		case !ok:
			b.WriteByte('?')
			out = append(out, arg)
		case len(l.values) == 0:
			b.WriteString(l.none)
		default:
			b.WriteString(strings.Repeat("?, ", len(l.values)-1) + "?")
			out = append(out, l.values...)
		}
		b.WriteString(pieces[i+1])
	}
	return b.String(), out
}

// ProjectionOnly rejects relation loading in a projection. Filters, ordering, and pagination remain valid.
func (q Of[M]) ProjectionOnly(what string) error {
	if q.err != nil {
		return q.err
	}
	if len(q.preloads) > 0 {
		return fmt.Errorf("orm: %s is a projection, but its scope loads relations", what)
	}
	return nil
}

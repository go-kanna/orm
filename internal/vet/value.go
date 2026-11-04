package vet

import (
	"fmt"
	"go/constant"
	"go/token"
	"slices"
	"strings"
)

// tri tells on which of the paths to a point something holds.
type tri uint8

const (
	never tri = iota // on none of them
	some             // on some of them, or vet cannot tell
	every            // on all of them
)

// join is the tri of something holding as a on some paths and as b on the others.
func (a tri) join(b tri) tri {
	if a == b {
		return a
	}
	return some
}

// or is the tri of a or b holding, on the same paths.
func (a tri) or(b tri) tri {
	switch {
	case a == every || b == every:
		return every
	case a == never && b == never:
		return never
	}
	return some
}

// cause is the call that made a scope what it is: of a method kanna-orm generated, or of the runtime, from code it did
// not generate.
type cause struct {
	pos    token.Pos
	in     string // the function the call is in
	method string // the method called
}

// first is whichever of a and b comes first in the source, so that what vet reports does not depend on the order it
// reads the paths in.
func first(a, b cause) cause {
	if a.pos == token.NoPos || (b.pos != token.NoPos && b.pos < a.pos) {
		return b
	}
	return a
}

// prop is something a scope may hold, such as an order, and the call it comes from when it holds on every path.
type prop struct {
	t  tri
	at cause
}

func (p prop) join(q prop) prop {
	return p.with(p.t.join(q.t), q)
}

func (p prop) or(q prop) prop {
	return p.with(p.t.or(q.t), q)
}

func (p prop) with(t tri, q prop) prop {
	if t != every {
		return prop{t: t}
	}
	switch {
	case p.t == every && q.t == every:
		return prop{every, first(p.at, q.at)}
	case p.t == every:
		return p
	}
	return q
}

func set(c cause) prop { return prop{every, c} }

// value is what vet knows of an SSA value. A nil value is one no path has reached yet.
type value interface{ key(b *strings.Builder) }

// unknown is a value vet does not follow, which may be anything.
type unknown struct{}

func (unknown) key(b *strings.Builder) { b.WriteString("?") }

// constVal is a constant, such as the name of a column.
type constVal struct{ v constant.Value }

func (c constVal) key(b *strings.Builder) { b.WriteString(c.v.ExactString()) }

// listVal is a slice whose elements are known one by one.
type listVal struct{ elems []value }

func (l listVal) key(b *strings.Builder) {
	b.WriteByte('[')
	for _, e := range l.elems {
		e.key(b)
		b.WriteByte(',')
	}
	b.WriteByte(']')
}

// tuple is the results of a call returning more than one.
type tuple struct{ elems []value }

func (t tuple) key(b *strings.Builder) {
	b.WriteByte('(')
	for _, e := range t.elems {
		keyOf(b, e)
		b.WriteByte(',')
	}
	b.WriteByte(')')
}

// failed is the error a check of the runtime returns when it fails on every path.
type failed struct {
	msg string
	at  cause
	// hint is what the second line of the report says of at.
	hint string
}

func (f failed) key(b *strings.Builder) { fmt.Fprintf(b, "!%q", f.msg) }

func keyOf(b *strings.Builder, v value) {
	if v == nil {
		b.WriteString("_")
		return
	}
	v.key(b)
}

func fingerprint(vs ...value) string {
	var b strings.Builder
	for _, v := range vs {
		keyOf(&b, v)
		b.WriteByte(';')
	}
	return b.String()
}

// join is the value of something that is a on some paths and b on the others.
func join(a, b value) value {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	switch a := a.(type) {
	case *scopeVal:
		if b, ok := b.(*scopeVal); ok {
			return a.join(b)
		}
	case *changeVal:
		if b, ok := b.(*changeVal); ok {
			return a.join(b)
		}
	case constVal:
		if b, ok := b.(constVal); ok && constant.Compare(a.v, token.EQL, b.v) {
			return a
		}
	case listVal:
		if b, ok := b.(listVal); ok && len(a.elems) == len(b.elems) {
			elems := make([]value, len(a.elems))
			for i := range elems {
				elems[i] = join(a.elems[i], b.elems[i])
			}
			return listVal{elems}
		}
	case tuple:
		if b, ok := b.(tuple); ok && len(a.elems) == len(b.elems) {
			elems := make([]value, len(a.elems))
			for i := range elems {
				elems[i] = join(a.elems[i], b.elems[i])
			}
			return tuple{elems}
		}
	}
	return unknown{}
}

// scopeVal is a scope: what it holds on the paths to a point.
type scopeVal struct {
	conds, orders, limit, offset, seek prop
	preloads                           []preload
	// open tells that the scope may load relations besides those of preloads, as one vet does not follow may.
	open bool
	err  errProp
}

// preload is a relation a scope loads, and the scope its rows are read by.
type preload struct {
	relation string
	each     bool
	p        prop
	sub      *scopeVal
}

// errProp is the error a scope holds, which every query of it returns: msg is the error, if it is the same on every
// path it holds on.
type errProp struct {
	t   tri
	msg string
	at  cause
}

func emptyScope() *scopeVal { return &scopeVal{} }

func unknownScope() *scopeVal {
	s := prop{t: some}
	return &scopeVal{conds: s, orders: s, limit: s, offset: s, seek: s, open: true, err: errProp{t: some}}
}

func (s *scopeVal) clone() *scopeVal {
	c := *s
	c.preloads = slices.Clone(s.preloads)
	return &c
}

// key writes s, with the causes it holds, as two scopes vet reads alike but reports apart differ in those.
func (s *scopeVal) key(b *strings.Builder) {
	b.WriteString("S{")
	for _, p := range []prop{s.conds, s.orders, s.limit, s.offset, s.seek} {
		p.key(b)
	}
	for _, p := range s.preloads {
		fmt.Fprintf(b, " %s/%t/", p.relation, p.each)
		p.p.key(b)
		p.sub.key(b)
	}
	fmt.Fprintf(b, " %t %d %q@%d}", s.open, s.err.t, s.err.msg, s.err.at.pos)
}

func (p prop) key(b *strings.Builder) {
	if p.t == every {
		fmt.Fprintf(b, "2@%d,", p.at.pos)
		return
	}
	fmt.Fprintf(b, "%d,", p.t)
}

// paged tells on which paths s keeps only some of the rows, and by what when it does on every path.
func (s *scopeVal) paged() prop {
	return s.limit.or(s.offset).or(s.seek)
}

// loads tells whether s loads a relation, and which on every path, if any.
func (s *scopeVal) loads() (tri, *preload) {
	t := never
	if s.open {
		t = some
	}
	for i, p := range s.preloads {
		if p.p.t == every {
			return every, &s.preloads[i]
		}
		t = t.or(p.p.t)
	}
	return t, nil
}

func (s *scopeVal) join(o *scopeVal) *scopeVal {
	r := &scopeVal{
		conds: s.conds.join(o.conds), orders: s.orders.join(o.orders), limit: s.limit.join(o.limit),
		offset: s.offset.join(o.offset), seek: s.seek.join(o.seek), open: s.open || o.open,
		err: s.err.join(o.err),
	}
	r.preloads = mergePreloads(s.preloads, o.preloads, func(p prop) prop { return p.join(prop{}) },
		func(a, b preload) preload {
			a.p = a.p.join(b.p)
			a.sub = a.sub.join(b.sub)
			a.each = a.each && b.each
			return a
		})
	return r
}

// then is s with what o holds added to it, as chaining o onto s does.
func (s *scopeVal) then(o *scopeVal) *scopeVal {
	r := &scopeVal{
		conds: s.conds.or(o.conds), orders: s.orders.or(o.orders), limit: s.limit.or(o.limit),
		offset: s.offset.or(o.offset), seek: s.seek.or(o.seek), open: s.open || o.open, err: s.err.then(o.err),
	}
	r.preloads = mergePreloads(s.preloads, o.preloads, func(p prop) prop { return p },
		func(a, b preload) preload {
			a.p = a.p.or(b.p)
			a.sub = a.sub.then(b.sub)
			a.each = a.each && b.each
			return a
		})
	return r
}

// mergePreloads merges ps and qs by relation: by both for one of each, and by alone for one of either.
func mergePreloads(ps, qs []preload, alone func(prop) prop, both func(a, b preload) preload) []preload {
	out := slices.Clone(ps)
	for i := range out {
		if !slices.ContainsFunc(qs, func(q preload) bool { return q.relation == out[i].relation }) {
			out[i].p = alone(out[i].p)
		}
	}
	for _, q := range qs {
		i := slices.IndexFunc(out, func(p preload) bool { return p.relation == q.relation })
		if i >= 0 {
			out[i] = both(out[i], q)
			continue
		}
		q.p = alone(q.p)
		out = append(out, q)
	}
	slices.SortFunc(out, func(a, b preload) int { return strings.Compare(a.relation, b.relation) })
	return out
}

func (e errProp) join(o errProp) errProp {
	t := e.t.join(o.t)
	switch {
	case t == never:
		return errProp{}
	case e.t == never:
		return errProp{t, o.msg, o.at}
	case o.t == never:
		return errProp{t, e.msg, e.at}
	case e.msg == o.msg:
		return errProp{t, e.msg, first(e.at, o.at)}
	}
	return errProp{t: t}
}

// then is the error of a scope holding e, given o added to it: the first one, as cmp.Or keeps it. Where e holds on
// some paths alone, and o on every one, o is the error of every path, if not the first of each.
func (e errProp) then(o errProp) errProp {
	switch {
	case e.t == every, o.t == never:
		return e
	case e.t == never, o.t == every:
		return o
	}
	r := errProp{t: e.t.or(o.t)}
	if e.msg == o.msg {
		r.msg, r.at = e.msg, e.at
	}
	return r
}

func setErr(msg string, c cause) errProp { return errProp{every, msg, c} }

// changeVal is a change set: the columns it assigns on the paths to a point.
type changeVal struct {
	columns []assigned
	// open tells that it may assign columns besides those of columns, as one vet does not follow may.
	open bool
}

type assigned struct {
	column string
	p      prop
}

func (c *changeVal) key(b *strings.Builder) {
	b.WriteString("C{")
	for _, a := range c.columns {
		b.WriteString(a.column + ":")
		a.p.key(b)
	}
	fmt.Fprintf(b, "%t}", c.open)
}

func (c *changeVal) join(o *changeVal) *changeVal {
	r := &changeVal{open: c.open || o.open}
	for _, a := range c.columns {
		p := a.p.join(prop{})
		if i := slices.IndexFunc(o.columns, func(b assigned) bool { return b.column == a.column }); i >= 0 {
			p = a.p.join(o.columns[i].p)
		}
		r.columns = append(r.columns, assigned{a.column, p})
	}
	for _, b := range o.columns {
		if !slices.ContainsFunc(c.columns, func(a assigned) bool { return a.column == b.column }) {
			r.columns = append(r.columns, assigned{b.column, b.p.join(prop{})})
		}
	}
	slices.SortFunc(r.columns, func(a, b assigned) int { return strings.Compare(a.column, b.column) })
	return r
}

// assign is c with column assigned on every path.
func (c *changeVal) assign(column string, at cause) *changeVal {
	r := &changeVal{columns: slices.Clone(c.columns), open: c.open}
	if i := slices.IndexFunc(r.columns, func(a assigned) bool { return a.column == column }); i >= 0 {
		r.columns[i].p = r.columns[i].p.or(set(at))
		return r
	}
	r.columns = append(r.columns, assigned{column, set(at)})
	slices.SortFunc(r.columns, func(a, b assigned) int { return strings.Compare(a.column, b.column) })
	return r
}

// empty tells on which paths c assigns no column.
func (c *changeVal) empty() tri {
	if c.open {
		return some
	}
	t := every
	for _, a := range c.columns {
		if a.p.t == every {
			return never
		}
		if a.p.t == some {
			t = some
		}
	}
	return t
}

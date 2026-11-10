package vet

import (
	"fmt"
	"go/constant"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/ssa"
)

const (
	implPath  = "github.com/go-kanna/orm/impl"
	scopePath = "github.com/go-kanna/orm/scope"
)

// isScope reports whether t is a scope: the runtime's, the scope package's, or a scope type, whose one field is a
// scope.Of.
func isScope(t types.Type) bool {
	return isNamedIn(t, implPath, "Of") || isNamedIn(t, scopePath, "Of") || isNamedIn(t, scopePath, "Raw") ||
		wrapsWith(t, func(f types.Type) bool { return isNamedIn(f, scopePath, "Of") })
}

// isChange reports whether t is a change set: the runtime's, which change.Set names, or one of a scope type.
func isChange(t types.Type) bool {
	return isNamedIn(t, implPath, "Changes") ||
		wrapsWith(t, func(f types.Type) bool { return isNamedIn(f, implPath, "Changes") })
}

// wraps reports whether t is a struct of one field holding a scope or a change set, which vet takes for that field.
func wraps(t types.Type) bool {
	return wrapsWith(t, func(f types.Type) bool { return isScope(f) || isChange(f) })
}

func wrapsWith(t types.Type, field func(types.Type) bool) bool {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return false
	}
	s, ok := n.Underlying().(*types.Struct)
	return ok && s.NumFields() == 1 && field(s.Field(0).Type())
}

func isNamedIn(t types.Type, path, name string) bool {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return false
	}
	obj := n.Origin().Obj()
	return obj.Pkg() != nil && obj.Pkg().Path() == path && obj.Name() == name
}

// opCall is a call of the runtime: its arguments, the receiver first, and the cause of what it adds to a scope.
type opCall struct {
	f    *frame
	args []value
	at   cause
}

func (c opCall) scope(i int) *scopeVal {
	if i < len(c.args) {
		if s, ok := c.args[i].(*scopeVal); ok {
			return s
		}
	}
	return unknownScope()
}

// change is the change set of argument 0, the receiver of a method of Changes.
func (c opCall) change() *changeVal {
	if len(c.args) > 0 {
		if s, ok := c.args[0].(*changeVal); ok {
			return s
		}
	}
	return &changeVal{open: true}
}

func (c opCall) str(i int) (string, bool) {
	if i < len(c.args) {
		if k, ok := c.args[i].(constVal); ok && k.v.Kind() == constant.String {
			return constant.StringVal(k.v), true
		}
	}
	return "", false
}

func (c opCall) int(i int) (int64, bool) {
	if i < len(c.args) {
		if k, ok := c.args[i].(constVal); ok && k.v.Kind() == constant.Int {
			return constant.Int64Val(k.v)
		}
	}
	return 0, false
}

// strs is the strings of the argument i, a slice of them, if vet knows each.
func (c opCall) strs(i int) ([]string, bool) {
	if i >= len(c.args) {
		return nil, false
	}
	l, ok := c.args[i].(listVal)
	if !ok {
		return nil, false
	}
	out := make([]string, len(l.elems))
	for j, e := range l.elems {
		k, ok := e.(constVal)
		if !ok || k.v.Kind() != constant.String {
			return nil, false
		}
		out[j] = constant.StringVal(k.v)
	}
	return out, true
}

// scopes is the scopes of the argument i, a slice of them, if vet knows each.
func (c opCall) scopes(i int) ([]*scopeVal, bool) {
	if i >= len(c.args) {
		return nil, false
	}
	l, ok := c.args[i].(listVal)
	if !ok {
		return nil, false
	}
	out := make([]*scopeVal, len(l.elems))
	for j, e := range l.elems {
		s, ok := e.(*scopeVal)
		if !ok {
			s = unknownScope()
		}
		out[j] = s
	}
	return out, true
}

type op func(c opCall) (value, *failed)

// ops are the functions and methods of the runtime vet knows: what they make of a scope, and the checks they make.
var ops = map[string]op{
	"impl.Of.Where":                     adds(func(s *scopeVal) *prop { return &s.conds }),
	"impl.Of.OrderBy":                   adds(func(s *scopeVal) *prop { return &s.orders }),
	"impl.Of.Limit":                     pages(func(s *scopeVal) *prop { return &s.limit }, "a limit of %d rows"),
	"impl.Of.Offset":                    pages(func(s *scopeVal) *prop { return &s.offset }, "an offset of %d rows"),
	"impl.Of.After":                     adds(func(s *scopeVal) *prop { return &s.seek }),
	"impl.Of.Before":                    adds(func(s *scopeVal) *prop { return &s.seek }),
	"impl.Of.DefaultOrder":              opDefaultOrder,
	"impl.Of.Or":                        opOr,
	"impl.Of.Not":                       opNot,
	"impl.Combine":                      opCombine,
	"impl.Preload":                      func(c opCall) (value, *failed) { return c.preload(false), nil },
	"impl.PreloadEach":                  func(c opCall) (value, *failed) { return c.preload(true), nil },
	"impl.Of.ConditionsOnly":            checks(conditionsOnly),
	"impl.Of.Unpaged":                   checks(unpaged),
	"impl.Of.ProjectionOnly":            checks(projectionOnly),
	"impl.Of.Render":                    renders(2, func(c opCall) *failed { return scopeErr(c.scope(0)) }),
	"impl.Of.RenderProjection":          renders(4, func(c opCall) *failed { return scopeErr(c.scope(0)) }),
	"impl.Of.RenderJoinedProjection":    renders(4, func(c opCall) *failed { return scopeErr(c.scope(0)) }),
	"impl.Of.RenderDelete":              renders(3, renderDelete),
	"impl.Changes.Set":                  opSet,
	"impl.Changes.Add":                  opSet,
	"impl.Changes.Touch":                opTouch,
	"impl.Changes.RenderUpdate":         renders(3, renderUpdate),
	"impl.Changes.RenderUpdateSelected": renders(3, renderUpdateSelected),
	"impl.Changes.UpsertValues":         renders(2, upsertValues),
	"scope.Of.Raw":                      func(c opCall) (value, *failed) { return c.scope(0), nil },
	"scope.Raw.Where":                   adds(func(s *scopeVal) *prop { return &s.conds }),
	"scope.Raw.OrderBy":                 adds(func(s *scopeVal) *prop { return &s.orders }),
}

// opOf is what vet knows of fn, a function or method of the runtime, if anything.
func opOf(fn *ssa.Function) op {
	if o := fn.Origin(); o != nil {
		fn = o
	}
	obj, ok := fn.Object().(*types.Func)
	if !ok || obj.Pkg() == nil {
		return nil
	}
	var pkg string
	switch obj.Pkg().Path() {
	case implPath:
		pkg = "impl"
	case scopePath:
		pkg = "scope"
	default:
		return nil
	}
	name := pkg + "." + obj.Name()
	if recv := obj.Signature().Recv(); recv != nil {
		n, ok := types.Unalias(deref(recv.Type())).(*types.Named)
		if !ok {
			return nil
		}
		name = pkg + "." + n.Origin().Obj().Name() + "." + obj.Name()
	}
	return ops[name]
}

// adds is a method of the runtime holding on every path, besides what its receiver does, what field names.
func adds(field func(s *scopeVal) *prop) op {
	return func(c opCall) (value, *failed) {
		s := c.scope(0).clone()
		p := field(s)
		*p = p.or(set(c.at))
		return s, nil
	}
}

// pages is adds of Limit or Offset, which a negative number makes an error of.
func pages(field func(s *scopeVal) *prop, format string) op {
	add := adds(field)
	return func(c opCall) (value, *failed) {
		v, _ := add(c)
		s := v.(*scopeVal) //nolint:forcetypeassert // adds returns a scope
		if n, ok := c.int(1); ok && n < 0 {
			s.err = s.err.then(setErr(fmt.Sprintf(format, n), c.at))
		}
		return s, nil
	}
}

// checks is a check of the runtime, returning its error, of the scope it is a method of, given what it is checked
// for.
func checks(check func(s *scopeVal, what string) *failed) op {
	return func(c opCall) (value, *failed) {
		f := check(c.scope(0), c.what(1))
		if f != nil {
			return *f, f
		}
		return unknown{}, nil
	}
}

// renders is a method of the runtime returning n results, the last its error, which check tells of.
func renders(n int, check func(c opCall) *failed) op {
	return func(c opCall) (value, *failed) {
		f := check(c)
		elems := make([]value, n)
		for i := range elems {
			elems[i] = unknown{}
		}
		if f != nil {
			elems[n-1] = *f
		}
		return tuple{elems}, f
	}
}

func (c opCall) what(i int) string {
	s, _ := c.str(i)
	return s
}

func opDefaultOrder(c opCall) (value, *failed) {
	s := c.scope(0).clone()
	key, ok := c.strs(1)
	switch {
	case ok && len(key) == 0, s.orders.t == every, s.seek.t == every:
	case ok && s.orders.t == never && s.seek.t == never:
		s.orders = set(c.at)
	default:
		s.orders = s.orders.or(prop{t: some})
	}
	return s, nil
}

// opOr reads Or: each alternative holds conditions alone, and Or adds a condition unless one of them holds none, as
// it then matches every row.
func opOr(c opCall) (value, *failed) {
	s := c.scope(0).clone()
	alts, ok := c.scopes(1)
	if !ok {
		s.conds = s.conds.or(prop{t: some})
		s.err = s.err.then(errProp{t: some})
		return s, nil
	}
	for _, a := range alts {
		if f := conditionsOnly(a, "Or"); f != nil {
			s.err = s.err.then(setErr(f.msg, c.at))
			return s, nil
		}
	}
	added := prop{t: every, at: c.at}
	for _, a := range alts {
		s.err = s.err.then(errProp{t: worst(a)})
		if a.conds.t == never {
			return s, nil
		}
		if a.conds.t == some {
			added = prop{t: some}
		}
	}
	s.conds = s.conds.or(added)
	return s, nil
}

// worst tells on which paths a, given to Or or Not, makes it fail.
func worst(a *scopeVal) tri {
	t := a.err.t.or(a.orders.t).or(a.paged().t)
	lt, _ := a.loads()
	return t.or(lt)
}

func opNot(c opCall) (value, *failed) {
	s := c.scope(0).clone()
	other := c.scope(1)
	if f := conditionsOnly(other, "Not"); f != nil {
		s.err = s.err.then(setErr(f.msg, c.at))
		return s, nil
	}
	s.err = s.err.then(errProp{t: worst(other)})
	s.conds = s.conds.or(set(c.at))
	return s, nil
}

func opCombine(c opCall) (value, *failed) {
	qs, ok := c.scopes(0)
	if !ok {
		return unknownScope(), nil
	}
	s := emptyScope()
	for _, q := range qs {
		s = s.then(q)
	}
	return s, nil
}

// preload is the scope of Preload or PreloadEach, which loads the relation of its argument 1 by the scope of its
// argument 2: a page of the rows of each row for PreloadEach, of all of them for Preload, which is an error.
func (c opCall) preload(each bool) *scopeVal {
	s := c.scope(0).clone()
	relation, ok := c.str(1)
	sub := c.scope(2)
	if !ok {
		s.open = true
		return s
	}
	switch {
	case !each && sub.paged().t == every:
		s.err = s.err.then(setErr(
			relation+" keeps only some of the rows it loads, which would count those of every row together", c.at))
	case each && sub.seek.t == every:
		s.err = s.err.then(setErr(
			relation+" continues a page of the rows of each row by Offset, not by a row", c.at))
	}
	s.err = s.err.then(sub.err)
	return s.then(&scopeVal{preloads: []preload{{relation: relation, each: each, p: set(c.at), sub: sub}}})
}

// opSet is Set or Add, either of which assigns the column of argument 1.
func opSet(c opCall) (value, *failed) {
	ch := c.change()
	column, ok := c.str(1)
	if !ok {
		r := *ch
		r.open = true
		return &r, nil
	}
	return ch.assign(column, c.at), nil
}

// opTouch is Touch of the change set of argument 0, as vet sees it: the change set itself. The update time it assigns,
// when the change set assigns any column, is one the method may write and none of the key it meets its row by.
func opTouch(c opCall) (value, *failed) { return c.change(), nil }

func fail(msg string, at cause, hint string) *failed {
	return &failed{msg: msg, at: at, hint: hint}
}

// scopeErr is the error s holds on every path, the same on each.
func scopeErr(s *scopeVal) *failed {
	if s.err.t == every && s.err.msg != "" {
		return fail(s.err.msg, s.err.at, "makes the error")
	}
	return nil
}

// conditionsOnly is ConditionsOnly: what it returns on every path, the first check of it that holds on every path
// telling.
func conditionsOnly(s *scopeVal, what string) *failed {
	if f := scopeErr(s); f != nil {
		return f
	}
	if s.orders.t == every {
		return fail(what+" takes conditions alone, but its scope orders the rows", s.orders.at, "orders them")
	}
	if p := s.paged(); p.t == every {
		return fail(what+" takes conditions alone, but its scope keeps only some of the rows", p.at,
			"keeps only some of them")
	}
	if t, p := s.loads(); t == every {
		return fail(what+" takes conditions alone, but its scope loads "+p.relation, p.p.at, "loads "+p.relation)
	}
	return nil
}

func unpaged(s *scopeVal, what string) *failed {
	if p := s.paged(); p.t == every {
		return fail(what+" reads one row by its key, but its scope keeps only some of the rows", p.at,
			"keeps only some of them")
	}
	return nil
}

func projectionOnly(s *scopeVal, what string) *failed {
	if f := scopeErr(s); f != nil {
		return f
	}
	if t, p := s.loads(); t == every {
		return fail(what+" is a projection, but its scope loads relations", p.p.at, "loads "+p.relation)
	}
	return nil
}

// renderDelete is RenderDelete(d, table, key) of the scope of argument 0.
func renderDelete(c opCall) *failed {
	key, ok := c.strs(3)
	return targets(c, c.scope(0), "Delete", key, ok)
}

// renderUpdate is RenderUpdate(d, table, allowed, q) of the change set of argument 0: RenderUpdateSelected of no key,
// of a scope holding conditions alone.
func renderUpdate(c opCall) *failed {
	s := c.scope(4)
	if f := conditionsOnly(s, "Update"); f != nil {
		return f
	}
	return updateSelected(c, s, nil, true)
}

// renderUpdateSelected is RenderUpdateSelected(d, table, allowed, key, lock, q) of the change set of argument 0.
func renderUpdateSelected(c opCall) *failed {
	key, ok := c.strs(4)
	return updateSelected(c, c.scope(6), key, ok)
}

// updateSelected is the check of an Update of the change set of argument 0, which may assign the columns of argument 3,
// to the rows s selects by key, which vet knows if known says so.
func updateSelected(c opCall, s *scopeVal, key []string, known bool) *failed {
	ch := c.change()
	if ch.empty() == every {
		return fail("Update of an empty change set returns orm.ErrEmptyChangeSet", cause{}, "")
	}
	if allowed, ok := c.strs(3); ok {
		if a := firstAssigned(ch, func(column string) bool { return !slices.Contains(allowed, column) }); a != nil {
			return fail("Update cannot assign column "+a.column, a.p.at, "assigns "+a.column)
		}
	}
	return targets(c, s, "Update", key, known)
}

// targets is the check of s, the scope selecting the rows what, a Delete or an Update, writes of the table of argument
// 2, by key, which vet knows if known says so. A row to continue from is a condition, as Render writes one of it.
func targets(c opCall, s *scopeVal, what string, key []string, known bool) *failed {
	if t, p := s.loads(); t == every {
		return fail(what+" cannot load relations", p.p.at, "loads "+p.relation)
	}
	if f := scopeErr(s); f != nil {
		return f
	}
	if table, ok := c.str(2); ok && s.conds.t == never && s.seek.t == never {
		return fail(what+" would "+strings.ToLower(what)+" every row of "+table+": give it a condition", cause{}, "")
	}
	if s.limit.t == never && s.offset.t == never && s.orders.t == every && s.seek.t == never {
		return fail(what+" ordering requires Limit or Offset", s.orders.at, "orders them")
	}
	if p := s.limit.or(s.offset); p.t == every && known && len(key) == 0 {
		return fail("paged "+what+" requires a primary or non-null unique key", p.at, "keeps only some of them")
	}
	return nil
}

// upsertValues is UpsertValues(method, allowed, added, key) of the change set of argument 0.
func upsertValues(c opCall) *failed {
	ch := c.change()
	method, _ := c.str(1)
	allowed, okAllowed := c.strs(2)
	key, okKey := c.strs(4)
	if !okAllowed || !okKey {
		return nil
	}
	a := firstAssigned(ch, func(column string) bool {
		return slices.Contains(key, column) || !slices.Contains(allowed, column)
	})
	switch {
	case a == nil:
		return nil
	case slices.Contains(key, a.column):
		return fail(fmt.Sprintf("%s meets its row by %s, which its change set cannot write", method, a.column), a.p.at,
			"assigns "+a.column)
	}
	return fail(fmt.Sprintf("%s cannot assign column %s", method, a.column), a.p.at, "assigns "+a.column)
}

// firstAssigned is the column of ch, assigned on every path, that bad holds of, first in the source.
func firstAssigned(ch *changeVal, bad func(column string) bool) *assigned {
	var out *assigned
	for i, a := range ch.columns {
		if a.p.t != every || !bad(a.column) {
			continue
		}
		if out == nil || a.p.at.pos < out.p.at.pos {
			out = &ch.columns[i]
		}
	}
	return out
}

// describe is the second line of a report, telling where at made a scope what the report says: hint, as "orders
// them", is what it did, and in the function the report is of, which at need not name.
func describe(at cause, hint, in string) string {
	if at.in == in {
		return at.method + " " + hint
	}
	return strings.Join([]string{at.in, hint, "by", at.method}, " ")
}

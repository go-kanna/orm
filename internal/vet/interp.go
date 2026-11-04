package vet

import (
	"cmp"
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"maps"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ssa"
)

const (
	// maxDepth is how deep calls are followed; a call deeper than that returns what vet does not know.
	maxDepth = 24
	// maxPasses is how many times a function is read over before its loops are taken to settle; one that has not by
	// then returns what vet does not know.
	maxPasses = 12
)

// result is what a call of a function returns, and the first check of the runtime its body makes on every path it
// returns on that fails, if any.
type result struct {
	val  value
	fail *failed
	busy bool
}

// cell is a variable vet follows the value of: an allocation, or an element of an array allocated, by its index.
type cell struct {
	alloc *ssa.Alloc
	index int
}

type memory map[cell]value

// frame reads one call of a function, with the values of its parameters and free variables.
type frame struct {
	a    *analysis
	fn   *ssa.Function
	args []value
	env  []value
	// site is, for a function kanna-orm generated, the call from code it did not generate that led to it: what that
	// call adds to a scope, it adds there.
	site  cause
	root  bool
	depth int

	regs    map[ssa.Value]value
	out     map[*ssa.BasicBlock]memory
	changed bool

	ret     value
	fail    *failed
	reports map[ssa.CallInstruction]*failed
	envs    map[*ssa.Function][]value
}

func (a *analysis) frame(fn *ssa.Function, args, env []value, site cause, depth int) *frame {
	return &frame{a: a, fn: fn, args: args, env: env, site: site, depth: depth}
}

// interpret reads a call of fn with args.
func (a *analysis) interpret(fn *ssa.Function, args []value, site cause, depth int) *result {
	if depth > maxDepth || len(fn.Blocks) == 0 {
		return &result{val: unknownResult(fn)}
	}
	key := fn.String() + "|" + fingerprint(args...) + "|" + siteKey(site)
	if r, ok := a.memo[key]; ok {
		if r.busy {
			return &result{val: unknownResult(fn)}
		}
		return r
	}
	r := &result{busy: true}
	a.memo[key] = r
	f := a.frame(fn, args, nil, site, depth)
	if f.run() {
		r.val, r.fail = f.ret, f.fail
	} else {
		r.val = unknownResult(fn)
	}
	r.busy = false
	return r
}

func siteKey(c cause) string {
	if c.pos == token.NoPos {
		return ""
	}
	return c.in + "." + c.method + "@" + strconv.Itoa(int(c.pos))
}

func unknownResult(fn *ssa.Function) value {
	n := fn.Signature.Results().Len()
	if n <= 1 {
		return unknown{}
	}
	elems := make([]value, n)
	for i := range elems {
		elems[i] = unknown{}
	}
	return tuple{elems}
}

// run reads f over until it settles, and reports whether it did.
func (f *frame) run() bool {
	blocks := f.fn.DomPreorder()
	f.regs = map[ssa.Value]value{}
	f.out = map[*ssa.BasicBlock]memory{}
	for range maxPasses {
		f.changed = false
		f.ret, f.fail = nil, nil
		f.reports = map[ssa.CallInstruction]*failed{}
		f.envs = map[*ssa.Function][]value{}
		for _, b := range blocks {
			f.block(b)
		}
		if !f.changed {
			return true
		}
	}
	return false
}

func (f *frame) block(b *ssa.BasicBlock) {
	mem := f.entry(b)
	for _, instr := range b.Instrs {
		f.instr(instr, mem)
	}
	if old, ok := f.out[b]; !ok || memoryKey(old) != memoryKey(mem) {
		f.changed = true
	}
	f.out[b] = mem
}

func memoryKey(m memory) string {
	cells := slices.SortedFunc(maps.Keys(m), func(a, b cell) int {
		if a.alloc != b.alloc {
			return cmp.Or(cmp.Compare(a.alloc.Pos(), b.alloc.Pos()), strings.Compare(a.alloc.Name(), b.alloc.Name()))
		}
		return cmp.Compare(a.index, b.index)
	})
	var b strings.Builder
	for _, k := range cells {
		fmt.Fprintf(&b, "%s[%d]", k.alloc.Name(), k.index)
		keyOf(&b, m[k])
	}
	return b.String()
}

// entry is what the cells hold as b starts: what the blocks reached so far leave them, a cell one of those leaves
// alone holding what vet does not know.
func (f *frame) entry(b *ssa.BasicBlock) memory {
	var mem memory
	for _, p := range b.Preds {
		out, ok := f.out[p]
		if !ok {
			continue
		}
		if mem == nil {
			mem = maps.Clone(out)
			continue
		}
		for k, v := range mem {
			w, ok := out[k]
			if !ok {
				w = unknown{}
			}
			mem[k] = join(v, w)
		}
		for k := range out {
			if _, ok := mem[k]; !ok {
				mem[k] = unknown{}
			}
		}
	}
	if mem == nil {
		mem = memory{}
	}
	return mem
}

func (f *frame) set(v ssa.Value, x value) {
	if old, ok := f.regs[v]; !ok || fingerprint(old) != fingerprint(x) {
		f.changed = true
	}
	f.regs[v] = x
}

func (f *frame) val(v ssa.Value) value {
	switch v := v.(type) {
	case *ssa.Const:
		return constOf(v)
	case *ssa.Parameter:
		for i, p := range f.fn.Params {
			if p == v && i < len(f.args) {
				return f.args[i]
			}
		}
		return unknown{}
	case *ssa.FreeVar, *ssa.Global, *ssa.Function, *ssa.Builtin:
		return unknown{}
	}
	return f.regs[v]
}

func constOf(c *ssa.Const) value {
	t := c.Type()
	switch {
	case c.Value != nil:
		return constVal{c.Value}
	case isScope(t):
		return emptyScope()
	case isChange(t):
		return &changeVal{}
	case isSlice(t):
		return listVal{}
	}
	return unknown{}
}

func (f *frame) instr(instr ssa.Instruction, mem memory) {
	switch i := instr.(type) {
	case *ssa.Alloc:
		if f.a.tracked(i) {
			zero(i, mem)
		}
		f.set(i, unknown{})
	case *ssa.Store:
		if c, ok := f.cellOf(i.Addr); ok {
			mem[c] = f.val(i.Val)
		}
	case *ssa.UnOp:
		if i.Op == token.MUL {
			f.set(i, f.load(i.X, mem))
		} else {
			f.set(i, unknown{})
		}
	case *ssa.Phi:
		var v value
		for k, e := range i.Edges {
			if _, ok := f.out[i.Block().Preds[k]]; ok {
				v = join(v, f.val(e))
			}
		}
		f.set(i, v)
	case *ssa.ChangeType:
		f.set(i, f.through(i.Type(), i.X))
	case *ssa.Convert:
		f.set(i, f.through(i.Type(), i.X))
	case *ssa.Field:
		if i.Field == 0 && wraps(i.X.Type()) {
			f.set(i, f.val(i.X))
		} else {
			f.set(i, unknown{})
		}
	case *ssa.Extract:
		if t, ok := f.val(i.Tuple).(tuple); ok && i.Index < len(t.elems) {
			f.set(i, t.elems[i.Index])
		} else {
			f.set(i, unknown{})
		}
	case *ssa.Slice:
		f.set(i, f.slice(i, mem))
	case *ssa.MakeSlice:
		f.set(i, f.mapped(i))
	case *ssa.Call:
		f.set(i, f.call(i))
	case *ssa.Defer:
		f.call(i)
	case *ssa.Go:
		f.call(i)
	case *ssa.MakeClosure:
		f.capture(i, mem)
		f.set(i, unknown{})
	case *ssa.Return:
		f.ret = join(f.ret, f.results(i))
	case ssa.Value:
		f.set(i, unknown{})
	}
}

func (f *frame) results(r *ssa.Return) value {
	if len(r.Results) == 1 {
		return f.val(r.Results[0])
	}
	elems := make([]value, len(r.Results))
	for i, v := range r.Results {
		elems[i] = f.val(v)
	}
	if len(elems) == 0 {
		return unknown{}
	}
	return tuple{elems}
}

// through is the value of x converted to t: the same scope or change set, as the types of the runtime, the scope
// package, and the scope types are all one struct to vet.
func (f *frame) through(t types.Type, x ssa.Value) value {
	if isScope(t) || isChange(t) {
		return f.val(x)
	}
	return unknown{}
}

func zero(a *ssa.Alloc, mem memory) {
	t := deref(a.Type())
	if arr, ok := t.Underlying().(*types.Array); ok {
		for k := range int(arr.Len()) {
			mem[cell{a, k}] = zeroOf(arr.Elem())
		}
		return
	}
	mem[cell{a, -1}] = zeroOf(t)
}

func zeroOf(t types.Type) value {
	switch {
	case isScope(t):
		return emptyScope()
	case isChange(t):
		return &changeVal{}
	}
	if b, ok := t.Underlying().(*types.Basic); ok && b.Info()&types.IsString != 0 {
		return constVal{constant.MakeString("")}
	}
	return unknown{}
}

// cellOf is the cell ptr points to, if vet follows it.
func (f *frame) cellOf(ptr ssa.Value) (cell, bool) {
	switch p := ptr.(type) {
	case *ssa.Alloc:
		if f.a.tracked(p) {
			return cell{p, -1}, true
		}
	case *ssa.FieldAddr:
		if p.Field == 0 && wraps(deref(p.X.Type())) {
			return f.cellOf(p.X)
		}
	case *ssa.IndexAddr:
		a, ok := p.X.(*ssa.Alloc)
		k, isConst := constInt(p.Index)
		if ok && isConst && f.a.tracked(a) {
			return cell{a, k}, true
		}
	}
	return cell{}, false
}

func (f *frame) load(ptr ssa.Value, mem memory) value {
	switch p := ptr.(type) {
	case *ssa.FreeVar:
		for i, fv := range f.fn.FreeVars {
			if fv == p && i < len(f.env) {
				return f.env[i]
			}
		}
		return unknown{}
	case *ssa.FieldAddr:
		if p.Field == 0 && wraps(deref(p.X.Type())) {
			return f.load(p.X, mem)
		}
		return unknown{}
	case *ssa.IndexAddr:
		if l, ok := f.val(p.X).(listVal); ok {
			if k, ok := constInt(p.Index); ok {
				if k < len(l.elems) {
					return l.elems[k]
				}
				return unknown{}
			}
			var v value
			for _, e := range l.elems {
				v = join(v, e)
			}
			if v == nil {
				return unknown{}
			}
			return v
		}
	}
	if c, ok := f.cellOf(ptr); ok {
		if v, ok := mem[c]; ok && v != nil {
			return v
		}
	}
	return unknown{}
}

// slice is the elements of an array vet follows the cells of, such as the one holding the arguments of a variadic
// call, as they are now.
func (f *frame) slice(s *ssa.Slice, mem memory) value {
	if s.Low != nil || s.High != nil || s.Max != nil {
		return unknown{}
	}
	if l, ok := f.val(s.X).(listVal); ok {
		return l
	}
	a, ok := s.X.(*ssa.Alloc)
	if !ok || !f.a.tracked(a) {
		return unknown{}
	}
	arr, ok := deref(a.Type()).Underlying().(*types.Array)
	if !ok {
		return unknown{}
	}
	elems := make([]value, arr.Len())
	for k := range elems {
		v, ok := mem[cell{a, k}]
		if !ok || v == nil {
			v = unknown{}
		}
		elems[k] = v
	}
	return listVal{elems}
}

// mapped reads the slice code kanna-orm generates makes of the elements of another, one for each, as in
//
//	qs := make([]impl.Of[model.Order], len(s))
//	for i, x := range s {
//		qs[i] = impl.Of[model.Order](x.Of())
//	}
//
// as the list of what each element becomes. A slice made in any other way is what vet does not know.
func (f *frame) mapped(ms *ssa.MakeSlice) value {
	if !f.a.generated(f.fn) {
		return unknown{}
	}
	var at *ssa.IndexAddr
	for _, r := range *ms.Referrers() {
		switch r := r.(type) {
		case *ssa.IndexAddr:
			if at != nil {
				return unknown{}
			}
			at = r
		case *ssa.Call, *ssa.DebugRef:
		default:
			return unknown{}
		}
	}
	if at == nil {
		return unknown{}
	}
	var store *ssa.Store
	for _, r := range *at.Referrers() {
		s, ok := r.(*ssa.Store)
		if !ok || s.Addr != at || store != nil {
			return unknown{}
		}
		store = s
	}
	if store == nil {
		return unknown{}
	}
	load := elementLoad(store.Val, at.Index)
	if load == nil {
		return unknown{}
	}
	ia, ok := load.X.(*ssa.IndexAddr)
	if !ok {
		return unknown{}
	}
	src := ia.X
	if !isLen(ms.Len, src) {
		return unknown{}
	}
	l, ok := f.val(src).(listVal)
	if !ok {
		return unknown{}
	}
	elems := make([]value, len(l.elems))
	for i, e := range l.elems {
		elems[i] = f.chain(store.Val, load, e)
	}
	return listVal{elems}
}

// elementLoad finds, among what v is made of, the load of an element of a slice at index.
func elementLoad(v ssa.Value, index ssa.Value) *ssa.UnOp {
	switch v := v.(type) {
	case *ssa.UnOp:
		if v.Op != token.MUL {
			return nil
		}
		if ia, ok := v.X.(*ssa.IndexAddr); ok && ia.Index == index {
			return v
		}
		if stored := storedOnce(v.X); stored != nil {
			return elementLoad(stored, index)
		}
	case *ssa.ChangeType:
		return elementLoad(v.X, index)
	case *ssa.Convert:
		return elementLoad(v.X, index)
	case *ssa.Field:
		return elementLoad(v.X, index)
	case *ssa.Call:
		if len(v.Call.Args) == 1 && v.Call.StaticCallee() != nil {
			return elementLoad(v.Call.Args[0], index)
		}
	}
	return nil
}

// storedOnce is the value of the one store to the variable addr points to, or to the struct it is the field of,
// as a range variable whose field is read is.
func storedOnce(addr ssa.Value) ssa.Value {
	if fa, ok := addr.(*ssa.FieldAddr); ok && fa.Field == 0 && wraps(deref(fa.X.Type())) {
		addr = fa.X
	}
	a, ok := addr.(*ssa.Alloc)
	if !ok {
		return nil
	}
	stores := storesTo(a)
	if len(stores) != 1 || stores[0].Addr != a {
		return nil
	}
	return stores[0].Val
}

func isLen(n, of ssa.Value) bool {
	c, ok := n.(*ssa.Call)
	if !ok {
		return false
	}
	b, ok := c.Call.Value.(*ssa.Builtin)
	return ok && b.Name() == "len" && len(c.Call.Args) == 1 && c.Call.Args[0] == of
}

// chain is the value of v, made of load by the conversions and calls elementLoad goes through, with load being e.
func (f *frame) chain(v ssa.Value, load *ssa.UnOp, e value) value {
	if v == load {
		return e
	}
	switch v := v.(type) {
	case *ssa.ChangeType:
		return f.convert(v.Type(), f.chain(v.X, load, e))
	case *ssa.Convert:
		return f.convert(v.Type(), f.chain(v.X, load, e))
	case *ssa.Field:
		if v.Field == 0 && wraps(v.X.Type()) {
			return f.chain(v.X, load, e)
		}
	case *ssa.UnOp:
		if stored := storedOnce(v.X); stored != nil && v.Op == token.MUL {
			return f.chain(stored, load, e)
		}
	case *ssa.Call:
		x := f.chain(v.Call.Args[0], load, e)
		r, _ := f.callee(v, v.Call.StaticCallee(), []value{x})
		return r
	}
	return unknown{}
}

func (f *frame) convert(t types.Type, x value) value {
	if isScope(t) || isChange(t) {
		return x
	}
	return unknown{}
}

// capture keeps what the free variables of the closure mc makes hold, for vet to read the closure with: a variable it
// reads holds, wherever the closure runs, what it holds now, if nothing can write it after.
func (f *frame) capture(mc *ssa.MakeClosure, mem memory) {
	if !f.root {
		return
	}
	fn, ok := mc.Fn.(*ssa.Function)
	if !ok {
		return
	}
	env := make([]value, len(mc.Bindings))
	for i, b := range mc.Bindings {
		env[i] = unknown{}
		a, ok := b.(*ssa.Alloc)
		switch {
		case !ok:
			env[i] = f.val(b)
		case f.a.tracked(a) && !storedAfter(mc, a):
			if v, ok := mem[cell{a, -1}]; ok && v != nil {
				env[i] = v
			}
		}
	}
	if old, ok := f.envs[fn]; ok {
		for i := range env {
			env[i] = join(old[i], env[i])
		}
	}
	f.envs[fn] = env
}

// storedAfter reports whether a is written anywhere the code can go after instr.
func storedAfter(instr ssa.Instruction, a *ssa.Alloc) bool {
	stores := map[*ssa.BasicBlock][]int{}
	for _, s := range storesTo(a) {
		b := s.Block()
		for i, in := range b.Instrs {
			if in == s {
				stores[b] = append(stores[b], i)
			}
		}
	}
	start := instr.Block()
	at := 0
	for i, in := range start.Instrs {
		if in == instr {
			at = i
		}
	}
	for _, i := range stores[start] {
		if i > at {
			return true
		}
	}
	seen := map[*ssa.BasicBlock]bool{}
	work := append([]*ssa.BasicBlock(nil), start.Succs...)
	for len(work) > 0 {
		b := work[len(work)-1]
		work = work[:len(work)-1]
		if seen[b] {
			continue
		}
		seen[b] = true
		if len(stores[b]) > 0 {
			return true
		}
		work = append(work, b.Succs...)
	}
	return false
}

func storesTo(a *ssa.Alloc) []*ssa.Store {
	var out []*ssa.Store
	var addrs []ssa.Value
	addrs = append(addrs, a)
	for _, r := range *a.Referrers() {
		switch r := r.(type) {
		case *ssa.FieldAddr:
			addrs = append(addrs, r)
		case *ssa.IndexAddr:
			addrs = append(addrs, r)
		}
	}
	for _, addr := range addrs {
		for _, r := range *addr.Referrers() {
			if s, ok := r.(*ssa.Store); ok && s.Addr == addr {
				out = append(out, s)
			}
		}
	}
	return out
}

// call reads a call, and returns what it returns.
func (f *frame) call(instr ssa.CallInstruction) value {
	common := instr.Common()
	if common.IsInvoke() {
		return f.invoke(instr)
	}
	callee := common.StaticCallee()
	if callee == nil {
		return unknown{}
	}
	args := make([]value, len(common.Args))
	for i, a := range common.Args {
		args[i] = f.val(a)
		if args[i] == nil {
			args[i] = unknown{}
		}
	}
	v, fail := f.callee(instr, callee, args)
	if fail != nil {
		f.failed(instr, fail)
	}
	return v
}

// callee reads a call of fn with args from instr, and returns what it returns, and the check it fails on every path,
// if any.
func (f *frame) callee(instr ssa.CallInstruction, fn *ssa.Function, args []value) (value, *failed) {
	if fn == nil {
		return unknown{}, nil
	}
	if op := opOf(fn); op != nil {
		return op(opCall{f: f, args: args, at: f.causeOf(instr, fn)})
	}
	generated := f.a.generated(fn)
	if !generated && !returnsFollowed(fn) {
		return unknownResult(fn), nil
	}
	r := f.a.interpret(fn, args, f.siteOf(instr, fn), f.depth+1)
	if !generated {
		// What a function written by hand does with its parameters is not followed, but for the scope it returns.
		return r.val, nil
	}
	return r.val, r.fail
}

// invoke reads a call of a method of an interface, of the repositories kanna-orm generates that implement it: if each
// of them fails it on every path, the call does.
func (f *frame) invoke(instr ssa.CallInstruction) value {
	common := instr.Common()
	targets := f.a.implementations(common.Value.Type(), common.Method)
	if len(targets) == 0 {
		return unknown{}
	}
	args := []value{unknown{}}
	for _, a := range common.Args {
		v := f.val(a)
		if v == nil {
			v = unknown{}
		}
		args = append(args, v)
	}
	var (
		val  value
		fail *failed
		all  = true
	)
	for _, fn := range targets {
		r := f.a.interpret(fn, args, f.siteOf(instr, fn), f.depth+1)
		val = join(val, r.val)
		if r.fail == nil {
			all = false
		} else if fail == nil {
			fail = r.fail
		}
	}
	if all && fail != nil {
		f.failed(instr, fail)
	}
	if val == nil {
		return unknown{}
	}
	return val
}

// failed records that the call instr fails on every path: for the report, if f is of code kanna-orm did not
// generate, and as what f itself fails with, if f is generated and makes the call on every path it returns on.
func (f *frame) failed(instr ssa.CallInstruction, fail *failed) {
	switch {
	case f.root:
		if _, ok := f.reports[instr]; !ok {
			f.reports[instr] = fail
		}
	case f.fail == nil && f.a.generated(f.fn) && f.a.mustRun(instr.Block()):
		f.fail = fail
	}
}

// causeOf is the cause of what a call of the runtime from instr adds to a scope.
func (f *frame) causeOf(instr ssa.CallInstruction, fn *ssa.Function) cause {
	if f.a.generated(f.fn) {
		return f.site
	}
	return cause{pos: f.a.callPos(instr), in: displayName(f.fn), method: fn.Name()}
}

// siteOf is the site of a call of fn from instr.
func (f *frame) siteOf(instr ssa.CallInstruction, fn *ssa.Function) cause {
	switch {
	case f.a.generated(f.fn):
		return f.site
	case f.a.generated(fn):
		return cause{pos: f.a.callPos(instr), in: displayName(f.fn), method: fn.Name()}
	}
	return cause{}
}

// displayName is the name of the function fn is in, a closure being in the one declaring it.
func displayName(fn *ssa.Function) string {
	for fn.Parent() != nil {
		fn = fn.Parent()
	}
	if o := fn.Origin(); o != nil {
		fn = o
	}
	return fn.Name()
}

// returnsFollowed reports whether fn returns a scope or a change set, which vet reads the body of fn for.
func returnsFollowed(fn *ssa.Function) bool {
	for v := range fn.Signature.Results().Variables() {
		if isScope(v.Type()) || isChange(v.Type()) {
			return true
		}
	}
	return false
}

func deref(t types.Type) types.Type {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		return p.Elem()
	}
	return t
}

func isSlice(t types.Type) bool {
	_, ok := t.Underlying().(*types.Slice)
	return ok
}

func constInt(v ssa.Value) (int, bool) {
	c, ok := v.(*ssa.Const)
	if !ok || c.Value == nil || c.Value.Kind() != constant.Int {
		return 0, false
	}
	n, ok := constant.Int64Val(c.Value)
	return int(n), ok
}

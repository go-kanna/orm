package vet

import (
	"go/token"
	"go/types"
	"slices"

	"golang.org/x/tools/go/ssa"

	"github.com/go-kanna/orm/internal/decl"
)

// bindings are what the Binds of the repositories kanna-orm generated take, and the repositories they bind: those
// types, and the interfaces their Binds return.
type bindings struct {
	takes []types.Type
	repos []types.Type
}

func (a *analysis) findBindings() {
	for _, r := range a.repositories {
		obj, _, _ := types.LookupFieldOrMethod(types.NewPointer(r), false, nil, "Bind")
		fn, ok := obj.(*types.Func)
		if !ok {
			continue
		}
		sig := fn.Signature()
		if sig.Params().Len() != 1 || sig.Results().Len() != 1 {
			continue
		}
		a.binds.takes = append(a.binds.takes, sig.Params().At(0).Type())
		a.binds.repos = append(a.binds.repos, r)
		if types.IsInterface(sig.Results().At(0).Type()) {
			a.binds.repos = append(a.binds.repos, sig.Results().At(0).Type())
		}
	}
}

func (a *analysis) isRepository(t types.Type) bool {
	t = types.Unalias(deref(t))
	return slices.ContainsFunc(a.binds.repos, func(r types.Type) bool { return types.Identical(t, r) })
}

// checkBinds reports, in each function literal under fn that takes what a Bind takes, the calls of a repository it
// did not bind: one held by a variable it captures, of a package, or a field of one, that the literal does not write.
func (a *analysis) checkBinds(fn *ssa.Function) {
	if len(a.binds.takes) == 0 || a.generated(fn) {
		return
	}
	for _, anon := range fn.AnonFuncs {
		if tx := a.transaction(anon); tx != nil {
			a.checkBound(anon, tx)
		}
		a.checkBinds(anon)
	}
}

// transaction is the named parameter of fn that a Bind takes, if any: an unnamed one cannot be bound to.
func (a *analysis) transaction(fn *ssa.Function) *ssa.Parameter {
	for _, p := range fn.Params {
		if obj := p.Object(); obj == nil || obj.Name() == "" || obj.Name() == "_" {
			continue
		}
		if slices.ContainsFunc(a.binds.takes, func(t types.Type) bool { return types.AssignableTo(p.Type(), t) }) {
			return p
		}
	}
	return nil
}

func (a *analysis) checkBound(fn *ssa.Function, tx *ssa.Parameter) {
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			call, ok := instr.(ssa.CallInstruction)
			if !ok {
				continue
			}
			recv, name, impls := a.repositoryCall(call.Common())
			if recv == nil || !a.needsTransaction(name, impls) || !unbound(fn, recv) {
				continue
			}
			a.reports = append(a.reports, report{pos: a.callPos(call), in: displayName(fn), fail: &failed{
				msg: name + " runs outside " + tx.Name() + ": call it on the repository Bind(" + tx.Name() + ") returns",
			}})
		}
	}
}

// repositoryCall is the receiver and the name of the method c calls, if it is one of a repository, and the methods
// of the repositories kanna-orm generated it runs.
func (a *analysis) repositoryCall(c *ssa.CallCommon) (ssa.Value, string, []*ssa.Function) {
	if c.IsInvoke() {
		if !a.isRepository(c.Value.Type()) {
			return nil, "", nil
		}
		return c.Value, c.Method.Name(), a.implementations(c.Value.Type(), c.Method)
	}
	callee := c.StaticCallee()
	if callee == nil || callee.Signature.Recv() == nil || len(c.Args) == 0 ||
		!a.isRepository(callee.Signature.Recv().Type()) {
		return nil, "", nil
	}
	return c.Args[0], callee.Name(), []*ssa.Function{callee}
}

// needsTransaction reports whether the method name of a repository, which runs impls, does what a transaction is
// for: writes, as the verb its name starts with tells, or locks the rows it reads, as each of impls checks that it
// runs in a transaction.
func (a *analysis) needsTransaction(name string, impls []*ssa.Function) bool {
	if v, ok := decl.VerbOf(name); ok && v.Writes() {
		return true
	}
	return len(impls) > 0 && !slices.ContainsFunc(impls, func(fn *ssa.Function) bool { return !a.locks(fn) })
}

// locks reports whether fn, a method kanna-orm generated, checks that it runs in a transaction, as one locking the
// rows it reads does.
func (a *analysis) locks(fn *ssa.Function) bool {
	if !a.generated(fn) {
		return false
	}
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			call, ok := instr.(*ssa.Call)
			if !ok {
				continue
			}
			callee := call.Call.StaticCallee()
			if callee == nil {
				continue
			}
			if o := callee.Origin(); o != nil {
				callee = o
			}
			if obj := callee.Object(); obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == implPath &&
				obj.Name() == "InTransaction" {
				return true
			}
		}
	}
	return false
}

// unbound reports whether v, in fn, is a variable fn captures or one of a package, or what a field of one holds,
// which fn does not write, and which fn does not capture from what a Bind returned.
func unbound(fn *ssa.Function, v ssa.Value) bool {
	for {
		switch x := v.(type) {
		case *ssa.Field:
			v = x.X
		case *ssa.FieldAddr:
			v = x.X
		case *ssa.UnOp:
			if x.Op != token.MUL {
				return false
			}
			v = x.X
		case *ssa.Global:
			return !writes(fn, x)
		case *ssa.FreeVar:
			return !writes(fn, x) && !capturesBind(fn, x)
		default:
			return false
		}
	}
}

// writes reports whether fn stores into root, or into a field of it.
func writes(fn *ssa.Function, root ssa.Value) bool {
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			s, ok := instr.(*ssa.Store)
			if !ok {
				continue
			}
			addr := s.Addr
			for f, ok := addr.(*ssa.FieldAddr); ok; f, ok = addr.(*ssa.FieldAddr) {
				addr = f.X
			}
			if addr == root {
				return true
			}
		}
	}
	return false
}

// capturesBind reports whether fv, a free variable of fn, is what a Bind returned, or a variable a Bind's result
// is stored into, in the function making fn.
func capturesBind(fn *ssa.Function, fv *ssa.FreeVar) bool {
	i := slices.Index(fn.FreeVars, fv)
	parent := fn.Parent()
	if i < 0 || parent == nil {
		return true
	}
	for _, b := range parent.Blocks {
		for _, instr := range b.Instrs {
			mc, ok := instr.(*ssa.MakeClosure)
			if !ok || mc.Fn != fn || i >= len(mc.Bindings) {
				continue
			}
			v := mc.Bindings[i]
			if up, ok := v.(*ssa.FreeVar); ok && capturesBind(parent, up) || isBind(v) {
				return true
			}
			for _, r := range *v.Referrers() {
				if s, ok := r.(*ssa.Store); ok && s.Addr == v && isBind(s.Val) {
					return true
				}
			}
		}
	}
	return false
}

func isBind(v ssa.Value) bool {
	call, ok := v.(*ssa.Call)
	if !ok {
		return false
	}
	if call.Call.IsInvoke() {
		return call.Call.Method.Name() == "Bind"
	}
	callee := call.Call.StaticCallee()
	return callee != nil && callee.Signature.Recv() != nil && callee.Name() == "Bind"
}

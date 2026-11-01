package gen

import (
	"go/ast"
	"go/types"

	"github.com/go-kanna/internal/diag"
)

func (r repository) operationType() string { return lowerFirst(r.name) + "Operation" }

func (r repository) operationNames() []string {
	names := []string{r.operationType()}
	for _, m := range r.methods {
		if m.bind == nil {
			names = append(names, r.operationType()+m.fn.Name())
		}
	}
	return names
}

func (r repoReader) operationCollisions(repo repository) []diag.Diag {
	var ds []diag.Diag
	for _, name := range repo.operationNames() {
		if pos, exists := r.declared[name]; exists {
			ds = append(ds, diag.Errorf(pos, "%s: generated operation name %s is already declared", repo.name, name))
		}
	}
	return ds
}

func (e repoEmitter) operations(repo repository) {
	typ := repo.operationType()
	e.printf("type %s string\n\n", typ)
	for _, m := range repo.methods {
		if m.bind != nil {
			continue
		}
		e.printf("const %s%s %s = %q\n", typ, m.fn.Name(), typ, m.fn.Name())
	}
	e.printf("\nvar _ func(%s, error) error = %s\n\n", typ, repo.errors)
}

func (r repoReader) validateTranslation(repo repository) []diag.Diag {
	fail := func() []diag.Diag {
		return []diag.Diag{diag.Errorf(repo.pos, "%s: errors=%s requires a function in the implementation package "+
			"with signature func(%s, error) error",
			repo.name, repo.errors, repo.operationType())}
	}
	if r.errorPkg == nil {
		return fail()
	}
	for _, file := range r.errorPkg.Syntax {
		if ast.IsGenerated(file) {
			continue
		}
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name.Name != repo.errors {
				continue
			}
			obj, ok := r.errorPkg.Types.Scope().Lookup(fn.Name.Name).(*types.Func)
			if !ok {
				return fail()
			}
			sig := obj.Signature()
			if sig.TypeParams().Len() != 0 || sig.Variadic() || sig.Params().Len() != 2 || sig.Results().Len() != 1 {
				return fail()
			}
			errType := types.Universe.Lookup("error").Type()
			if !types.Identical(sig.Params().At(1).Type(), errType) || !types.Identical(sig.Results().At(0).Type(), errType) {
				return fail()
			}
			op := sig.Params().At(0).Type()
			if named, ok := types.Unalias(op).(*types.Named); ok && named.Obj().Name() == repo.operationType() &&
				named.Obj().Pkg() == r.errorPkg.Types {
				return nil
			}
			// The operation type does not exist until the first generation.
			ident, ok := fn.Type.Params.List[0].Type.(*ast.Ident)
			if types.Identical(op, types.Typ[types.Invalid]) && ok && ident.Name == repo.operationType() {
				return nil
			}
			return fail()
		}
	}
	return fail()
}

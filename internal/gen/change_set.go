package gen

import (
	"go/ast"
	"go/types"
	"slices"

	"github.com/go-kanna/internal/packages"
)

func (r repoReader) changesParam(p *types.Var, field *ast.Field, ts *ast.TypeSpec) (*types.Named, bool) {
	if model, ok := scopeModelKind(p.Type(), "Changes"); ok || !packages.Invalid(p.Type()) {
		return model, ok
	}
	if field == nil {
		return nil, false
	}
	ft, ok := field.Type.(*ast.FuncType)
	if !ok || ft.Params == nil {
		return nil, false
	}
	for _, param := range ft.Params.List {
		if !slices.ContainsFunc(param.Names, func(n *ast.Ident) bool { return n.Name == p.Name() }) {
			continue
		}
		pkgPath, name := r.pkg.PkgPath, ""
		switch expr := param.Type.(type) {
		case *ast.Ident:
			name = expr.Name
		case *ast.SelectorExpr:
			id, ok := expr.X.(*ast.Ident)
			if !ok {
				return nil, false
			}
			pkgPath, name = r.importPath(ts, id.Name), expr.Sel.Name
		}
		for _, entry := range slices.Concat(r.l.loaded, r.l.idx) {
			if set, _ := changeSetNames(entry.name); entry.pkgPath != pkgPath || set != name {
				continue
			}
			if entry.model != nil {
				return entry.model, true
			}
			if pkg := r.imported[entry.modelPath]; pkg != nil {
				if obj, ok := pkg.Scope().Lookup(entry.modelName).(*types.TypeName); ok {
					model := asNamed(obj.Type())
					return model, model != nil
				}
			}
		}
	}
	return nil, false
}

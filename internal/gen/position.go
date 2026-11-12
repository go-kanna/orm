package gen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"sync"
)

// fieldPosition is where f is declared. A package read from export data, as the packages of models and projections
// are, keeps the line of a field but not its column, which fieldPosition finds in the source then.
func fieldPosition(fset *token.FileSet, f *types.Var) token.Position {
	pos := fset.Position(f.Pos())
	if pos.Column > 1 || pos.Filename == "" {
		return pos
	}
	for _, p := range sourceFields(fset, pos.Filename)[f.Name()] {
		if p.Line == pos.Line {
			return p
		}
	}
	return pos
}

type sourceFile struct {
	fset *token.FileSet
	name string
}

// sourceFieldCache holds, of each file read, where it declares the fields of each name.
var sourceFieldCache sync.Map // sourceFile → func() map[string][]token.Position

// sourceFields tells where the file named name, of the packages fset was loaded with, declares the fields of each
// name: nothing when it cannot be read.
func sourceFields(fset *token.FileSet, name string) map[string][]token.Position {
	load, _ := sourceFieldCache.LoadOrStore(sourceFile{fset, name}, sync.OnceValue(func() map[string][]token.Position {
		fs := token.NewFileSet()
		file, err := parser.ParseFile(fs, name, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil
		}
		fields := map[string][]token.Position{}
		ast.Inspect(file, func(n ast.Node) bool {
			st, ok := n.(*ast.StructType)
			if !ok {
				return true
			}
			for _, f := range st.Fields.List {
				names := f.Names
				if id := embeddedIdent(f.Type); len(names) == 0 && id != nil {
					names = []*ast.Ident{id}
				}
				for _, id := range names {
					fields[id.Name] = append(fields[id.Name], fs.Position(id.Pos()))
				}
			}
			return true
		})
		return fields
	}))
	return load.(func() map[string][]token.Position)() //nolint:forcetypeassert // the cache holds nothing else
}

// embeddedIdent is the name of the field a type embeds, as go/types takes it.
func embeddedIdent(e ast.Expr) *ast.Ident {
	switch e := e.(type) {
	case *ast.StarExpr:
		return embeddedIdent(e.X)
	case *ast.Ident:
		return e
	case *ast.SelectorExpr:
		return e.Sel
	case *ast.IndexExpr:
		return embeddedIdent(e.X)
	case *ast.IndexListExpr:
		return embeddedIdent(e.X)
	}
	return nil
}

package gen

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/packages"
	"github.com/go-kanna/internal/suggest"

	"github.com/go-kanna/orm/internal/placeholder"
	"github.com/go-kanna/orm/internal/snapshot"
	"github.com/go-kanna/orm/internal/sqlfile"
)

// sqlScope is a scope written by hand in a .sql file: a method of a scope type, narrowing it by a condition.
type sqlScope struct {
	name string
	// file is the .sql file it is written in, doc the comment above its -- scope: line, and pos where that line is.
	file, doc string
	pos       token.Position
	params    []param
	// pieces are the pieces of SQL the condition is written in, and args the names of the parameters between them.
	pieces, args []string
}

type param struct {
	name     string
	typ      types.Type
	variadic bool
}

// sqlScopes reads the scopes written in the .sql files next to the scope types of pkg, plans, for a database of
// dialect s.Dialect, and checks each against what describe recorded of it in s.
func sqlScopes(pkg *packages.Package, s snapshot.Snapshot, plans []scopePlan) (map[string][]sqlScope, []diag.Diag) {
	if len(plans) == 0 {
		return nil, nil
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(pkg.GoFiles[0]), "*.sql"))
	if err != nil {
		return nil, []diag.Diag{diag.Errorf(token.Position{}, "%s: %v", pkg.PkgPath, err)}
	}
	names := make([]string, 0, len(plans))
	for _, p := range plans {
		names = append(names, p.name)
	}
	r := resolver{pkg: pkg}

	var (
		scopes = map[string][]sqlScope{}
		ds     []diag.Diag
		seen   = map[string]token.Position{}
	)
	for _, file := range files {
		data, err := os.ReadFile(file) //nolint:gosec // a .sql file beside the sources of a package loaded
		if err != nil {
			ds = append(ds, diag.Errorf(token.Position{Filename: file}, "%v", err))
			continue
		}
		stmts, fds := sqlfile.Parse(file, data)
		ds = append(ds, fds...)
		for _, st := range stmts {
			if st.Kind != sqlfile.Scope || st.Name == "" || st.Text == "" {
				continue
			}
			label := "-- scope: " + st.Type + "." + st.Name
			errorf := func(format string, args ...any) diag.Diag {
				return diag.Errorf(st.Header, "%s: %s", label, fmt.Sprintf(format, args...))
			}
			if at, ok := seen[st.Type+"."+st.Name]; ok {
				if at.Filename != st.Header.Filename { // sqlfile.Parse reports one written twice in a file
					ds = append(ds, errorf("it is written a second time").
						WithHints(fmt.Sprintf("it is also at %s:%d", filepath.Base(at.Filename), at.Line)))
				}
				continue
			}
			seen[st.Type+"."+st.Name] = st.Header
			if !slices.Contains(names, st.Type) {
				d := errorf("%s is no //kanna:scope type of this package", st.Type)
				if near, ok := suggest.Closest(st.Type, names); ok {
					d = d.WithHints("did you mean " + near + "?")
				}
				ds = append(ds, d)
				continue
			}

			described, ok := s.Statements[pkg.PkgPath+"."+st.Type+"."+st.Name]
			switch {
			case !ok:
				ds = append(ds, errorf("the snapshot has not described it").
					WithHints("run go tool kanna-orm describe to describe it"))
				continue
			case described.Digest != snapshot.Digest(st.Text):
				ds = append(ds, errorf("it has changed since the snapshot described it").
					WithHints("run go tool kanna-orm describe to describe it again"))
				continue
			}

			sc, sds := r.sqlScope(st, s.Dialect, errorf)
			ds = append(ds, sds...)
			if !diag.HasErrors(sds) {
				sc.file = filepath.Base(file)
				scopes[st.Type] = append(scopes[st.Type], sc)
			}
		}
	}
	return scopes, ds
}

// sqlScope reads st as a scope of a database of dialect d. errorf reports a problem with it.
func (r resolver) sqlScope(st sqlfile.Statement, d snapshot.Dialect,
	errorf func(format string, args ...any) diag.Diag,
) (sqlScope, []diag.Diag) {
	sc := sqlScope{name: st.Name, doc: st.Doc, pos: st.Header}
	ft, err := parser.ParseExpr("func(" + st.Params + ")")
	fn, ok := ft.(*ast.FuncType)
	if err != nil || !ok {
		return sc, []diag.Diag{errorf("(%s) does not read as the parameters of a Go function", st.Params).
			WithHints("as in -- scope: Order.PlacedInMonth(at time.Time)")}
	}
	for i, field := range fn.Params.List {
		expr, variadic := field.Type, false
		if e, ok := expr.(*ast.Ellipsis); ok {
			expr, variadic = e.Elt, true
		}
		t, err := r.typeOf(expr)
		if err != nil {
			return sc, []diag.Diag{errorf("%v", err)}
		}
		if variadic {
			t = types.NewSlice(t)
		}
		if len(field.Names) == 0 {
			return sc, []diag.Diag{errorf("its parameter %d has no name, which the statement takes it by", i+1)}
		}
		for _, n := range field.Names {
			sc.params = append(sc.params, param{name: n.Name, typ: t, variadic: variadic})
		}
	}

	pieces, args, err := placeholder.Split(st.Text, d)
	if e, ok := errors.AsType[*placeholder.Error](err); ok {
		pos := token.Position{Filename: st.Header.Filename, Line: st.Line + e.Line - 1, Column: e.Column}
		return sc, []diag.Diag{diag.Errorf(pos, "%s", e.Message)}
	}
	if err != nil {
		return sc, []diag.Diag{errorf("%v", err)}
	}
	sc.pieces, sc.args = pieces, args

	var ds []diag.Diag
	for _, a := range args {
		if !slices.ContainsFunc(sc.params, func(p param) bool { return p.name == a }) {
			ds = append(ds, errorf(":%s is none of its parameters", a).WithHints("declare it on the -- scope: line"))
			break
		}
	}
	for _, p := range sc.params {
		if !slices.Contains(args, p.name) {
			ds = append(ds, diag.Warningf(st.Header, "-- scope: %s.%s: the statement does not use %s", st.Type,
				st.Name, p.name))
		}
	}
	return sc, ds
}

// resolver reads the types of the parameters of a scope written by hand, as the Go files of pkg would: an identifier
// by what pkg declares and the predeclared types, and a type of another package by the name a file of pkg imports it
// by, or else by the name of a package pkg imports, directly or not, when only one of them has the name.
type resolver struct {
	pkg *packages.Package
}

func (r resolver) typeOf(e ast.Expr) (types.Type, error) {
	switch e := e.(type) {
	case *ast.Ident:
		if obj, ok := r.pkg.Types.Scope().Lookup(e.Name).(*types.TypeName); ok {
			return obj.Type(), nil
		}
		if obj, ok := types.Universe.Lookup(e.Name).(*types.TypeName); ok {
			return obj.Type(), nil
		}
		return nil, fmt.Errorf("%s names no type", e.Name)
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		if !ok {
			break
		}
		p, err := r.imported(x.Name)
		if err != nil {
			return nil, err
		}
		obj, ok := p.Scope().Lookup(e.Sel.Name).(*types.TypeName)
		if !ok || !obj.Exported() {
			return nil, fmt.Errorf("%s.%s names no type of %s", x.Name, e.Sel.Name, p.Path())
		}
		return obj.Type(), nil
	case *ast.StarExpr:
		t, err := r.typeOf(e.X)
		if err != nil {
			return nil, err
		}
		return types.NewPointer(t), nil
	case *ast.ArrayType:
		if e.Len != nil {
			break
		}
		t, err := r.typeOf(e.Elt)
		if err != nil {
			return nil, err
		}
		return types.NewSlice(t), nil
	case *ast.MapType:
		k, err := r.typeOf(e.Key)
		if err != nil {
			return nil, err
		}
		v, err := r.typeOf(e.Value)
		if err != nil {
			return nil, err
		}
		return types.NewMap(k, v), nil
	}
	return nil, fmt.Errorf("%s is a type kanna-orm cannot read here: name a type, a pointer, a slice, or a map",
		types.ExprString(e))
}

// imported finds the package a file of r.pkg would refer to as name.
func (r resolver) imported(name string) (*types.Package, error) {
	byPath := map[string]*types.Package{}
	for _, p := range r.pkg.Types.Imports() {
		byPath[p.Path()] = p
	}
	for _, f := range r.pkg.Syntax {
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			p := byPath[path]
			if err != nil || p == nil {
				continue
			}
			if imp.Name != nil && imp.Name.Name == name || imp.Name == nil && p.Name() == name {
				return p, nil
			}
		}
	}

	var found []*types.Package
	seen := map[*types.Package]bool{}
	for next := r.pkg.Types.Imports(); len(next) > 0 && len(found) == 0; {
		var deeper []*types.Package
		for _, p := range next {
			if seen[p] {
				continue
			}
			seen[p] = true
			if p.Name() == name {
				found = append(found, p)
			}
			deeper = append(deeper, p.Imports()...)
		}
		next = deeper
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("%s names no package that package %s imports", name, r.pkg.Name)
	case 1:
		return found[0], nil
	}
	return nil, fmt.Errorf("%s names both %s and %s", name, found[0].Path(), found[1].Path())
}

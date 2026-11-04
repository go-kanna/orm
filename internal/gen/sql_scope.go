package gen

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
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

// sqlScopes reads the scopes of stmts, those of the .sql files next to the scope types of pkg, plans, for a database
// of dialect s.Dialect, and checks each against what describe recorded of it in s.
func sqlScopes(pkg *packages.Package, s snapshot.Snapshot, plans []scopePlan, stmts []sqlfile.Statement) (
	map[string][]sqlScope, []diag.Diag,
) {
	if len(plans) == 0 {
		return nil, nil
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
			sc.file = filepath.Base(st.Header.Filename)
			scopes[st.Type] = append(scopes[st.Type], sc)
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
			d := errorf("%v", err)
			if ni := (notImportedError{}); errors.As(err, &ni) && ni.path != "" {
				d = d.WithHints(fmt.Sprintf("import it in a Go file of the package, as in import _ %q", ni.path))
			}
			return sc, []diag.Diag{d}
		}
		if variadic {
			t = types.NewSlice(t)
		}
		if len(field.Names) == 0 {
			return sc, []diag.Diag{errorf("its parameter %d has no name, which the statement takes it by", i+1)}
		}
		for _, n := range field.Names {
			if n.Name != "_" && slices.ContainsFunc(sc.params, func(p param) bool { return p.name == n.Name }) {
				return sc, []diag.Diag{errorf("two of its parameters are named %s", n.Name)}
			}
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
// by what pkg declares and the predeclared types, and a type of another package by the name the files of pkg import
// it by.
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

// imported finds the package the Go files of r.pkg written by hand import as name, by that name or, for one imported
// for its side effects alone, as in import _ "time", by the package's own. A package they do not import is not looked
// for among those they import in turn, which would change as those change; it is only told of, as one to import.
func (r resolver) imported(name string) (*types.Package, error) {
	byPath := map[string]*types.Package{}
	for _, p := range r.pkg.Types.Imports() {
		byPath[p.Path()] = p
	}
	var found *types.Package
	for _, f := range r.pkg.Syntax {
		if ast.IsGenerated(f) {
			continue // What the files kanna-orm wrote import is no declaration of the package's.
		}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			p := byPath[path]
			if err != nil || p == nil {
				continue
			}
			local := p.Name()
			if imp.Name != nil && imp.Name.Name != "_" {
				local = imp.Name.Name
			}
			switch {
			case local != name, found == p:
			case found != nil:
				return nil, fmt.Errorf("%s names both %s and %s, which files of package %s import", name, found.Path(),
					p.Path(), r.pkg.Name)
			default:
				found = p
			}
		}
	}
	if found != nil {
		return found, nil
	}
	path, _ := r.deeper(name)
	return nil, notImportedError{name: name, pkg: r.pkg.Name, path: path}
}

// notImportedError is the error of name, which no package the files of pkg import is named, with path, the one
// package of the name among those they import in turn, if there is one, which they could import.
type notImportedError struct{ name, pkg, path string }

func (e notImportedError) Error() string {
	return fmt.Sprintf("%s names no package that package %s imports", e.name, e.pkg)
}

// deeper finds the import path of the one package named name among those the packages r.pkg imports import, at any
// depth, to tell of.
func (r resolver) deeper(name string) (string, bool) {
	var found []string
	seen := map[*types.Package]bool{}
	for next := r.pkg.Types.Imports(); len(next) > 0; {
		var deeper []*types.Package
		for _, p := range next {
			if seen[p] {
				continue
			}
			seen[p] = true
			if p.Name() == name {
				found = append(found, p.Path())
			}
			deeper = append(deeper, p.Imports()...)
		}
		next = deeper
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}

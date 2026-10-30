package gen

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/packages"
	"github.com/go-kanna/internal/suggest"

	"github.com/go-kanna/orm/internal/decl"
	"github.com/go-kanna/orm/internal/snapshot"
)

const (
	// ormPath is the import path of the package the generated code runs on.
	ormPath = "github.com/go-kanna/orm"
	// outputName is the file kanna-orm writes into each package it generates for.
	outputName = "orm_gen.go"
)

// scope is a //kanna:scope type.
type scope struct {
	name string
	// pos is where its directive is.
	pos token.Position
	// field names the type's one field, the orm.Of the generated methods narrow.
	field string
	// model is the struct the rows of table are read into.
	model *types.Named
	table string
	// fn names the function returning the empty scope.
	fn string
	// implied reports whether a //kanna:scopes directive implies it, so that the run declares it too.
	implied bool
}

// findScopes returns the //kanna:scope types of pkg, in the order they are declared, then those its //kanna:scopes
// directives imply, as idx has them, and reports those that are not declared as a scope type is, or whose table s
// does not have.
func findScopes(pkg *packages.Package, s snapshot.Snapshot, idx []indexed) ([]scope, []diag.Diag) {
	var (
		scopes []scope
		ds     []diag.Diag
		fns    = map[string]string{}
		models []decl.Models
	)
	add := func(sc scope, sds []diag.Diag) {
		if fn, ok := fns[sc.fn]; ok {
			sds = append(sds, diag.Errorf(sc.pos, "%s: %s is the function returning the empty scope of %s too",
				sc.name, sc.fn, fn).WithHints("name another with func="))
		}
		ds = append(ds, sds...)
		if len(sds) == 0 {
			scopes = append(scopes, sc)
			fns[sc.fn] = sc.name
		}
	}
	for _, file := range pkg.Syntax {
		if ast.IsGenerated(file) {
			continue
		}
		decls, fds := decl.Read(pkg.Fset, file)
		ds = append(ds, fds...)
		for _, d := range decls.Scopes {
			add(scopeOf(pkg, s, d))
		}
		models = append(models, decls.Models...)
	}
	for _, e := range idx {
		if e.implied && e.pkgPath == pkg.PkgPath {
			add(impliedScope(pkg, e))
		}
	}
	return scopes, append(ds, impliesNothing(pkg, s, models)...)
}

// impliedScope reads the scope type e, which a //kanna:scopes directive of pkg implies.
func impliedScope(pkg *packages.Package, e indexed) (scope, []diag.Diag) {
	sc := scope{name: e.name, pos: e.pos, field: "q", model: e.model, table: e.table, implied: true}
	errorf := func(format string, args ...any) []diag.Diag {
		return []diag.Diag{diag.Errorf(e.pos, "%s: %s", sc.name, fmt.Sprintf(format, args...))}
	}
	fn, ok := funcName(e.table)
	sc.fn = fn
	switch {
	case e.model == nil:
		return sc, errorf("kanna-orm loaded no struct %s of %s, which //kanna:scopes implies it of", e.modelName,
			e.modelPath)
	case !ok:
		return sc, errorf("no Go name can be made of %s for the function returning the empty scope", e.table)
	}
	for _, name := range []string{sc.name, fn} {
		if pos, ok := declared(pkg, name); ok {
			return sc, []diag.Diag{diag.Errorf(e.pos, "%s: %s, which the scope type //kanna:scopes implies of %s needs, "+
				"is declared already", sc.name, name, e.table).WithHints(
				fmt.Sprintf("it is declared at %s:%d", filepath.Base(pos.Filename), pos.Line),
				"declare the scope type yourself: //kanna:scope table="+e.table)}
		}
	}
	return sc, nil
}

// impliesNothing reports the packages the //kanna:scopes directives of pkg, models, name that no scope type can be
// implied of: one outside the module of pkg, or one with no struct named after a table of s.
func impliesNothing(pkg *packages.Package, s snapshot.Snapshot, models []decl.Models) []diag.Diag {
	if pkg.Module == nil {
		return nil
	}
	root, modPath := pkg.Module.Dir, pkg.Module.Path
	hasTable := func(table string) bool { _, ok := s.Tables[table]; return ok }
	var ds []diag.Diag
	for _, m := range models {
		for _, path := range m.Paths {
			if _, _, ok := m.Resolve(path, root, modPath); !ok {
				ds = append(ds, diag.Errorf(m.Pos, "//kanna:scopes takes the packages of %s, not %s", modPath, path).
					WithHints("declare a scope type of each table of its models with //kanna:scope"))
				continue
			}
			if len(decl.Imply(decl.Models{Pos: m.Pos, Paths: []string{path}}, root, modPath, hasTable)) == 0 {
				ds = append(ds, diag.Warningf(m.Pos, "//kanna:scopes %s implies no scope type: no struct of it is named "+
					"after a table of the snapshot", path).WithHints("as order_lines is after OrderLine"))
			}
		}
	}
	return ds
}

// scopeOf reads the scope type d declares.
func scopeOf(pkg *packages.Package, s snapshot.Snapshot, d decl.Scope) (scope, []diag.Diag) {
	field, model, ds := modelOf(pkg, d.Spec)
	sc := scope{name: d.Spec.Name.Name, pos: d.Pos, field: field, model: model, table: d.Table}

	if _, ok := s.Tables[d.Table]; !ok {
		missing := diag.Errorf(d.Pos, "%s: the snapshot has no table %s", sc.name, d.Table)
		if near, ok := suggest.Closest(d.Table, slices.Sorted(maps.Keys(s.Tables))); ok {
			missing = missing.WithHints(fmt.Sprintf("did you mean %s?", near))
		} else {
			missing = missing.WithHints("if it is new, describe the database again: go tool kanna-orm describe")
		}
		return sc, append(ds, missing)
	}

	fn := d.Func
	if fn == "" {
		var ok bool
		if fn, ok = funcName(d.Table); !ok {
			return sc, append(ds, diag.Errorf(d.Pos, "%s: no Go name can be made of %s", sc.name, d.Table).
				WithHints("name the function returning the empty scope with func="))
		}
	}
	sc.fn = fn
	if fn == sc.name {
		ds = append(ds, diag.Errorf(d.Pos, "%s: the function returning the empty scope would be named %s, as the type is",
			sc.name, fn).WithHints("name another with func="))
	} else if pos, ok := declared(pkg, fn); ok {
		ds = append(ds, diag.Errorf(d.Pos, "%s: %s, the function returning the empty scope, is declared already",
			sc.name, fn).WithHints(fmt.Sprintf("it is declared at %s:%d", filepath.Base(pos.Filename), pos.Line),
			"name another with func="))
	}
	return sc, ds
}

// modelOf returns the one field of the scope type ts declares, and M, the model of the orm.Of[M] it holds.
func modelOf(pkg *packages.Package, ts *ast.TypeSpec) (string, *types.Named, []diag.Diag) {
	name := ts.Name.Name
	pos := pkg.Fset.Position(ts.Name.Pos())
	shape := diag.Errorf(pos, "%s: a scope type is a struct whose only field is an orm.Of of its model", name).
		WithHints(fmt.Sprintf("as in type %s struct{ q orm.Of[model.%[1]s] }", name))
	if ts.TypeParams != nil {
		return "", nil, []diag.Diag{diag.Errorf(pos, "%s: a scope type is not generic: it is the scope of one table", name)}
	}
	obj, ok := pkg.Types.Scope().Lookup(name).(*types.TypeName)
	if !ok || obj.IsAlias() {
		return "", nil, []diag.Diag{shape}
	}
	st, ok := obj.Type().Underlying().(*types.Struct)
	if !ok || st.NumFields() != 1 {
		return "", nil, []diag.Diag{shape}
	}
	f := st.Field(0)
	if packages.Invalid(f.Type()) {
		return "", nil, []diag.Diag{packages.NotTypeChecked(pos)}
	}
	of, ok := types.Unalias(f.Type()).(*types.Named)
	if !ok || of.Obj().Pkg() == nil || of.Obj().Pkg().Path() != ormPath || of.Obj().Name() != "Of" {
		return "", nil, []diag.Diag{shape}
	}

	qualifier := relativeTo(pkg.Types)
	m := types.Unalias(of.TypeArgs().At(0))
	if packages.Invalid(m) {
		return "", nil, []diag.Diag{packages.NotTypeChecked(pos)}
	}
	example := "q orm.Of[" + types.TypeString(m, qualifier) + "]"
	switch {
	case f.Embedded():
		return "", nil, []diag.Diag{diag.Errorf(pos, "%s: its orm.Of is embedded, which makes Where a method of the scope",
			name).WithHints("name the field: " + example)}
	case f.Exported():
		return "", nil, []diag.Diag{diag.Errorf(pos, "%s: the field %s is exported, so code outside the package could "+
			"narrow it with SQL of its own", name, f.Name()).WithHints("unexport it: " + example)}
	}
	model, ok := m.(*types.Named)
	if !ok || !isStruct(model) {
		d := diag.Errorf(pos, "%s: the model, %s, is not a named struct", name, types.TypeString(m, qualifier))
		if p, ok := m.(*types.Pointer); ok {
			d = d.WithHints("hold the struct itself: orm.Of[" + types.TypeString(p.Elem(), qualifier) + "]")
		}
		return "", nil, []diag.Diag{d}
	}
	return f.Name(), model, nil
}

func isStruct(t types.Type) bool {
	_, ok := t.Underlying().(*types.Struct)
	return ok
}

// funcName names the function returning the empty scope of table after it, in camel case: Orders for orders, and
// OrderLines for order_lines. It reports false for a table no exported Go name is made of that way.
func funcName(table string) (string, bool) {
	var b strings.Builder
	for word := range strings.SplitSeq(table, "_") {
		if word == "" {
			continue
		}
		r, size := utf8.DecodeRuneInString(word)
		b.WriteRune(unicode.ToUpper(r))
		b.WriteString(word[size:])
	}
	name := b.String()
	return name, token.IsIdentifier(name) && token.IsExported(name)
}

// declared reports where pkg declares name at the top level, unless it is the output of an earlier run, which is to
// be written again.
func declared(pkg *packages.Package, name string) (token.Position, bool) {
	obj := pkg.Types.Scope().Lookup(name)
	if obj == nil {
		return token.Position{}, false
	}
	pos := pkg.Fset.Position(obj.Pos())
	if filepath.Base(pos.Filename) == outputName && packages.GeneratedFiles(pkg)[pos.Filename] {
		return token.Position{}, false
	}
	return pos, true
}

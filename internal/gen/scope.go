package gen

import (
	"errors"
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
	"github.com/go-kanna/internal/directive"
	"github.com/go-kanna/internal/packages"
	"github.com/go-kanna/internal/suggest"

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
	// field names the type's one field, the orm.Of the generated methods narrow.
	field string
	// model is the struct the rows of table are read into.
	model *types.Named
	table string
	// fn names the function returning the empty scope.
	fn string
}

// findScopes returns the //kanna:scope types of pkg, in the order they are declared, and reports those that are not
// declared as a scope type is, or whose table s does not have.
func findScopes(pkg *packages.Package, s snapshot.Snapshot) ([]scope, []diag.Diag) {
	var (
		scopes []scope
		ds     []diag.Diag
		fns    = map[string]string{}
	)
	for _, file := range pkg.Syntax {
		if ast.IsGenerated(file) {
			continue
		}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				doc := ts.Doc
				if doc == nil && len(gd.Specs) == 1 {
					doc = gd.Doc
				}
				d, fds := directive.Find(pkg.Fset, doc, "scope")
				ds = append(ds, fds...)
				if !d.Found {
					continue
				}
				sc, sds := scopeOf(pkg, s, ts, d)
				if fn, ok := fns[sc.fn]; ok {
					sds = append(sds, diag.Errorf(d.Pos, "%s: %s is the function returning the empty scope of %s too",
						sc.name, sc.fn, fn).WithHints("name another with func="))
				}
				ds = append(ds, sds...)
				if len(sds) == 0 {
					scopes = append(scopes, sc)
					fns[sc.fn] = sc.name
				}
			}
		}
	}
	return scopes, ds
}

// scopeOf reads ts, which d marks as a scope type.
func scopeOf(pkg *packages.Package, s snapshot.Snapshot, ts *ast.TypeSpec, d directive.Directive) (scope, []diag.Diag) {
	field, model, ds := modelOf(pkg, ts)
	sc := scope{name: ts.Name.Name, field: field, model: model}

	table, fn, err := scopeArgs(d.Args)
	if err != nil {
		return sc, append(ds, diag.Errorf(d.Pos, "%v", err).WithHints("as in //kanna:scope table=orders"))
	}
	sc.table = table

	if _, ok := s.Tables[table]; !ok {
		missing := diag.Errorf(d.Pos, "%s: the snapshot has no table %s", sc.name, table)
		if near, ok := suggest.Closest(table, slices.Sorted(maps.Keys(s.Tables))); ok {
			missing = missing.WithHints(fmt.Sprintf("did you mean %s?", near))
		} else {
			missing = missing.WithHints("if it is new, describe the database again: go tool kanna-orm describe")
		}
		return sc, append(ds, missing)
	}

	if fn == "" {
		var ok bool
		if fn, ok = funcName(table); !ok {
			return sc, append(ds, diag.Errorf(d.Pos, "%s: no Go name can be made of %s", sc.name, table).
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
	of, ok := types.Unalias(f.Type()).(*types.Named)
	if !ok || of.Obj().Pkg() == nil || of.Obj().Pkg().Path() != ormPath || of.Obj().Name() != "Of" {
		return "", nil, []diag.Diag{shape}
	}

	qualifier := func(p *types.Package) string {
		if p == pkg.Types {
			return ""
		}
		return p.Name()
	}
	m := types.Unalias(of.TypeArgs().At(0))
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

// scopeArgs reads what follows //kanna:scope: the table, and the name func= gives the function returning the empty
// scope, if any.
func scopeArgs(args string) (table, fn string, err error) {
	for field := range strings.FieldsSeq(args) {
		key, value, _ := strings.Cut(field, "=")
		switch {
		case key == "table" && table != "", key == "func" && fn != "":
			return "", "", fmt.Errorf("//kanna:scope gives %s= twice", key)
		case key == "table" && value != "":
			table = value
		case key == "func" && token.IsIdentifier(value):
			fn = value
		case key == "func":
			return "", "", fmt.Errorf("//kanna:scope func=%s: %q is not a name a Go function can have", value, value)
		default:
			return "", "", fmt.Errorf("//kanna:scope takes table= and func=, not %q", field)
		}
	}
	if table == "" {
		return "", "", errors.New("//kanna:scope wants the table its type is a scope of")
	}
	return table, fn, nil
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

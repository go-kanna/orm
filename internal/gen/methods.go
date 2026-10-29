package gen

import (
	"go/types"
	"path/filepath"
	"strings"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/packages"

	"github.com/go-kanna/orm/internal/snapshot"
)

// generated is a method emit writes on a scope type, and what for.
type generated struct {
	name, why string
}

// methodsOf names the methods emit writes on the scope type of p, for a database of dialect d, besides the scopes
// written by hand.
func methodsOf(p scopePlan, d snapshot.Dialect) []generated {
	ms := []generated{{"Of", "for every scope type"}}
	for _, c := range p.columns {
		name := c.path[len(c.path)-1]
		t := c.typ
		if ptr, ok := t.Underlying().(*types.Pointer); ok {
			t = ptr.Elem()
		}
		for _, suffix := range columnMethods(kindOf(t), !c.col.NotNull, d) {
			ms = append(ms, generated{name + suffix, "for the column " + c.col.Name})
		}
	}
	for _, lk := range p.links {
		ms = append(ms, generated{"With" + lk.field.path[len(lk.field.path)-1], "to load " + strings.Join(
			lk.field.path, ".")})
	}
	for _, name := range []string{"Limit", "Offset", "Or", "Not"} {
		ms = append(ms, generated{name, "for every scope type"})
	}
	if len(p.primaryKey) > 0 {
		for _, name := range []string{"values", "After", "Before", "Cursor", "AfterCursor"} {
			ms = append(ms, generated{name, "to continue a page from a row"})
		}
	}
	return ms
}

// columnMethods names the methods of a column of kind k, after the name of its field.
func columnMethods(k kind, nullable bool, d snapshot.Dialect) []string {
	ms := []string{"Eq", "Ne"}
	if !k.array {
		ms = append(ms, "In", "NotIn")
	}
	if k.ordered {
		ms = append(ms, "Lt", "Lte", "Gt", "Gte", "Between")
	}
	if k.text {
		ms = append(ms, "Like")
		if d == snapshot.PostgreSQL {
			ms = append(ms, "ILike")
		}
		ms = append(ms, "HasPrefix", "Contains")
	}
	if nullable {
		ms = append(ms, "IsNull", "IsNotNull")
	}
	return append(ms, "Asc", "Desc")
}

// checkNames reports a name a method kanna-orm writes on a scope type of pkg, one of plans, has already: a method
// written in Go, the scope type's field, or a scope written by hand in a .sql file, named as a method kanna-orm writes
// or as one written in Go. kanna-orm would otherwise write over the one, or write a package that does not compile.
func checkNames(pkg *packages.Package, plans []scopePlan, d snapshot.Dialect) []diag.Diag {
	var ds []diag.Diag
	generatedFiles := packages.GeneratedFiles(pkg)
	for _, p := range plans {
		named := map[string]string{}
		for _, m := range methodsOf(p, d) {
			named[m.name] = m.why
		}
		for _, w := range p.written {
			if why, ok := named[w.name]; ok {
				ds = append(ds, diag.Errorf(w.pos, "-- scope: %s.%s: kanna-orm writes a method of this name, %s",
					p.name, w.name, why).WithHints("name the scope otherwise"))
				continue
			}
			named[w.name] = "for the scope " + w.file + " writes"
		}

		if why, ok := named[p.field]; ok {
			ds = append(ds, diag.Errorf(p.pos, "%s: its field %s is named as a method kanna-orm writes, %s", p.name,
				p.field, why).WithHints("name the field otherwise"))
		}
		obj, ok := pkg.Types.Scope().Lookup(p.name).(*types.TypeName)
		if !ok {
			continue
		}
		t, ok := obj.Type().(*types.Named)
		if !ok {
			continue
		}
		for m := range t.Methods() {
			pos := pkg.Fset.Position(m.Pos())
			if filepath.Base(pos.Filename) == outputName && generatedFiles[pos.Filename] {
				continue
			}
			if why, ok := named[m.Name()]; ok {
				ds = append(ds, diag.Errorf(pos, "%s.%s: kanna-orm writes a method of this name, %s", p.name, m.Name(),
					why).WithHints("name yours otherwise"))
			}
		}
	}
	return ds
}

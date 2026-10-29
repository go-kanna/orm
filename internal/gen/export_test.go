package gen

import (
	"go/types"
	"strings"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/packages"

	"github.com/go-kanna/orm/internal/snapshot"
)

var ColumnName = columnName

// ParseTag is parseTag, with what the tag says spread out.
func ParseTag(tag string) (column string, skip bool, fk string, err error) {
	t, err := parseTag(tag)
	return t.column, t.skip, t.fk, err
}

// Scope is a scope type as findScopes finds it, its model named by the name of its package.
type Scope struct{ Name, Field, Model, Table, Func string }

func FindScopes(pkg *packages.Package, s snapshot.Snapshot) ([]Scope, []diag.Diag) {
	found, ds := findScopes(pkg, s)
	scopes := make([]Scope, 0, len(found))
	for _, sc := range found {
		model := types.TypeString(sc.model, (*types.Package).Name)
		scopes = append(scopes, Scope{Name: sc.name, Field: sc.field, Model: model, Table: sc.table, Func: sc.fn})
	}
	return scopes, ds
}

// Fields matches the fields of the models of the scope types of pkg to their columns, and returns those holding a
// column as "Model.Path type column", then those holding other models as "Model.Path type relation", with the foreign
// key their tag names.
func Fields(pkg *packages.Package, s snapshot.Snapshot) ([]string, []diag.Diag) {
	scopes, ds := findScopes(pkg, s)
	var got []string
	for _, sc := range scopes {
		columns, relations, fds := fields(pkg.Fset, sc, s.Tables[sc.table])
		ds = append(ds, fds...)
		prefix := sc.model.Obj().Name() + "."
		for _, c := range columns {
			got = append(got, prefix+strings.Join(c.path, ".")+" "+types.TypeString(c.typ, (*types.Package).Name)+" "+
				c.col.Name)
		}
		for _, r := range relations {
			f := prefix + strings.Join(r.path, ".") + " " + types.TypeString(r.typ, (*types.Package).Name) + " relation"
			if r.fk != "" {
				f += " fk=" + r.fk
			}
			got = append(got, f)
		}
	}
	return got, ds
}

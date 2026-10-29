package gen

import (
	"go/types"

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

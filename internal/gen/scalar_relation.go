package gen

import (
	"go/types"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"

	"github.com/go-kanna/orm/internal/snapshot"
)

func (l linker) scalarLink(sc scope, columns []column, r relation) (link, []diag.Diag, bool) {
	fail := func(format string, args ...any) (link, []diag.Diag, bool) {
		label := sc.model.Obj().Name() + "." + strings.Join(r.path, ".") + ": "
		return link{}, []diag.Diag{diag.Errorf(r.pos, label+format, args...)}, false
	}
	s, ok := r.typ.Underlying().(*types.Slice)
	if !ok || !holdsColumn(s.Elem()) || holdsModels(r.typ) {
		return fail("a scalar relation requires a slice of column values")
	}
	table, ok := l.tables[r.table]
	if !ok {
		return fail("the snapshot has no table %s", r.table)
	}
	i := slices.IndexFunc(table.Columns, func(c snapshot.Column) bool { return c.Name == r.value })
	if i < 0 {
		return fail("%s has no column %s", r.table, r.value)
	}
	c := table.Columns[i]
	if _, ok := readable(s.Elem(), c.Type); !ok || !c.NotNull && !nilable(s.Elem()) {
		return fail("%s cannot hold %s.%s (%s), including its nullability", typeName(s.Elem()), r.table, r.value, c.Type)
	}
	var ways []snapshot.ForeignKey
	for _, fk := range table.ForeignKeys {
		if fk.References == sc.table && (r.fk == "" || (way{fk: fk, table: r.table}).follows(r.fk)) {
			ways = append(ways, fk)
		}
	}
	if len(ways) == 0 {
		return fail("no matching foreign key relates %s to %s", r.table, sc.table)
	}
	if len(ways) > 1 {
		return fail("multiple foreign keys relate %s to %s; specify fk=<column>", r.table, sc.table)
	}
	fk := ways[0]
	if len(fk.Columns) == 0 || len(fk.Columns) != len(fk.ReferencedColumns) {
		return fail("the foreign key has inconsistent columns")
	}
	keys, missing := held(columns, fk.ReferencedColumns)
	if missing != "" {
		return fail("no field holds %s.%s", sc.table, missing)
	}
	var targetKeys []column
	for i, name := range fk.Columns {
		if !types.Comparable(elemOf(keys[i].typ)) {
			return fail("the relation key cannot be used as a map key")
		}
		j := slices.IndexFunc(table.Columns, func(c snapshot.Column) bool { return c.Name == name })
		if j < 0 {
			return fail("%s has no foreign key column %s", r.table, name)
		}
		if _, ok := readable(elemOf(keys[i].typ), table.Columns[j].Type); !ok {
			return fail("the relation key cannot hold %s.%s", r.table, name)
		}
		targetKeys = append(targetKeys, column{typ: keys[i].typ, col: table.Columns[j]})
	}
	return link{field: r, way: way{kind: hasMany, fk: fk, table: r.table},
		target: indexed{table: r.table}, keys: keys,
		targetKeys: targetKeys,
		scalar:     &column{typ: s.Elem(), col: c}}, nil, true
}

package gen

import (
	"fmt"
	"go/types"
	"slices"
	"strconv"
	"strings"

	"github.com/go-kanna/internal/diag"

	"github.com/go-kanna/orm/internal/decl"
	"github.com/go-kanna/orm/internal/snapshot"
)

// scalarLink links r, a scalar relation of the model of sc, which reads the values of a column of its table, the column
// whose plural its name is, as Topics is of topic, by the foreign key of <model>_id of that table, as record_id of
// Record. Its tag names the column with column=, and the columns of the foreign key with fk=, when they are others.
func (l linker) scalarLink(sc scope, columns []column, r relation) (link, []diag.Diag, bool) {
	field := r.path[len(r.path)-1]
	errorf := func(format string, args ...any) diag.Diag {
		label := sc.model.Obj().Name() + "." + strings.Join(r.path, ".") + ": "
		return diag.Errorf(r.pos, label+format, args...)
	}
	fail := func(format string, args ...any) (link, []diag.Diag, bool) {
		return link{}, []diag.Diag{errorf(format, args...)}, false
	}
	s, many := r.typ.Underlying().(*types.Slice)
	if !many {
		return fail("relation= reads the values of a column of the related rows into a slice; a value picked from " +
			"them is written in SQL")
	}
	valueType := s.Elem()
	if !holdsColumn(valueType) || holdsModels(valueType) {
		return fail("a scalar relation requires column values")
	}
	table, ok := l.tables[r.table]
	if !ok {
		return fail("the snapshot has no table %s", r.table)
	}
	value := r.value
	if value == "" {
		var plurals []string
		for _, c := range table.Columns {
			if pluralOfColumn(field, c.Name) {
				plurals = append(plurals, c.Name)
			}
		}
		switch len(plurals) {
		case 0:
			return fail("%s has no column whose plural is %s", r.table, field)
		case 1:
			value = plurals[0]
		default:
			return link{}, []diag.Diag{errorf("%s are all columns of %s whose plural is %s", strings.Join(plurals, ", "),
				r.table, field).WithHints("name the one to read with column=")}, false
		}
	}
	i := slices.IndexFunc(table.Columns, func(c snapshot.Column) bool { return c.Name == value })
	if i < 0 {
		return fail("%s has no column %s", r.table, value)
	}
	c := table.Columns[i]
	if _, ok := readable(valueType, c.Type); !ok || !c.NotNull && !nilable(valueType) {
		return fail("%s cannot hold %s.%s (%s), including its nullability", typeName(valueType), r.table, value, c.Type)
	}
	keyColumns := r.fk
	if keyColumns == nil {
		keyColumns = []string{columnName(sc.model.Obj().Name()) + "_id"}
	}
	fks := foreignKeys(table, keyColumns, sc.table)
	switch {
	case len(fks) == 0:
		d := errorf("no foreign key of %s to %s reads it", qualifiedColumns(r.table, keyColumns), sc.table)
		for _, fk := range table.ForeignKeys {
			if fk.References == sc.table && len(foreignKeys(table, fk.Columns, sc.table)) == 1 {
				d = d.WithHints(fmt.Sprintf("%s to %s reads it: name its columns with fk=%s",
					qualifiedColumns(r.table, fk.Columns), sc.table, strings.Join(fk.Columns, ",")))
			}
		}
		return link{}, []diag.Diag{d}, false
	case len(fks) > 1:
		return link{}, []diag.Diag{errorf("%s foreign keys of %s refer to %s", count(len(fks)),
			qualifiedColumns(r.table, keyColumns), sc.table).WithHints("read the values by SQL written by hand")}, false
	}
	fk := fks[0]
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
	target := indexed{table: r.table}
	return link{field: r, way: way{kind: hasMany, fk: fk, table: r.table}, target: target, keys: keys,
		targetKeys: targetKeys, scalar: &column{typ: valueType, col: c}}, nil, true
}

// pluralOfColumn reports whether field, which holds the values of a column, is named the plural of column, as pluralOf
// takes that of a field of a model: Topics of topic, or, of a name ending in an acronym, TopicIDs of topic_id.
func pluralOfColumn(field, column string) bool {
	if decl.SnakeCase(field) == decl.Plural(column) {
		return true
	}
	single, ok := strings.CutSuffix(field, "s")
	return ok && endsInAcronym(single) && decl.SnakeCase(single) == column
}

func count(n int) string {
	if words := []string{"two", "three", "four", "five"}; n >= 2 && n-2 < len(words) {
		return words[n-2]
	}
	return strconv.Itoa(n)
}

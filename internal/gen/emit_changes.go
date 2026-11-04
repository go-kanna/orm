package gen

import (
	"go/types"
	"slices"
	"strconv"
	"strings"
)

// changeSetNames name the change set of the scope type named scope, and the function returning an empty one: after
// scope, but for a Scope it ends in, as OrderChangeSet and OrderChanges are of Order and of OrderScope alike.
func changeSetNames(scope string) (set, fn string) {
	if trimmed, ok := strings.CutSuffix(scope, "Scope"); ok && trimmed != "" {
		scope = trimmed
	}
	return scope + "ChangeSet", scope + "Changes"
}

func (e emitter) changes() {
	model := types.TypeString(e.p.model, e.f.Qualifier)
	name, fn := changeSetNames(e.p.name)
	e.printf("// %s holds explicitly assigned columns of %s.\n", name, e.p.table)
	e.printf("type %s struct { changes %s[%s] }\n\n", name, e.ref("Set"), model)
	e.printf("func %s() %s { return %s{} }\n\n", fn, name, name)
	e.printf("func (c %s) Of() %s[%s] { return c.changes }\n\n", name, e.ref("Set"), model)
	for _, col := range changeColumns(e.p) {
		field := col.path[len(col.path)-1]
		typ := types.TypeString(col.typ, e.f.Qualifier)
		value := "v"
		if col.nullzero {
			value = e.f.Import("database/sql", "sql") + ".Null[" + typ + "]{V: v, Valid: v != \"\"}"
		}
		e.printf("func (c %s) Set%s(v %s) %s {\n", name, field, typ, name)
		e.printf("return %s{c.changes.Set(%s, %s)}\n}\n\n", name, strconv.Quote(col.col.Name), value)
	}
}

func changeColumns(p scopePlan) []column {
	return slices.DeleteFunc(slices.Clone(p.columns), func(c column) bool {
		return c.col.Assigned || c.insertOnly || slices.Contains(p.definition.PrimaryKey, c.col.Name)
	})
}

func changeMethods(p scopePlan) []generated {
	methods := make([]generated, 0, len(p.columns)+1)
	methods = append(methods, generated{"Of", "for generated change sets"})
	for _, c := range changeColumns(p) {
		methods = append(methods, generated{"Set" + c.path[len(c.path)-1], "to assign column " + c.col.Name})
	}
	return methods
}

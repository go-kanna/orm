package gen

import (
	"go/types"
	"slices"
	"strconv"
)

func (e emitter) changes() {
	model := types.TypeString(e.p.model, e.f.Qualifier)
	name := e.p.name + "ChangeSet"
	e.printf("// %s holds explicitly assigned columns of %s.\n", name, e.p.table)
	e.printf("type %s struct { changes %s[%s] }\n\n", name, e.ref("Changes"), model)
	e.printf("func %sChanges() %s { return %s{} }\n\n", e.p.name, name, name)
	e.printf("func (c %s) Of() %s[%s] { return c.changes }\n\n", name, e.ref("Changes"), model)
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
		return c.col.Assigned || slices.Contains(p.definition.PrimaryKey, c.col.Name)
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

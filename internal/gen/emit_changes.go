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
	e.printf("type %s struct { changes %s[%s] }\n\n", name, e.ref("Changes"), model)
	e.printf("func %s() %s { return %s{} }\n\n", fn, name, name)
	e.printf("func (c %[1]s) Of() %[2]s[%[3]s] { return %[2]s[%[3]s](c.changes) }\n\n", name, e.ref("Set"), model)
	for _, col := range changeColumns(e.p) {
		field := col.path[len(col.path)-1]
		typ := types.TypeString(col.typ, e.f.Qualifier)
		value := "v"
		if col.nullzero {
			value = e.f.Import("database/sql", "sql") + ".Null[" + typ + "]{V: v, Valid: v != \"\"}"
		}
		e.printf("func (c %s) Set%s(v %s) %s {\n", name, field, typ, name)
		e.printf("return %s{c.changes.Set(%s, %s)}\n}\n\n", name, strconv.Quote(col.col.Name), value)
		if additive(col) {
			e.printf("func (c %s) Add%s(n %s) %s {\n", name, field, typ, name)
			e.printf("return %s{c.changes.Add(%s, n)}\n}\n\n", name, strconv.Quote(col.col.Name))
		}
		if ordered(col) {
			for _, verb := range []string{"Raise", "Lower"} {
				e.printf("func (c %s) %s%s(v %s) %s {\n", name, verb, field, typ, name)
				e.printf("return %s{c.changes.%s(%s, v)}\n}\n\n", name, verb, strconv.Quote(col.col.Name))
			}
		}
	}
}

// changeColumns are the columns of p a change set writes: neither the primary key, those the database assigns, those
// written by an insert alone, nor the audit columns, which the conventions write.
func changeColumns(p scopePlan) []column {
	return slices.DeleteFunc(slices.Clone(p.columns), func(c column) bool {
		return c.col.Assigned || c.insertOnly || c.audit != auditNone ||
			slices.Contains(p.definition.PrimaryKey, c.col.Name)
	})
}

// additive reports whether a change set adds to c as well as setting it: c is a number in Go and in the database, and
// never NULL, as NULL plus a number stays NULL.
func additive(c column) bool {
	return c.col.NotNull && numeric(c.typ, c.col.Type)
}

// ordered reports whether a change set raises and lowers c, writing the greater or the lesser of a value and the stored
// one: c is a number or a time in Go and in the database, and never NULL, which GREATEST and LEAST of MySQL return
// for NULL, and those of PostgreSQL ignore.
func ordered(c column) bool {
	return c.col.NotNull && (numeric(c.typ, c.col.Type) || classOf(c.col.Type) == classTime && timeBased(c.typ))
}

// timeBased reports whether t is time.Time, or a type made of one.
func timeBased(t types.Type) bool {
	if isTime(t) {
		return true
	}
	st, ok := t.Underlying().(*types.Struct)
	if !ok || st.NumFields() == 0 || st.Field(0).Pkg() == nil || st.Field(0).Pkg().Path() != "time" {
		return false
	}
	obj := st.Field(0).Pkg().Scope().Lookup("Time")
	return obj != nil && types.Identical(st, obj.Type().Underlying())
}

// numeric reports whether a column of dbType, held by a t, is a number in Go and in the database: t is an integer or a
// floating-point number, or a type made of one, and dbType a type of numbers.
func numeric(t types.Type, dbType string) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&(types.IsInteger|types.IsFloat) != 0 &&
		slices.Contains([]dbClass{classInteger, classTinyint1, classFloat, classNumeric}, classOf(dbType))
}

func changeMethods(p scopePlan) []generated {
	methods := make([]generated, 0, 4*len(p.columns)+1)
	methods = append(methods, generated{"Of", "for generated change sets"})
	for _, c := range changeColumns(p) {
		field := c.path[len(c.path)-1]
		methods = append(methods, generated{"Set" + field, "to assign column " + c.col.Name})
		if additive(c) {
			methods = append(methods, generated{"Add" + field, "to add to column " + c.col.Name})
		}
		if ordered(c) {
			methods = append(methods, generated{"Raise" + field, "to raise column " + c.col.Name},
				generated{"Lower" + field, "to lower column " + c.col.Name})
		}
	}
	return methods
}

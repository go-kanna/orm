package gen

import (
	"fmt"
	"go/types"
	"strconv"
	"strings"
)

func (e emitter) columnReferences() {
	e.printf("type %sColumns struct {\n", e.p.name)
	for _, c := range e.referenceColumns() {
		e.printf("%s %s[%s]\n", c.name, e.ref("Column"), c.typ)
	}
	e.printf("}\n\nfunc %sColumnsFor(parent %s) %sColumns {\nreturn %sColumns{\n", lowerFirst(e.p.name),
		e.ref("Parent"), e.p.name, e.p.name)
	for _, c := range e.referenceColumns() {
		e.printf("%s: %s[%s](parent, %q),\n", c.name, e.ref("NewColumn"), c.typ, c.column)
	}
	e.printf("}\n}\n\n")
}

type referenceColumn struct{ name, column, typ string }

func (e emitter) referenceColumns() []referenceColumn {
	result := make([]referenceColumn, 0, len(e.p.definition.Columns))
	for _, db := range e.p.definition.Columns {
		var name, typ string
		for _, c := range e.p.columns {
			if c.col.Name == db.Name {
				name = c.path[len(c.path)-1]
				typ = types.TypeString(elemOf(c.typ), e.f.Qualifier)
				break
			}
		}
		if name == "" {
			name, _ = funcName(db.Name)
			if before0, ok := strings.CutSuffix(name, "Id"); ok {
				name = before0 + "ID"
			}
		}
		if typ == "" {
			switch classOf(db.Type) {
			case classInteger:
				if r, ok := intRangeOf(db.Type); ok {
					typ = fmt.Sprintf("int%d", r.bits)
					if r.bits == 24 {
						typ = "int32"
					}
					if !r.signed {
						typ = "u" + typ
					}
				} else {
					typ = "int64"
				}
			case classFloat:
				typ = "float64"
			case classText, classNumeric:
				typ = "string"
			case classBool, classTinyint1:
				typ = "bool"
			case classTime:
				typ = e.f.Import("time", "time") + ".Time"
			case classBytes:
				typ = "[]byte"
			case classUUID, classUnknown:
				continue
			}
		}
		result = append(result, referenceColumn{name, db.Name, typ})
	}
	return result
}
func (e emitter) columnComparison(c column) {
	name := c.path[len(c.path)-1]
	typ := types.TypeString(elemOf(c.typ), e.f.Qualifier)
	for _, m := range []struct{ name, op string }{{"Eq", "="}, {"Ne", "<>"}, {"Lt", "<"}, {"Lte", "<="}, {"Gt",
		">"}, {"Gte", ">="}} {
		if m.name != "Eq" && m.name != "Ne" && !kindOf(elemOf(c.typ)).ordered {
			continue
		}
		e.fn("s", name+m.name+"Column", "v "+e.ref("Column")+"["+typ+"]", e.ref("CompareColumn")+"("+e.q("s")+", "+
			goString(sqlName(e.d, c.col.Name))+", "+strconv.Quote(m.op)+", v)")
	}
}

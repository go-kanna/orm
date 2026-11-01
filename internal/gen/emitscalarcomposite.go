package gen

import (
	"fmt"
	"go/types"
	"strconv"
	"strings"
)

func (e repoEmitter) preloadScalarComposite(lk link, n preloadNames, c compositeNames) {
	valueType := types.TypeString(lk.scalar.typ, e.f.Qualifier)
	field := strings.Join(lk.field.path, ".")
	e.printf("if _, %s := %s.Preloaded[%s](%s, %q); %s {\n", n.ok, e.ormPkg(), valueType, n.q, field, n.ok)
	e.printf("type %s struct{ %s }\n", c.key, e.keyFields(lk.keys))
	e.printf("%s := make([]%s, 0, len(%s))\n", n.keys, c.key, n.rows)
	e.printf("for %s := range %s {\n%s\n}\n", n.i, n.rows,
		keyOf(c.key, c.k, n.rows+"["+n.i+"]", lk.keys, func(k string) string {
			return n.keys + " = append(" + n.keys + ", " + k + ")"
		}))
	e.printf("%s := map[%s][]%s{}\n", n.byKey, c.key, valueType)
	e.printf("for %s := range %s.Chunk(%s, %d) {\n", n.chunk, e.f.Import("slices", "slices"), n.keys,
		max(1, inChunk/len(lk.keys)))
	columns := make([]string, 0, len(lk.targetKeys))
	dbTypes := make([]string, 0, len(lk.targetKeys))
	dests := make([]string, 0, len(lk.targetKeys)+1)
	for i, col := range lk.targetKeys {
		columns = append(columns, sqlName(e.d, col.col.Name))
		dbTypes = append(dbTypes, col.col.Type)
		dests = append(dests, "&"+c.k+".k"+strconv.Itoa(i))
	}
	condition := e.inKeys(columns, dbTypes, n.chunk, lk.keys, c.arrays, c)
	e.printf("var %s %s.Of[%s]\n", n.sub, e.ormPkg(), valueType)
	e.printf("%s = %s.Where(%s).OrderBy(%q, false)\n", n.sub, n.sub, condition, sqlName(e.d, lk.scalar.col.Name))
	e.printf("rd, %s := %s.Render(%s, 1)\nif %s != nil { return %s }\n", n.err, n.sub, e.dialect(), n.err, n.err)
	query := "SELECT " + strings.Join(columns, ", ") + ", " + sqlName(e.d, lk.scalar.col.Name) +
		" FROM " + sqlName(e.d, lk.target.table)
	wrap := e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", "load "+field+": %w") + ", " + n.err + ")"
	e.printf("%s, %s := %s.%s(%s, %q+rd.Where()+rd.OrderBy(), rd.Args()...)\n",
		n.related, n.err, n.db, e.query, n.ctx, query)
	e.printf("if %s != nil { return %s }\n", n.err, wrap)
	e.printf("for %s.Next() {\nvar %s %s\nvar value %s\n", n.related, c.k, c.key, valueType)
	dests = append(dests, "&value")
	e.printf("if %s := %s.Scan(%s); %s != nil { %s.Close(); return %s }\n",
		n.err, n.related, strings.Join(dests, ", "), n.err, n.related, wrap)
	e.printf("%s[%s] = append(%s[%s], value)\n}\n", n.byKey, c.k, n.byKey, c.k)
	e.printf("%s.Close()\nif %s := %s.Err(); %s != nil { return %s }\n}\n", n.related, n.err, n.related, n.err, wrap)
	e.printf("for %s := range %s {\n%s[%s].%s = []%s{}\n%s\n}\n}\n", n.i, n.rows, n.rows, n.i, field, valueType,
		keyOf(c.key, c.k, n.rows+"["+n.i+"]", lk.keys, func(k string) string {
			return "if values, ok := " + n.byKey + "[" + k + "]; ok { " + n.rows + "[" + n.i + "]." + field + " = values }"
		}))
}

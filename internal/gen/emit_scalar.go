package gen

import (
	"fmt"
	"go/types"
	"strings"
)

func (e repoEmitter) preloadScalar(lk link, n preloadNames) {
	own, other := lk.keys[0], lk.targetKeys[0]
	keyType := types.TypeString(elemOf(own.typ), e.f.Qualifier)
	valueType := types.TypeString(lk.scalar.typ, e.f.Qualifier)
	field := strings.Join(lk.field.path, ".")
	e.printf("if _, %s := %s.Preloaded[%s](%s, %q); %s {\n", n.ok, e.ormPkg(), valueType, n.q, field, n.ok)
	e.printf("%s := make([]%s, 0, len(%s))\n", n.keys, keyType, n.rows)
	e.printf("for %s := range %s {\n%s\n}\n", n.i, n.rows,
		ifSet(n.rows+"["+n.i+"]."+fieldPath(own), isPointer(own.typ), func(v string) string {
			return n.keys + " = append(" + n.keys + ", " + v + ")"
		}))
	e.printf("%s := map[%s][]%s{}\n", n.byKey, keyType, valueType)
	// Chunk both drivers so one relation never exceeds the driver's parameter limit.
	e.printf("for %s := range %s.Chunk(%s, %d) {\n", n.chunk, e.f.Import("slices", "slices"), n.keys, inChunk)
	e.printf("var %s %s.Of[%s]\n", n.sub, e.ormPkg(), valueType)
	e.printf("%s = %s.Where(%q, %s.List(%s), %q).OrderBy(%q, false)\n", n.sub, n.sub,
		sqlName(e.d, other.col.Name)+" IN (", e.ormPkg(), n.chunk, ")", sqlName(e.d, lk.scalar.col.Name))
	e.printf("rd, %s := %s.Render(%s, 1)\nif %s != nil { return %s }\n", n.err, n.sub, e.dialect(), n.err, n.err)
	query := "SELECT " + sqlName(e.d, other.col.Name) + ", " + sqlName(e.d, lk.scalar.col.Name) +
		" FROM " + sqlName(e.d, lk.target.table)
	wrap := e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", "load "+field+": %w") + ", " + n.err + ")"
	e.printf("%s, %s := %s.%s(%s, %q+rd.Where()+rd.OrderBy(), rd.Args()...)\n",
		n.related, n.err, n.db, e.query, n.ctx, query)
	e.printf("if %s != nil { return %s }\n", n.err, wrap)
	e.printf("for %s.Next() {\nvar key %s\nvar value %s\n", n.related, keyType, valueType)
	e.printf("if %s := %s.Scan(&key, &value); %s != nil { %s.Close(); return %s }\n",
		n.err, n.related, n.err, n.related, wrap)
	e.printf("%s[key] = append(%s[key], value)\n}\n", n.byKey, n.byKey)
	e.printf("%s.Close()\nif %s := %s.Err(); %s != nil { return %s }\n}\n", n.related, n.err, n.related, n.err, wrap)
	e.printf("for %s := range %s {\n%s[%s].%s = []%s{}\n%s\n}\n}\n", n.i, n.rows, n.rows, n.i, field, valueType,
		ifSet(n.rows+"["+n.i+"]."+fieldPath(own), isPointer(own.typ), func(v string) string {
			return "if values, ok := " + n.byKey + "[" + v + "]; ok { " + n.rows + "[" + n.i + "]." + field + " = values }"
		}))
}

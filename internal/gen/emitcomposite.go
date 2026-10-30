package gen

import (
	"fmt"
	"go/types"
	"slices"
	"strconv"
	"strings"

	"github.com/go-kanna/internal/gofile"

	"github.com/go-kanna/orm/internal/snapshot"
)

// compositeNames are the names, besides those of preloadNames, in the blocks loading relations by keys of more than
// one column: each block declares them anew.
type compositeNames struct {
	key, targetKey, parts, sep, k string
	// arrays and targetArrays name, on PostgreSQL, the array of each field of a key, and of a key of the rows related
	// through a table between.
	arrays, targetArrays []string
}

// newCompositeNames names, in sc, what the blocks loading links declare, for those by keys of more than one column.
func newCompositeNames(sc gofile.Scope, links []link) compositeNames {
	c := compositeNames{
		key: sc.Name("key"), targetKey: sc.Name("targetKey"), parts: sc.Name("parts"), sep: sc.Name("sep"),
		k: sc.Name("k"),
	}
	keys, targetKeys := 0, 0
	for _, lk := range links {
		keys = max(keys, len(lk.keys), len(lk.targetKeys))
		if lk.way.kind == manyToMany {
			targetKeys = max(targetKeys, len(lk.targetKeys))
		}
	}
	for i := range keys {
		c.arrays = append(c.arrays, sc.Name("k"+strconv.Itoa(i)+"s"))
	}
	for i := range targetKeys {
		c.targetArrays = append(c.targetArrays, sc.Name("tk"+strconv.Itoa(i)+"s"))
	}
	return c
}

// preloadComposite writes the block loading lk, a relation by a key of more than one column, as preload does one by a
// key of one: each key a struct of its columns, k0, k1, and so on, and the rows related to some read by those columns
// as a row, (a, b) IN (...).
func (e repoEmitter) preloadComposite(lk link, n preloadNames, c compositeNames) {
	elemType := types.TypeString(lk.model, e.f.Qualifier)
	if s, ok := lk.field.typ.Underlying().(*types.Slice); ok {
		elemType = types.TypeString(s.Elem(), e.f.Qualifier)
	}
	many := lk.way.kind == hasMany || lk.way.kind == manyToMany
	valueType := elemType
	if many {
		valueType = "[]" + elemType
	}

	e.printf("if %[1]s, %[2]s := %[3]s.Preloaded[%[4]s](%[5]s, %[6]q); %[2]s {\n", n.sub, n.ok, e.orm,
		types.TypeString(lk.model, e.f.Qualifier), n.q, strings.Join(lk.field.path, "."))
	e.printf("type %s struct{ %s }\n", c.key, e.keyFields(lk.keys))
	e.printf("%s := make([]%s, 0, len(%s))\n", n.keys, c.key, n.rows)
	add := n.keys + " = append(" + n.keys + ", %s)"
	if lk.way.kind == belongsTo { // the foreign keys of the rows, which may repeat
		e.printf("%s := map[%s]bool{}\n", n.seen, c.key)
		add = "if !" + n.seen + "[%[1]s] {\n" + n.seen + "[%[1]s] = true\n" + n.keys + " = append(" + n.keys + ", %[1]s)\n}"
	}
	e.printf("for %s := range %s {\n%s\n}\n", n.i, n.rows,
		keyOf(c.key, c.k, n.rows+"["+n.i+"]", lk.keys, func(k string) string { return fmt.Sprintf(add, k) }))
	e.printf("%s := map[%s]%s{}\n", n.byKey, c.key, valueType)

	keys := n.keys
	if e.d == snapshot.MySQL {
		e.printf("for %s := range %s.Chunk(%s, %d) {\n", n.chunk, e.slices, n.keys, inChunk)
		keys = n.chunk
	}
	byTarget, targetType := lk.targetKeys, c.key
	if lk.way.kind == manyToMany {
		e.printf("type %s struct{ %s }\n", c.targetKey, e.keyFields(lk.targetKeys))
		e.throughPairsComposite(lk, n, c, keys)
		keys, targetType = n.targetKeys, c.targetKey
	}
	columns := make([]string, 0, len(byTarget))
	dbTypes := make([]string, 0, len(byTarget))
	for _, col := range byTarget {
		columns = append(columns, sqlName(e.d, col.col.Name))
		dbTypes = append(dbTypes, col.col.Type)
	}
	fieldCols, arrays := lk.keys, c.arrays
	if lk.way.kind == manyToMany {
		fieldCols, arrays = lk.targetKeys, c.targetArrays
		if e.d == snapshot.MySQL { // no rows of no keys
			e.printf("if len(%s) > 0 {\n", keys)
		}
	}
	cond := e.inKeys(columns, dbTypes, keys, fieldCols, arrays, c)
	e.printf("%[1]s, %[2]s := select%[3]s(%[4]s, %[5]s, %[6]s.Where(%[7]s))\nif %[2]s != nil {\nreturn %[2]s\n}\n",
		n.related, n.err, e.names[lk.target.table], n.ctx, n.db, n.sub, cond)

	elem := n.related + "[" + n.j + "]"
	if strings.HasPrefix(elemType, "*") {
		elem = "&" + elem
	}
	e.printf("for %s := range %s {\n", n.j, n.related)
	e.printf("%s\n}\n", keyOf(targetType, c.k, n.related+"["+n.j+"]", lk.targetKeys, func(k string) string {
		switch {
		case lk.way.kind == manyToMany:
			return fmt.Sprintf("for _, %[1]s := range %[2]s[%[3]s] {\n%[4]s[%[1]s] = append(%[4]s[%[1]s], %[5]s)\n}",
				n.pk, n.parentsOf, k, n.byKey, elem)
		case many:
			return fmt.Sprintf("%[1]s[%[2]s] = append(%[1]s[%[2]s], %[3]s)", n.byKey, k, elem)
		}
		return fmt.Sprintf("%s[%s] = %s", n.byKey, k, elem)
	}))
	if e.d == snapshot.MySQL && lk.way.kind == manyToMany {
		e.printf("}\n")
	}
	if e.d == snapshot.MySQL {
		e.printf("}\n")
	}

	dest := n.rows + "[" + n.i + "]." + strings.Join(lk.field.path, ".")
	value := n.found
	if !many && isPointer(lk.field.typ) {
		value = "&" + n.found
	}
	e.printf("for %s := range %s {\n", n.i, n.rows)
	if many {
		e.printf("%s = []%s{}\n", dest, elemType)
	}
	e.printf("%s\n}\n}\n", keyOf(c.key, c.k, n.rows+"["+n.i+"]", lk.keys, func(k string) string {
		return "if " + n.found + ", " + n.ok + " := " + n.byKey + "[" + k + "]; " + n.ok + " {\n" + dest + " = " +
			value + "\n}"
	}))
}

// keyFields declares the fields of the struct of a key of columns: k0, k1, and so on, of the types of the columns.
func (e repoEmitter) keyFields(columns []column) string {
	fields := make([]string, 0, len(columns))
	for i, c := range columns {
		fields = append(fields, "k"+strconv.Itoa(i)+" "+types.TypeString(elemOf(c.typ), e.f.Qualifier))
	}
	return strings.Join(fields, "; ")
}

// keyOf is what do says of k, the key, of the struct type keyType, that the fields of row holding columns make,
// guarded, when any of them is a pointer, by a check that each is set.
func keyOf(keyType, k, row string, columns []column, do func(k string) string) string {
	values := make([]string, 0, len(columns))
	var set []string
	for _, c := range columns {
		v := row + "." + fieldPath(c)
		if isPointer(c.typ) {
			set = append(set, v+" != nil")
			v = "*" + v
		}
		values = append(values, v)
	}
	body := k + " := " + keyType + "{" + strings.Join(values, ", ") + "}\n" + do(k)
	if len(set) == 0 {
		return body
	}
	return "if " + strings.Join(set, " && ") + " {\n" + body + "\n}"
}

// inKeys writes what makes the condition that columns, of dbTypes, are those of one of keys, a slice of structs of
// the types of fieldCols, and is the arguments of Where saying it: on PostgreSQL, the fields of the keys as arrays,
// unnested to rows; on MySQL, the keys as rows of placeholders.
func (e repoEmitter) inKeys(columns, dbTypes []string, keys string, fieldCols []column, arrays []string,
	c compositeNames,
) string {
	row := "(" + strings.Join(columns, ", ") + ") IN ("
	n := len(fieldCols)
	if e.d == snapshot.MySQL {
		e.printf("%[1]s := make([]any, 0, 2*%[2]d*len(%[3]s)+1)\n%[4]s := %[5]s\n", c.parts, n, keys, c.sep,
			goString(row+"("))
		fields := make([]string, 0, 2*n)
		for i := range n {
			if i > 0 {
				fields = append(fields, goString(", "))
			}
			fields = append(fields, c.k+".k"+strconv.Itoa(i))
		}
		e.printf("for _, %[1]s := range %[2]s {\n%[3]s = append(%[3]s, %[4]s, %[5]s)\n%[4]s = %[6]s\n}\n", c.k, keys,
			c.parts, c.sep, strings.Join(fields, ", "), goString("), ("))
		e.printf("%[1]s = append(%[1]s, %[2]s)\n", c.parts, goString("))"))
		return c.parts + "..."
	}
	arrays = e.keyArrays(keys, fieldCols, arrays, c)
	parts := []string{goString(row + "SELECT * FROM unnest("), arrays[0]} //nolint:unqueryvet // unnest gives the key
	for i := 1; i < n; i++ {
		parts = append(parts, goString("::"+dbTypes[i-1]+"[], "), arrays[i])
	}
	return strings.Join(append(parts, goString("::"+dbTypes[n-1]+"[]))")), ", ")
}

// keyArrays writes the arrays of the fields of keys, a slice of structs of the types of fieldCols, one for each
// field, named after arrays.
func (e repoEmitter) keyArrays(keys string, fieldCols []column, arrays []string, c compositeNames) []string {
	arrays = arrays[:len(fieldCols)]
	appends := make([]string, 0, len(fieldCols))
	for i, col := range fieldCols {
		a := arrays[i]
		e.printf("%s := make([]%s, 0, len(%s))\n", a, types.TypeString(elemOf(col.typ), e.f.Qualifier), keys)
		appends = append(appends, a+" = append("+a+", "+c.k+".k"+strconv.Itoa(i)+")")
	}
	e.printf("for _, %s := range %s {\n%s\n}\n", c.k, keys, strings.Join(appends, "\n"))
	return arrays
}

// throughPairsComposite writes, as throughPairs does for a key of one column, the query reading from the table a
// many-to-many relation lk goes through the keys of the rows keys holds, structs of c.key, and those of the rows
// related to them, structs of c.targetKey.
func (e repoEmitter) throughPairsComposite(lk link, n preloadNames, c compositeNames, keys string) {
	fk := make([]string, 0, len(lk.way.fk.Columns))
	for _, col := range lk.way.fk.Columns {
		fk = append(fk, sqlName(e.d, col))
	}
	selected := slices.Clone(fk)
	for _, col := range lk.way.throughFK.Columns {
		selected = append(selected, sqlName(e.d, col))
	}
	query := "SELECT " + strings.Join(selected, ", ") + " FROM " + sqlName(e.d, lk.way.table) + " WHERE (" +
		strings.Join(fk, ", ") + ") IN ("
	wrap := e.fmt + ".Errorf(" + fmt.Sprintf("%q", "read "+lk.way.table+": %w") + ", " + n.err + ")"
	if e.d == snapshot.MySQL {
		fields := make([]string, 0, len(lk.keys))
		for i := range lk.keys {
			fields = append(fields, c.k+".k"+strconv.Itoa(i))
		}
		tuple := "(" + strings.TrimSuffix(strings.Repeat("?, ", len(lk.keys)), ", ") + ")"
		e.printf("%[1]s := make([]any, 0, %[2]d*len(%[3]s))\n"+
			"for _, %[4]s := range %[3]s {\n%[1]s = append(%[1]s, %[5]s)\n}\n",
			n.args, len(lk.keys), keys, c.k, strings.Join(fields, ", "))
		e.printf("%[1]s, %[2]s := %[3]s.%[4]s(%[5]s, %[6]s+%[7]s.Repeat(%[8]s, len(%[9]s)-1)+%[10]s, %[11]s...)\n",
			n.pairs, n.err, n.db, e.query, n.ctx, goString(query+tuple), e.f.Import("strings", "strings"),
			goString(", "+tuple), keys, goString(")"), n.args)
	} else {
		arrays := e.keyArrays(keys, lk.keys, c.arrays, c)
		unnest := make([]string, 0, len(lk.keys))
		for i, col := range lk.keys {
			unnest = append(unnest, "$"+strconv.Itoa(i+1)+"::"+col.col.Type+"[]")
		}
		e.printf("%[1]s, %[2]s := %[3]s.%[4]s(%[5]s, %[6]s, %[7]s)\n", n.pairs, n.err, n.db, e.query, n.ctx,
			goString(query+"SELECT * FROM unnest("+strings.Join(unnest, ", ")+"))"), //nolint:unqueryvet // the key
			strings.Join(arrays, ", "))
	}
	e.printf("if %[1]s != nil {\nreturn %[2]s\n}\n", n.err, wrap)
	dests := make([]string, 0, len(lk.keys)+len(lk.targetKeys))
	for i := range lk.keys {
		dests = append(dests, "&"+n.pk+".k"+strconv.Itoa(i))
	}
	for i := range lk.targetKeys {
		dests = append(dests, "&"+n.tk+".k"+strconv.Itoa(i))
	}
	e.printf("%[1]s := map[%[2]s][]%[3]s{}\nvar %[4]s []%[2]s\nfor %[5]s.Next() {\nvar %[6]s %[3]s\nvar %[7]s %[2]s\n",
		n.parentsOf, c.targetKey, c.key, n.targetKeys, n.pairs, n.pk, n.tk)
	e.printf("if %[1]s := %[2]s.Scan(%[3]s); %[1]s != nil {\n%[2]s.Close()\nreturn %[4]s\n}\n", n.err, n.pairs,
		strings.Join(dests, ", "), wrap)
	e.printf("if _, %[1]s := %[2]s[%[3]s]; !%[1]s {\n%[4]s = append(%[4]s, %[3]s)\n}\n"+
		"%[2]s[%[3]s] = append(%[2]s[%[3]s], %[5]s)\n}\n", n.ok, n.parentsOf, n.tk, n.targetKeys, n.pk)
	e.printf("if %[1]s := %[2]s.Err(); %[1]s != nil {\nreturn %[3]s\n}\n", n.err, n.pairs, wrap)
}

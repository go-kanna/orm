package gen

import (
	"go/types"
	"strconv"
	"strings"

	"github.com/go-kanna/orm/internal/snapshot"
)

// preload writes the block loading the relation lk into the rows, when the scope loads it. It numbers the keys of the
// rows, each once, and has the database join them to the related rows: so a row is related to a key as SQL compares
// them, whatever Go's == says of their values, and comes with the number of its key, which hands it to the rows of the
// key. A limit or an offset of the scope keeps a page of the related rows of each key. On MySQL, the keys go in
// chunks, for a statement to take far fewer than the 65,535 placeholders it can.
func (e repoEmitter) preload(lk link, n preloadNames) {
	field := strings.Join(lk.field.path, ".")
	wrap := e.fmtPkg() + ".Errorf(" + strconv.Quote("load "+field+": %w") + ", err)"
	target, scalar := e.models[lk.target.table], lk.scalar != nil
	var row, columns string
	orderKey := "nil"
	if scalar {
		row, columns = types.TypeString(lk.scalar.typ, e.f.Qualifier), columnNamesLiteral([]column{*lk.scalar})
	} else {
		row, columns = types.TypeString(lk.model, e.f.Qualifier), columnNamesLiteral(target.columns)
		orderKey = e.orderKey(target.table)
	}

	e.printf("if %s, %s := %s[%s](%s, %q); %s {\n", n.sub, n.ok, e.ref("Preloaded"), row, n.q, field, n.ok)
	e.printf("type parentKey struct{ %s }\n", e.keyFields(lk.keys))
	e.printf("keys := []parentKey{}\nowners := make([]int, len(%[1]s))\nseen := map[parentKey]int{}\n"+
		"for i := range %[1]s {\nowners[i] = -1\n%[2]s\n}\n", n.rows,
		keyOf("parentKey", "key", n.rows+"[i]", lk.keys, func(k string) string {
			return "at, ok := seen[" + k + "]\nif !ok {\nat = len(keys)\nseen[" + k + "] = at\nkeys = append(keys, " + k +
				")\n}\nowners[i] = at"
		}))
	if scalar {
		e.printf("%[1]s = %[1]s.OrderBy(%[2]q, false)\n", n.sub, sqlName(e.d, lk.scalar.col.Name))
	}
	e.printf("flat, numbers := []%s{}, []int{}\n", row)
	keys, first := "keys", "0"
	if e.d == snapshot.MySQL {
		e.printf("for start := 0; start < len(keys); start += %d {\nchunk := keys[start:min(start+%[1]d, len(keys))]\n",
			max(1, inChunk/len(lk.keys)))
		keys, first = "chunk", "start"
	} else {
		e.printf("if len(keys) > 0 {\n")
	}
	values := make([]string, 0, len(lk.keys))
	for i := range lk.keys {
		values = append(values, "key.k"+strconv.Itoa(i))
	}
	e.printf("inputs := make([][]any, len(%[1]s))\nfor i, key := range %[1]s {\ninputs[i] = []any{%[2]s}\n}\n", keys,
		strings.Join(values, ", "))
	e.printf("text, args, err := %s.RenderEach(%s, %s, %s, %s, inputs, %s)\nif err != nil {\nreturn %s\n}\n", n.sub,
		e.dialect(), e.batch(lk), orderKey, columns, first, wrap)
	e.printf("related, err := %s.%s(%s, text, args...)\nif err != nil {\nreturn %s\n}\n", n.db, e.query, n.ctx,
		wrap)
	e.printf("for related.Next() {\nvar number int\nvar row %s\n", row)
	dests, assign := []string{"&row"}, []string(nil)
	if !scalar {
		dests, assign = e.scanTargets(e.f.Scope(), "row", target.columns)
	}
	e.printf("if err := related.Scan(%s); err != nil {\nrelated.Close()\nreturn %s\n}\n",
		strings.Join(append([]string{"&number"}, dests...), ", "), wrap)
	e.assignScanned(assign)
	e.printf("flat = append(flat, row)\nnumbers = append(numbers, number)\n}\nrelated.Close()\n"+
		"if err := related.Err(); err != nil {\nreturn %s\n}\n}\n", wrap)
	if !scalar && len(target.links) > 0 {
		e.printf("if err := %s(%s, %s, flat, %s); err != nil {\nreturn err\n}\n", e.helpers[lk.target.table].preload,
			n.ctx, n.db, n.sub)
	}
	e.handOut(lk, n, row)
}

// handOut writes what hands the related rows, flat, each the number of its key in numbers, to the rows each key is
// the key of: a slice of them, empty for none, or a pointer to the first, nil for none.
func (e repoEmitter) handOut(lk link, n preloadNames, row string) {
	dest := n.rows + "[i]." + strings.Join(lk.field.path, ".")
	slice, many := lk.field.typ.Underlying().(*types.Slice)
	if !many {
		e.printf("grouped := make([][]%s, len(keys))\nfor i := range flat {\n"+
			"grouped[numbers[i]] = append(grouped[numbers[i]], flat[i])\n}\n"+
			"for i := range %s {\nif owners[i] >= 0 && len(grouped[owners[i]]) > 0 {\nfound := grouped[owners[i]][0]\n"+
			"%s = &found\n}\n}\n}\n", row, n.rows, dest)
		return
	}
	elem, value := types.TypeString(slice.Elem(), e.f.Qualifier), "flat[i]"
	if isPointer(slice.Elem()) {
		value = "&flat[i]"
	}
	e.printf("grouped := make([][]%s, len(keys))\nfor i := range flat {\n"+
		"grouped[numbers[i]] = append(grouped[numbers[i]], %s)\n}\n"+
		"for i := range %s {\n%s = []%s{}\nif owners[i] >= 0 && grouped[owners[i]] != nil {\n%[4]s = grouped[owners[i]]\n}\n"+
		"}\n}\n", elem, value, n.rows, dest, elem)
}

// batch is the impl.Batch literal telling how the rows lk reaches are related to the rows they are loaded for, and on
// PostgreSQL the types of the columns of their keys, which it casts the keys to.
func (e repoEmitter) batch(lk link) string {
	b := e.ref("Batch") + "{Table: " + strconv.Quote(lk.target.table)
	if lk.way.kind == manyToMany {
		b += ", Through: " + strconv.Quote(lk.way.table) + ", ThroughParent: " + namesLiteral(lk.way.fk.Columns) +
			", ThroughChild: " + namesLiteral(lk.way.throughFK.Columns) + ", TargetKeys: " +
			columnNamesLiteral(lk.targetKeys)
	} else {
		b += ", ChildKeys: " + columnNamesLiteral(lk.targetKeys)
	}
	if e.d == snapshot.PostgreSQL {
		types := make([]string, 0, len(lk.keys))
		for _, c := range lk.keys {
			types = append(types, castType(c.col.Type))
		}
		b += ", KeyTypes: " + namesLiteral(types)
	}
	return b + "}"
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

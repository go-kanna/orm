package gen

import (
	"go/types"
	"strconv"
	"strings"

	"github.com/go-kanna/orm/internal/snapshot"
)

// each writes the impl.Each the block of preload reads the rows of lk with, as a variable named name of the file: the
// part of its query that is the same for every read, put together once.
func (e repoEmitter) each(lk link, name string) {
	var columns string
	orderKey := "nil"
	if lk.scalar != nil {
		columns = columnNamesLiteral([]column{*lk.scalar})
	} else {
		target := e.models[lk.target.table]
		columns, orderKey = columnNamesLiteral(target.columns), e.orderKey(target.table)
	}
	e.printf("// %s reads the rows of %s for the keys of rows, as %s loads them.\n", name, lk.target.table,
		strings.Join(lk.field.path, "."))
	e.printf("var %s = %s(%s, %s, %s, %s)\n\n", name, e.ref("NewEach"), e.dialect(), e.batch(lk), orderKey, columns)
}

// preload writes the block loading the relation lk into the rows, when the scope loads it. It numbers the keys of the
// rows, each once, and has the database join them to the related rows: so a row is related to a key as SQL compares
// them, whatever Go's == says of their values, and comes with the number of its key, which hands it to the rows of the
// key. A limit or an offset of the scope keeps a page of the related rows of each key. On MySQL, the keys go in
// chunks, for a statement to take far fewer than the 65,535 placeholders it can.
func (e repoEmitter) preload(lk link, n preloadNames) {
	field := strings.Join(lk.field.path, ".")
	wrap := e.fmtPkg() + ".Errorf(" + strconv.Quote("load "+field+": %w") + ", err)"
	target, scalar := e.models[lk.target.table], lk.scalar != nil
	var row string
	if scalar {
		row = types.TypeString(lk.scalar.typ, e.f.Qualifier)
	} else {
		row = types.TypeString(lk.model, e.f.Qualifier)
	}

	e.printf("if %s, %s := %s[%s](%s, %q); %s {\n", n.sub, n.ok, e.ref("Preloaded"), row, n.q, field, n.ok)
	e.printf("type parentKey struct{ %s }\n", e.keyFields(lk.keys))
	columns, appends := make([]string, 0, len(lk.keys)), make([]string, 0, len(lk.keys))
	for i, c := range lk.keys {
		k := "keys" + strconv.Itoa(i)
		columns = append(columns, k)
		appends = append(appends, k+" = append("+k+", key.k"+strconv.Itoa(i)+")")
		e.printf("%s := make([]%s, 0, len(%s))\n", k, types.TypeString(elemOf(c.typ), e.f.Qualifier), n.rows)
	}
	e.printf("owners := make([]int, len(%[1]s))\nseen := make(map[parentKey]int, len(%[1]s))\n"+
		"for i := range %[1]s {\nowners[i] = -1\n%[2]s\n}\n", n.rows,
		keyOf("parentKey", "key", n.rows+"[i]", lk.keys, func(k string) string {
			return "at, ok := seen[" + k + "]\nif !ok {\nat = len(keys0)\nseen[" + k + "] = at\n" +
				strings.Join(appends, "\n") + "\n}\nowners[i] = at"
		}))
	if scalar {
		e.printf("%[1]s = %[1]s.OrderBy(%[2]q, false)\n", n.sub, sqlName(e.d, lk.scalar.col.Name))
	}
	e.printf("var flat []%s\nvar numbers []int\n", row)
	keys, first := "[]any{"+strings.Join(columns, ", ")+"}", "0"
	if e.d == snapshot.MySQL {
		size := max(1, inChunk/len(lk.keys))
		e.printf("for start := 0; start < len(keys0); start += %d {\nend := min(start+%[1]d, len(keys0))\n", size)
		chunks := make([]string, 0, len(columns))
		for _, k := range columns {
			chunks = append(chunks, k+"[start:end]")
		}
		keys, first = "[]any{"+strings.Join(chunks, ", ")+"}", "start"
	} else {
		e.printf("if len(keys0) > 0 {\n")
	}
	e.printf("text, args, err := %s.RenderEach(%s, %s, %s)\nif err != nil {\nreturn %s\n}\n", n.sub,
		e.eachNames[eachKey(n.table, lk)], keys, first, wrap)
	e.printf("related, err := %s.%s(%s, text, args...)\nif err != nil {\nreturn %s\n}\n", n.db, e.query, n.ctx,
		wrap)
	e.printf("var number int\nvar row %s\n", row)
	dests, assign, reset := []string{"&row"}, []string(nil), "row = none"
	if scalar {
		e.printf("var none %s\n", row)
	} else {
		dests, assign = e.scanTargets(e.f.Scope(), "row", target.columns)
		reset = "row = " + row + "{}"
	}
	e.printf("dest := []any{%s}\nfor related.Next() {\n%s\n", strings.Join(append([]string{"&number"}, dests...), ", "),
		reset)
	e.printf("if err := related.Scan(dest...); err != nil {\nrelated.Close()\nreturn %s\n}\n", wrap)
	e.assignScanned(assign)
	e.printf("flat = append(flat, row)\nnumbers = append(numbers, number)\n}\nrelated.Close()\n"+
		"if err := related.Err(); err != nil {\nreturn %s\n}\n}\n", wrap)
	if !scalar && len(target.links) > 0 {
		e.printf("if err := %s(%s, %s, flat, %s); err != nil {\nreturn err\n}\n", e.helpers[lk.target.table].preload,
			n.ctx, n.db, n.sub)
	}
	e.handOut(lk, n)
}

// eachKey is how the file names the impl.Each of lk, loaded for the rows of table: by the table and the field.
func eachKey(table string, lk link) string { return table + "." + strings.Join(lk.field.path, ".") }

// handOut writes what hands the related rows, flat, each the number of its key in numbers, to the rows each key is
// the key of: a slice of them, empty for none, or a copy of the first, nil for none.
func (e repoEmitter) handOut(lk link, n preloadNames) {
	dest := n.rows + "[i]." + strings.Join(lk.field.path, ".")
	group := e.ref("Group")
	slice, many := lk.field.typ.Underlying().(*types.Slice)
	if many && isPointer(slice.Elem()) {
		group = e.ref("GroupPointers")
	}
	e.printf("grouped := %s(flat, numbers, len(keys0))\nfor i := range %s {\n", group, n.rows)
	if !many {
		e.printf("if owners[i] >= 0 && grouped[owners[i]] != nil {\nfound := grouped[owners[i]][0]\n%s = &found\n}\n}\n}\n",
			dest)
		return
	}
	e.printf("%s = []%s{}\nif owners[i] >= 0 && grouped[owners[i]] != nil {\n%[1]s = grouped[owners[i]]\n}\n}\n}\n",
		dest, types.TypeString(slice.Elem(), e.f.Qualifier))
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

package gen

import (
	"fmt"
	"go/types"
	"slices"
	"strings"

	"github.com/go-kanna/internal/gofile"
)

// aggregateRead writes the rest of the body of m, a Sum, a Min, a Max, or an Avg taking scopes, which reads the
// aggregate of a column of the rows q matches by the conditions alone. keys are the columns of its keys, and args
// their values.
func (e repoEmitter) aggregateRead(m repoMethod, sc gofile.Scope, recv string, sig signature, q string,
	keys []column, args []string,
) {
	zero := e.zeroOf(m.aggregate.typ)
	err, rd := sc.Name("err"), sc.Name("rd")
	e.printf("if %[1]s := %[2]s.ConditionsOnly(%[3]q); %[1]s != nil {\nreturn %[4]s, %[5]s\n}\n", err, q, m.name, zero,
		sig.mapped(err))
	e.printf("%[1]s, %[2]s := %[3]s.Render(%[4]s, 1, nil)\nif %[2]s != nil {\nreturn %[5]s, %[6]s\n}\n", rd, err, q,
		e.dialect(), zero, sig.mapped(err))
	query := goString("SELECT "+aggregateSQL(e.d, *m.aggregate)+" FROM "+sqlName(e.d, m.target.table)) + "+" + rd +
		".Where()"
	e.aggregateRow(m, sc, recv, sig, err, sig.ctx+", "+query+", "+rd+".Args()...", keys, args)
}

// aggregateRow writes the rest of the body of m, which reads an aggregate as the one column of the one row the query
// call runs returns, its error into err: into a value, which is zero for no value of a Sum, and orm.ErrNotFound for
// none of the others, or into a pointer, nil for no value. keys are the columns of its keys, and args their values.
func (e repoEmitter) aggregateRow(m repoMethod, sc gofile.Scope, recv string, sig signature, err, call string,
	keys []column, args []string,
) {
	a := m.aggregate
	typ, zero := types.TypeString(a.typ, e.f.Qualifier), e.zeroOf(a.typ)
	what := strings.ToLower(string(a.fn)) + " " + m.target.table + "." + a.col.col.Name
	v := sc.Name(aggregateWords[a.fn])
	// A value that may be NULL is read through a pointer, whose nil is no value.
	nullable := !isPointer(a.typ) && !a.coalesce
	held := typ
	if nullable {
		held = "*" + typ
	}
	e.printf("var %s %s\n", v, held)
	e.printf("if %[1]s := %[2]s.db.%[3]s(%[4]s).Scan(&%[5]s); %[1]s != nil {\nreturn %[6]s, %[7]s\n}\n", err, recv,
		e.queryRow, call, v, zero, sig.mapped(e.fmtPkg()+".Errorf("+goString(what+": %w")+", "+err+")"))
	if !nullable {
		e.printf("return %s, nil\n", v)
		return
	}
	values := e.ref("ErrNotFound")
	if len(keys) > 0 {
		format, shown := e.keyValues(keys, args)
		what += " with " + format
		values = shown + ", " + values
	}
	notFound := fmt.Sprintf("%s.Errorf(%q, %s)", e.fmtPkg(), what+": %w", values)
	e.printf("if %s == nil {\nreturn %s, %s\n}\nreturn *%s, nil\n", v, zero, sig.mapped(notFound), v)
}

// groupedRead writes the rest of the body of m, a List of a projection of aggregates, which groups the rows q matches
// by the conditions alone by the columns of the other fields, and reads the aggregates of each group.
func (e repoEmitter) groupedRead(m repoMethod, sc gofile.Scope, recv string, sig signature, q string) {
	p := m.projection
	typ := types.TypeString(p.model, e.f.Qualifier)
	err, rd, rows, out, row := sc.Name("err"), sc.Name("rd"), sc.Name("rows"), sc.Name("out"), sc.Name("row")
	e.printf("if %[1]s := %[2]s.ConditionsOnly(%[3]q); %[1]s != nil {\nreturn nil, %[4]s\n}\n", err, q, m.name,
		sig.mapped(err))
	e.printf("%[1]s, %[2]s := %[3]s.Render(%[4]s, 1, nil)\nif %[2]s != nil {\nreturn nil, %[5]s\n}\n", rd, err, q,
		e.dialect(), sig.mapped(err))
	keys := make([]string, 0, len(p.columns))
	for _, c := range p.columns {
		keys = append(keys, sqlName(e.d, c.col.Name))
	}
	selected, scanned := slices.Clone(keys), slices.Clone(p.columns)
	for _, a := range p.aggregates {
		selected = append(selected, aggregateSQL(e.d, a))
		scanned = append(scanned, column{path: []string{a.field}, typ: a.typ})
	}
	query := goString("SELECT "+strings.Join(selected, ", ")+" FROM "+sqlName(e.d, m.target.table)) + "+" + rd +
		".Where()+" + goString(" GROUP BY "+strings.Join(keys, ", "))
	wrap := sig.mapped(e.fmtPkg() + ".Errorf(" + goString("group "+m.target.table+": %w") + ", " + err + ")")
	e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s, %[6]s, %[7]s.Args()...)\nif %[2]s != nil {\nreturn nil, %[8]s\n}\n"+
		"defer %[1]s.Close()\n", rows, err, recv, e.query, sig.ctx, query, rd, wrap)
	e.scanRows(scanNames{rows: rows, out: out, row: row, err: err, wrap: wrap}, typ, scanned, sc)
	e.printf("return %s, nil\n", out)
}

// zeroOf is the zero value of t, an aggregate's type, as the file writes it.
func (e repoEmitter) zeroOf(t types.Type) string {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsNumeric != 0:
			return "0"
		case u.Info()&types.IsString != 0:
			return `""`
		}
	case *types.Struct:
		return types.TypeString(t, e.f.Qualifier) + "{}"
	}
	return "nil"
}

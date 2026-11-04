package gen

import (
	"fmt"
	"go/types"
	"strconv"
	"strings"

	"github.com/go-kanna/internal/gofile"

	"github.com/go-kanna/orm/internal/snapshot"
)

// scopedQuery writes the body of m, a method a query written by hand implements that takes scopes: the query named in
// a WITH, and its result read as the rows of m's table, which the scopes narrow, order, and page, with the relations
// they load. The query's parameters come first, then those of the scopes.
func (e repoEmitter) scopedQuery(m repoMethod, sc gofile.Scope, recv string, sig signature) {
	model, row := types.TypeString(m.model, e.f.Qualifier), types.TypeString(m.query.row, e.f.Qualifier)
	verb, _ := verbOf(m.name)
	q, qs, i, x := sc.Name("q"), sc.Name("qs"), sc.Name("i"), sc.Name("x")
	rd, err, rows, out, text, args := sc.Name("rd"), sc.Name("err"), sc.Name("rows"), sc.Name("out"), sc.Name("text"),
		sc.Name("args")
	zero := "nil"
	if verb == verbGet || verb == verbFirst {
		zero = row + "{}"
	}
	e.printf("%s := make([]%s[%s], len(%s))\n", qs, e.ref("Of"), model, sig.scopes)
	e.printf("for %[1]s, %[2]s := range %[3]s {\n%[4]s[%[1]s] = %[5]s[%[6]s](%[2]s.Of())\n}\n", i, x, sig.scopes, qs,
		e.ref("Of"), model)
	e.printf("%s := %s(%s...)\n", q, e.ref("Combine"), qs)
	switch verb { //nolint:exhaustive // queryScope takes these alone
	case verbFirst:
		e.printf("%[1]s = %[1]s.DefaultOrder(%[2]s).Limit(1)\n", q, e.orderKey(m.table))
	case verbGet, verbFind:
		e.printf("if %[1]s := %[2]s.Unpaged(%[3]q); %[1]s != nil {\nreturn %[4]s, %[5]s\n}\n", err, q, m.name, zero,
			sig.mapped(err))
	}

	values := make([]string, 0, len(m.query.args))
	for i, a := range m.query.args {
		value := sig.params[a.parameter]
		if a.field != nil {
			value = e.storageValue(*a.field, value+"."+fieldPath(*a.field))
		}
		if m.query.pieces != nil && expands(a.typ) {
			value = e.ref(listOf(m.query.pieces[i], m.query.pieces[i+1])) + "(" + value + ")"
		}
		values = append(values, value)
	}
	columns := make([]string, 0, len(m.query.fields))
	for _, f := range m.query.fields {
		columns = append(columns, sqlName(e.d, m.target.table)+"."+sqlName(e.d, f.col.Name))
	}
	cte := sqlName(e.d, m.query.cte)
	head, tail := "WITH "+cte+" AS (", ")\nSELECT "+strings.Join(columns, ", ")+" FROM "+cte+" AS "+
		sqlName(e.d, m.target.table)
	if m.query.pieces == nil {
		e.printf("const %s = %s\n%s := []any{%s}\n", text, goString(head+m.query.sql+tail), args,
			strings.Join(values, ", "))
	} else {
		pieces := make([]string, 0, len(m.query.pieces))
		for _, p := range m.query.pieces {
			pieces = append(pieces, goString(p))
		}
		inner := sc.Name("inner")
		e.printf("%s, %s := %s([]string{%s}, %s)\n%s := %s+%s+%s\n", inner, args, e.ref("Expand"),
			strings.Join(pieces, ", "), strings.Join(values, ", "), text, goString(head), inner, goString(tail))
	}
	first := 1
	if e.d == snapshot.PostgreSQL {
		first = len(m.query.args) + 1
	}
	e.printf("%[1]s, %[2]s := %[3]s.Render(%[4]s, %[5]s, %[6]s)\nif %[2]s != nil {\nreturn %[7]s, %[8]s\n}\n", rd, err,
		q, e.dialect(), strconv.Itoa(first), e.orderKey(m.table), zero, sig.mapped(err))
	wrap := sig.mapped(e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", "query "+m.name+": %w") + ", " + err + ")")
	e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s, %[6]s+%[7]s.Where()+%[7]s.OrderBy()+%[7]s.Limit(), "+
		"append(%[8]s, %[7]s.Args()...)...)\n", rows, err, recv, e.query, sig.ctx, text, rd, args)
	e.printf("if %s != nil {\nreturn %s, %s\n}\ndefer %s.Close()\n", err, zero, wrap, rows)
	e.scanRows(scanNames{rows: rows, out: out, row: sc.Name("row"), err: err, wrap: wrap, zero: zero}, row,
		m.query.fields, sc)
	e.printf("if %s.Reversed() {\n%s.Reverse(%s)\n}\n", rd, e.f.Import("slices", "slices"), out)
	if rm := e.models[m.target.table]; len(rm.links) > 0 {
		preload := e.helpers[m.target.table].preload
		if field := embedded(m.query.row, m.model); field == "" {
			e.printf("if %[1]s := %[2]s(%[3]s, %[4]s.db, %[5]s, %[6]s); %[1]s != nil {\nreturn %[7]s, %[8]s\n}\n", err,
				preload, sig.ctx, recv, out, q, zero, sig.mapped(err))
		} else {
			// The relations are loaded into the rows of the model each holds, and those put back.
			rowsOf := sc.Name(lowerFirst(field) + "s")
			e.printf("%[1]s := make([]%[2]s, len(%[3]s))\nfor %[4]s := range %[3]s {\n%[1]s[%[4]s] = %[3]s[%[4]s].%[5]s\n}\n",
				rowsOf, model, out, i, field)
			e.printf("if %[1]s := %[2]s(%[3]s, %[4]s.db, %[5]s, %[6]s); %[1]s != nil {\nreturn %[7]s, %[8]s\n}\n", err,
				preload, sig.ctx, recv, rowsOf, q, zero, sig.mapped(err))
			e.printf("for %[1]s := range %[2]s {\n%[2]s[%[1]s].%[3]s = %[4]s[%[1]s]\n}\n", i, out, field, rowsOf)
		}
	}
	switch verb { //nolint:exhaustive // queryScope takes these alone
	case verbList:
		e.printf("return %s, nil\n", out)
	case verbFind:
		e.printf("if len(%[1]s) == 0 {\nreturn nil, nil\n}\nreturn &%[1]s[0], nil\n", out)
	default:
		e.printf("if len(%[1]s) == 0 {\nreturn %[2]s, %[3]s\n}\nreturn %[1]s[0], nil\n", out, zero,
			sig.mapped(e.fmtPkg()+".Errorf("+fmt.Sprintf("%q", "query "+m.name+": %w")+", "+e.ref("ErrNotFound")+")"))
	}
}

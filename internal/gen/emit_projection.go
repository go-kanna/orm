package gen

import (
	"fmt"
	"go/types"

	"github.com/go-kanna/internal/gofile"
)

func (e repoEmitter) projectedRead(m repoMethod, sc gofile.Scope, recv string, sig signature, q, notFound string) {
	typ := types.TypeString(m.projection.model, e.f.Qualifier)
	read := sc.Name("read")
	if m.result == resultRow {
		e.printf("%s := func() ([]%s, error) {\n", read, typ)
	}
	zero := "nil"
	err, rd := sc.Name("err"), sc.Name("rd")
	e.printf("if %s := %s.ProjectionOnly(%q); %s != nil { return %s, %s }\n", err, q, m.name, err, zero, err)
	if m.verb == verbFirst {
		e.printf("%s = %s.Limit(1)\n", q, q)
	}
	e.printf("%s, %s := %s.Render(%s, 1)\nif %s != nil { return %s, %s }\n",
		rd, err, q, e.dialect(), err, zero, err)
	rows, out, row := sc.Name("rows"), sc.Name("out"), sc.Name("row")
	query := "SELECT " + e.columnList(m.projection.columns) + " FROM " + sqlName(e.d, m.target.table)
	e.printf("%s, %s := %s.executor.%s(%s, %s+%s.Where()+%s.OrderBy()+%s.Limit()+%s, %s.Args()...)\n",
		rows, err, recv, e.query, sig.ctx, goString(query), rd, rd, rd, lockClause(m), rd)
	wrap := e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", "project "+m.target.table+": %w") + ", " + err + ")"
	e.printf("if %s != nil { return %s, %s }\ndefer %s.Close()\n", err, zero, wrap, rows)
	e.scanRows(scanNames{rows: rows, out: out, row: row, err: err, wrap: wrap}, typ, m.projection.columns, sc)
	e.printf("if %s.Reversed() { %s.Reverse(%s) }\n", rd, e.f.Import("slices", "slices"), out)
	if m.result == resultRows {
		e.printf("return %s, nil\n", out)
		return
	}
	e.printf("return %s, nil\n}\n", out)
	if !m.optional {
		zero = typ + "{}"
	}
	e.printf("%s, %s := %s()\nif %s != nil { return %s, %s }\n", out, err, read, err, zero, err)
	if m.optional {
		e.printf("if len(%s) == 0 { return nil, nil }\nreturn &%s[0], nil\n", out, out)
		return
	}
	e.printf("if len(%s) == 0 { return %s, %s }\nreturn %s[0], nil\n", out, zero, notFound, out)
}

package gen

import (
	"fmt"
	"go/types"
	"slices"
	"strings"

	"github.com/go-kanna/internal/gofile"
)

func (e repoEmitter) projectedRead(m repoMethod, sc gofile.Scope, recv string, sig signature, q string,
	notFound func() string,
) {
	typ := types.TypeString(m.projection.model, e.f.Qualifier)
	read := sc.Name("read")
	if m.returns == returnsRow {
		e.printf("%s := func() ([]%s, error) {\n", read, typ)
	}
	// The errors of a row read go through mapError once read returns them.
	mapped := sig.mapped
	if m.returns == returnsRow {
		mapped = func(err string) string { return err }
	}
	zero := "nil"
	err, text, args, reversed := sc.Name("err"), sc.Name("text"), sc.Name("args"), sc.Name("reversed")
	e.printf("if %s := %s.ProjectionOnly(%q); %s != nil { return %s, %s }\n", err, q, m.name, err, zero, mapped(err))
	if m.verb == verbFirst {
		e.printf("if %[1]s := %[2]s.Forward(%[3]q); %[1]s != nil { return %[4]s, %[5]s }\n", err, q, m.name, zero,
			mapped(err))
		e.printf("%[1]s = %[1]s.DefaultOrder(%[2]s).Limit(1)\n", q, e.orderKey(m.table))
	}
	if m.verb == verbGet || m.verb == verbFind {
		e.printf("if %[1]s := %[2]s.Unpaged(%[3]q); %[1]s != nil { return %[4]s, %[5]s }\n", err, q, m.name, zero,
			mapped(err))
	}
	rows, out, row := sc.Name("rows"), sc.Name("out"), sc.Name("row")
	if len(m.projection.joins) == 0 {
		plain := "SELECT " + e.columnList(m.projection.columns) + " FROM " + sqlName(e.d, m.target.table)
		e.printf("%s, %s, %s, %s := %s.RenderProjection(%s, %s, %s)\n", text, args, reversed, err, q, e.dialect(),
			goString(plain), e.orderKey(m.table))
	} else {
		selected, joins, needed := e.projectionJoins(m)
		e.printf("%s, %s, %s, %s := %s.RenderJoinedProjection(%s, %q, %s, %s, %s, %s, %s)\n", text, args, reversed,
			err, q, e.dialect(), m.target.table, goString(selected), goString(joins), namesLiteral(needed),
			namesLiteral(tableColumnNames(m)), e.orderKey(m.table))
	}
	e.printf("if %s != nil { return %s, %s }\n", err, zero, mapped(err))
	e.printf("%s, %s := %s.db.%s(%s, %s+%s, %s...)\n",
		rows, err, recv, e.query, sig.ctx, text, lockClause(m), args)
	wrap := mapped(e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", "project "+m.target.table+": %w") + ", " + err + ")")
	e.printf("if %s != nil { return %s, %s }\ndefer %s.Close()\n", err, zero, wrap, rows)
	if len(m.projection.joins) == 0 {
		e.scanRows(scanNames{rows: rows, out: out, row: row, err: err, wrap: wrap}, typ, m.projection.columns, sc)
	} else {
		e.scanJoinedRows(scanNames{rows: rows, out: out, row: row, err: err, wrap: wrap}, typ, m.projection, sc)
	}
	e.printf("if %s { %s.Reverse(%s) }\n", reversed, e.f.Import("slices", "slices"), out)
	if m.returns == returnsRows {
		e.printf("return %s, nil\n", out)
		return
	}
	e.printf("return %s, nil\n}\n", out)
	if !m.optional {
		zero = typ + "{}"
	}
	e.printf("%s, %s := %s()\nif %s != nil { return %s, %s }\n", out, err, read, err, zero, sig.mapped(err))
	if m.optional {
		e.printf("if len(%s) == 0 { return nil, nil }\nreturn &%s[0], nil\n", out, out)
		return
	}
	e.printf("if len(%s) == 0 { return %s, %s }\nreturn %s[0], nil\n", out, zero, notFound(), out)
}

// projectionJoins writes the read of the projection of m, which joins tables: selected, the SELECT of its columns,
// those of the scope's table by its name, and those of each table joined by its alias, after whether a row joins, for
// a LEFT JOIN; joins, the JOIN clauses; and needed, the columns of the scope's table that the two read.
func (e repoEmitter) projectionJoins(m repoMethod) (selected, joins string, needed []string) {
	table := sqlName(e.d, m.target.table)
	names := make([]string, 0, len(m.projection.columns))
	need := func(name string) {
		if !slices.Contains(needed, name) {
			needed = append(needed, name)
		}
	}
	for _, c := range m.projection.columns {
		names = append(names, table+"."+sqlName(e.d, c.col.Name))
		need(c.col.Name)
	}
	clauses := make([]string, 0, len(m.projection.joins))
	for _, j := range m.projection.joins {
		alias := sqlName(e.d, j.alias)
		if j.left {
			names = append(names, alias+"."+sqlName(e.d, j.fk.ReferencedColumns[0])+" IS NOT NULL")
		}
		for _, c := range j.columns {
			names = append(names, alias+"."+sqlName(e.d, c.col.Name))
		}
		on := make([]string, 0, len(j.fk.Columns))
		for i, name := range j.fk.Columns {
			on = append(on, alias+"."+sqlName(e.d, j.fk.ReferencedColumns[i])+" = "+table+"."+sqlName(e.d, name))
			need(name)
		}
		kind := "INNER JOIN"
		if j.left {
			kind = "LEFT JOIN"
		}
		clauses = append(clauses, fmt.Sprintf(" %s %s AS %s ON %s", kind, sqlName(e.d, j.fk.References), alias,
			strings.Join(on, " AND ")))
	}
	return "SELECT " + strings.Join(names, ", "), strings.Join(clauses, ""), needed
}

// scanJoinedRows writes the loop reading the rows of p, a projection joining tables, into typ. The columns of a table
// a LEFT JOIN joins are read apart, and make the struct holding them only when a row joins.
func (e repoEmitter) scanJoinedRows(n scanNames, typ string, p *readProjection, sc gofile.Scope) {
	e.printf("%[1]s := []%[2]s{}\nfor %[3]s.Next() {\nvar %[4]s %[2]s\n", n.out, typ, n.rows, n.row)
	targets, assignments := e.scanTargets(sc, n.row, p.columns)
	var joined []string
	for _, j := range p.joins {
		if !j.left {
			t, a := e.scanTargets(sc, n.row, j.columns)
			targets, assignments = append(targets, t...), append(assignments, a...)
			continue
		}
		found := sc.Name(lowerFirst(j.field) + "Joined")
		e.printf("var %s bool\n", found)
		targets = append(targets, "&"+found)
		values := make([]string, 0, len(j.columns))
		for _, c := range j.columns {
			field := c.path[len(c.path)-1]
			held, v := types.TypeString(c.typ, e.f.Qualifier), ""
			if !nilable(c.typ) {
				held, v = e.f.Import("database/sql", "sql")+".Null["+held+"]", ".V"
			}
			local := sc.Name(lowerFirst(j.field) + field)
			e.printf("var %s %s\n", local, held)
			targets = append(targets, "&"+local)
			values = append(values, field+": "+local+v)
		}
		joined = append(joined, fmt.Sprintf("if %s {\n%s.%s = &%s{%s}\n}", found, n.row, j.field,
			types.TypeString(elemOf(j.typ), e.f.Qualifier), strings.Join(values, ", ")))
	}
	e.printf("if %[1]s := %[2]s.Scan(%[3]s); %[1]s != nil {\nreturn nil, %[4]s\n}\n", n.err, n.rows,
		strings.Join(targets, ", "), n.wrap)
	e.assignScanned(append(assignments, joined...))
	e.printf("%[1]s = append(%[1]s, %[2]s)\n}\n", n.out, n.row)
	e.printf("if %[1]s := %[2]s.Err(); %[1]s != nil {\nreturn nil, %[3]s\n}\n", n.err, n.rows, n.wrap)
}

func tableColumnNames(m repoMethod) []string {
	names := make([]string, 0, len(m.table.Columns))
	for _, c := range m.table.Columns {
		names = append(names, c.Name)
	}
	return names
}

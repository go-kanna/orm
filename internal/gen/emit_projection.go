package gen

import (
	"fmt"
	"go/types"
	"maps"
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
		e.printf("%[1]s = %[1]s.DefaultOrder(%[2]s).Limit(1)\n", q, e.orderKey(m.table))
	}
	if m.verb == verbGet || m.verb == verbFind {
		e.printf("if %[1]s := %[2]s.Unpaged(%[3]q); %[1]s != nil { return %[4]s, %[5]s }\n", err, q, m.name, zero,
			mapped(err))
	}
	rows, out, row := sc.Name("rows"), sc.Name("out"), sc.Name("row")
	query := "SELECT " + e.columnList(m.projection.columns) + " FROM " + sqlName(e.d, m.target.table)
	e.printf("%s, %s, %s, %s := %s.RenderProjection(%s, %q, %s, %s, %s, %s, %s)\n",
		text, args, reversed, err, q, e.dialect(), m.target.table, e.projectionSources(m), e.projectionJoins(m),
		namesLiteral(tableColumnNames(m)), goString(query), e.orderKey(m.table))
	e.printf("if %s != nil { return %s, %s }\n", err, zero, mapped(err))
	e.printf("%s, %s := %s.db.%s(%s, %s+%s, %s...)\n",
		rows, err, recv, e.query, sig.ctx, text, lockClause(m), args)
	wrap := mapped(e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", "project "+m.target.table+": %w") + ", " + err + ")")
	e.printf("if %s != nil { return %s, %s }\ndefer %s.Close()\n", err, zero, wrap, rows)
	e.scanRows(scanNames{rows: rows, out: out, row: row, err: err, wrap: wrap}, typ, m.projection.columns, sc)
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

func (e repoEmitter) projectionSources(m repoMethod) string {
	parts := make([]string, 0, len(m.projection.sources))
	for _, s := range m.projection.sources {
		parts = append(parts, fmt.Sprintf("{Table:%q,Column:%q}", s.table, s.column.col.Name))
	}
	return "[]" + e.ref("SelectColumn") + "{" + strings.Join(parts, ",") + "}"
}

func (e repoEmitter) projectionJoins(m repoMethod) string {
	var parts []string
	for _, name := range slices.Sorted(maps.Keys(m.projection.tables)) {
		if name == m.target.table {
			continue
		}
		var fields []string
		for _, c := range m.projection.tables[name].Columns {
			fields = append(fields, c.Name)
		}
		found := false
		for _, fk := range m.table.ForeignKeys {
			if fk.References != name {
				continue
			}
			found = true
			parts = append(parts, fmt.Sprintf("{Table:%q,ParentColumns:%s,Columns:%s,Fields:%s}",
				name, namesLiteral(fk.Columns), namesLiteral(fk.ReferencedColumns), namesLiteral(fields)))
		}
		if !found {
			parts = append(parts, fmt.Sprintf("{Table:%q,Fields:%s}", name, namesLiteral(fields)))
		}
	}
	return "[]" + e.ref("TableJoin") + "{" + strings.Join(parts, ",") + "}"
}

func tableColumnNames(m repoMethod) []string {
	names := make([]string, 0, len(m.table.Columns))
	for _, c := range m.table.Columns {
		names = append(names, c.Name)
	}
	return names
}

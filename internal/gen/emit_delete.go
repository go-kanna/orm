package gen

import (
	"go/types"
	"slices"

	"github.com/go-kanna/internal/gofile"

	"github.com/go-kanna/orm/internal/snapshot"
)

func (e repoEmitter) deleteReturning(m repoMethod, sc gofile.Scope, recv string, sig signature, q string) {
	model, columns := m.model, m.columns
	if m.projection != nil {
		model, columns = m.projection.model, m.projection.columns
	}
	typ := types.TypeString(model, e.f.Qualifier)
	text, args, err := sc.Name("text"), sc.Name("args"), sc.Name("err")
	e.printf("%s, %s, %s := %s.RenderDelete(%s, %q, %s)\n",
		text, args, err, q, e.dialect(), m.target.table, namesLiteral(deleteKey(m.table)))
	e.printf("if %s != nil { return nil, %s }\n", err, sig.mapped(err))
	rows, out, row := sc.Name("rows"), sc.Name("out"), sc.Name("row")
	wrap := sig.mapped(e.fmtPkg() + ".Errorf(" + goString("delete from "+m.target.table+": %w") + ", " + err + ")")
	e.printf("%s, %s := %s.db.%s(%s, %s+%s, %s...)\n",
		rows, err, recv, e.query, sig.ctx, text, goString(" RETURNING "+e.columnList(columns)), args)
	e.printf("if %s != nil { return nil, %s }\ndefer %s.Close()\n", err, wrap, rows)
	e.scanRows(scanNames{rows: rows, out: out, row: row, err: err, wrap: wrap}, typ, columns, sc)
	e.printf("return %s, nil\n", out)
}

func (e repoEmitter) deleteSelected(m repoMethod, sc gofile.Scope, recv string, sig signature, q string) {
	text, args, err := sc.Name("text"), sc.Name("args"), sc.Name("err")
	e.printf("%s, %s, %s := %s.RenderDelete(%s, %q, %s)\n",
		text, args, err, q, e.dialect(), m.target.table, namesLiteral(deleteKey(m.table)))
	e.printf("if %s != nil { return 0, %s }\n", err, sig.mapped(err))
	res := e.execResult(sc)
	wrap := sig.mapped(e.fmtPkg() + ".Errorf(" + goString("delete from "+m.target.table+": %w") + ", " + err + ")")
	e.printf("%s, %s := %s.db.%s(%s, %s, %s...)\n", res, err, recv, e.exec, sig.ctx, text, args)
	e.printf("if %s != nil { return 0, %s }\n", err, wrap)
	e.printf("return %s, nil\n", e.rowsAffected(sc, res, err, "0, ", wrap))
}

func deleteKey(table snapshot.Table) []string {
	if len(table.PrimaryKey) > 0 {
		return table.PrimaryKey
	}
	for _, key := range table.UniqueKeys {
		if len(key) > 0 && !slices.ContainsFunc(key, func(name string) bool {
			i := slices.IndexFunc(table.Columns, func(c snapshot.Column) bool { return c.Name == name })
			return i < 0 || !table.Columns[i].NotNull
		}) {
			return key
		}
	}
	return nil
}

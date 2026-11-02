package gen

import (
	"slices"

	"github.com/go-kanna/internal/gofile"

	"github.com/go-kanna/orm/internal/snapshot"
)

func (e repoEmitter) deleteSelected(m repoMethod, sc gofile.Scope, recv string, sig signature, q string) {
	text, args, err := sc.Name("text"), sc.Name("args"), sc.Name("err")
	e.printf("%s, %s, %s := %s.RenderDelete(%s, %q, %s)\n",
		text, args, err, q, e.dialect(), m.target.table, namesLiteral(deleteKey(m.table)))
	e.printf("if %s != nil { return 0, %s }\n", err, err)
	res := e.execResult(sc)
	wrap := e.fmtPkg() + ".Errorf(" + goString("delete from "+m.target.table+": %w") + ", " + err + ")"
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

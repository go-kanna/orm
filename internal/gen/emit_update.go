package gen

import (
	"go/types"
	"strings"

	"github.com/go-kanna/internal/gofile"

	"github.com/go-kanna/orm/internal/snapshot"
)

func (e repoEmitter) updateChanges(m repoMethod, sc gofile.Scope, recv string, sig signature) {
	model := types.TypeString(m.model, e.f.Qualifier)
	zero := ""
	switch m.result {
	case resultNone:
	case resultRow:
		zero = model + "{}, "
	case resultRows:
		zero = "nil, "
	case resultCount:
		zero = "0, "
	case resultBool, resultScalar:
		panic("unsupported Update result")
	}
	q, changes := sc.Name("q"), sc.Name("changes")
	if m.scoped {
		qs := sc.Name("qs")
		e.printf("%s := make([]%s[%s], len(%s))\n", qs, e.ref("Of"), model, sig.scopes)
		e.printf("for i, s := range %s { %s[i] = s.Of() }\n", sig.scopes, qs)
		e.printf("%s := %s(%s...)\n", q, e.ref("Combine"), qs)
	} else {
		e.printf("var %s %s[%s]\n", q, e.ref("Of"), model)
	}
	if m.changes != "" {
		e.printf("%s := %s.Of()\n", changes, sig.params[m.changes])
		for _, k := range m.keys {
			e.printf("%s = %s.Where(%s, %s)\n", q, q,
				goString(sqlName(e.d, k.col.col.Name)+" = "), sig.params[k.param])
		}
	} else {
		e.printf("var %s %s[%s]\n", changes, e.ref("Changes"), model)
		for _, c := range m.updated() {
			e.printf("%s = %s.Set(%q, %s)\n", changes, changes, c.col.Name,
				e.storageValue(c, sig.row+"."+fieldPath(c)))
		}
		for _, key := range m.rowKey() {
			e.printf("%s = %s.Where(%s, %s.%s)\n", q, q,
				goString(sqlName(e.d, key)+" = "), sig.row, fieldPath(m.field(key)))
		}
	}
	text, args, err := sc.Name("text"), sc.Name("args"), sc.Name("err")
	e.printf("%s, %s, %s := %s.RenderUpdate(%s, %q, %s, %s)\n",
		text, args, err, changes, e.dialect(), m.target.table, columnNamesLiteral(m.updated()), q)
	e.printf("if %s != nil { return %s%s }\n", err, zero, err)
	wrap := e.fmtPkg() + ".Errorf(" + goString("update "+m.target.table+": %w") + ", " + err + ")"
	notFound := func() string { return e.notFound(m.target.table, nil, nil) }
	if m.result == resultRow || m.result == resultRows {
		e.printf("%s += %s\n", text, goString(" RETURNING "+e.columnList(m.columns)))
		e.updateReturning(m, sc, recv, sig, text, args, err, model, wrap, notFound)
		return
	}
	res := e.execResult(sc)
	e.printf("%s, %s := %s.executor.%s(%s, %s, %s...)\n", res, err, recv, e.exec, sig.ctx, text, args)
	e.printf("if %s != nil { return %s%s }\n", err, zero, wrap)
	affected := e.rowsAffected(sc, res, err, zero, wrap)
	if m.result == resultCount {
		e.printf("return %s, nil\n", affected)
		return
	}
	if e.d == snapshot.MySQL {
		e.updateMatched(sc, recv, sig.ctx, q, affected, err, wrap, notFound(), m.target.table)
	} else {
		e.printf("if %s == 0 { return %s }\n", affected, notFound())
	}
	e.printf("return nil\n")
}

func (e repoEmitter) updateReturning(m repoMethod, sc gofile.Scope, recv string, sig signature,
	text, args, err, model, wrap string, notFound func() string,
) {
	if m.result == resultRows {
		rows := sc.Name("rows")
		e.printf("%s, %s := %s.executor.%s(%s, %s, %s...)\n", rows, err, recv, e.query, sig.ctx, text, args)
		e.printf("if %s != nil { return nil, %s }\ndefer %s.Close()\n", err, wrap, rows)
		out := sc.Name("out")
		e.scanRows(scanNames{rows: rows, out: out, row: sc.Name("row"), err: err, wrap: wrap},
			model, m.columns, sc)
		e.printf("return %s, nil\n", out)
		return
	}
	row := sc.Name("row")
	e.printf("var %s %s\n", row, model)
	dests, assignments := e.scanTargets(sc, row, m.columns)
	e.printf("%s = %s.executor.%s(%s, %s, %s...).Scan(%s)\n",
		err, recv, e.queryRow, sig.ctx, text, args, strings.Join(dests, ", "))
	var noRows string
	if e.drv == driverPgx {
		noRows = e.f.Import("github.com/jackc/pgx/v5", "pgx") + ".ErrNoRows"
	} else {
		noRows = e.f.Import("database/sql", "sql") + ".ErrNoRows"
	}
	e.printf("if %s.Is(%s, %s) { return %s{}, %s }\n", e.f.Import("errors", "errors"),
		err, noRows, model, notFound())
	e.printf("if %s != nil { return %s{}, %s }\n", err, model, wrap)
	e.assignScanned(assignments)
	e.printf("return %s, nil\n", row)
}

func (e repoEmitter) updateMatched(sc gofile.Scope, recv, ctx, q, affected, err, wrap, notFound, table string) {
	rd, found := sc.Name("rd"), sc.Name("found")
	e.printf("if %s == 0 {\n", affected)
	e.printf("%s, %s := %s.Render(%s, 1)\nif %s != nil { return %s }\n",
		rd, err, q, e.dialect(), err, err)
	e.printf("var %s bool\n", found)
	e.printf("if %s := %s.executor.%s(%s, %s+%s.Where()+\")\", %s.Args()...).Scan(&%s); %s != nil { return %s }\n",
		err, recv, e.queryRow, ctx, goString("SELECT EXISTS (SELECT 1 FROM "+sqlName(e.d, table)),
		rd, rd, found, err, wrap)
	e.printf("if !%s { return %s }\n}\n", found, notFound)
}

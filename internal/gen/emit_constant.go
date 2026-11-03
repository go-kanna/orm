package gen

import (
	"fmt"
	"go/types"
	"strings"

	"github.com/go-kanna/internal/gofile"
)

// constantRead writes the body of m, a read or a Delete that takes no scope, with its statement a constant, and
// reports whether it did: one with nothing a scope could change is the same statement every time.
func (e repoEmitter) constantRead(m repoMethod, sc gofile.Scope, recv string, sig signature, q string) bool {
	if m.scoped || m.projection != nil {
		return false
	}
	switch m.verb {
	case verbGet, verbFind, verbFirst, verbList, verbCount, verbExists:
	case verbDelete:
		if m.returns != returnsNone && m.returns != returnsCount {
			return false
		}
	case verbPluck, verbInsert, verbUpdate, verbUpsert:
		return false
	}
	table := sqlName(e.d, m.target.table)
	conds, args := make([]string, 0, len(m.keys)), make([]string, 0, len(m.keys))
	names := make([]string, 0, len(m.keys))
	for _, k := range m.keys {
		args = append(args, sig.params[k.param])
		conds = append(conds, sqlName(e.d, k.col.col.Name)+" = "+e.placeholder(len(args)))
		names = append(names, k.col.col.Name)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	err := sc.Name("err")
	call := strings.Join(append([]string{sig.ctx, q}, args...), ", ")
	notFound := func() string { return sig.mapped(e.notFound(m.target.table, names, args)) }

	switch m.verb { //nolint:exhaustive // the verbs above, the rows read by default
	case verbCount, verbExists:
		query, typ, zero := "SELECT count(*) FROM "+table+where, "int64", "0"
		if m.verb == verbExists {
			query, typ, zero = "SELECT EXISTS (SELECT 1 FROM "+table+where+")", "bool", "false"
		}
		n := sc.Name("n")
		e.printf("const %s = %s\nvar %s %s\n", q, goString(query), n, typ)
		e.printf("if %[1]s := %[2]s.db.%[3]s(%[4]s).Scan(&%[5]s); %[1]s != nil {\nreturn %[6]s, %[7]s\n}\n"+
			"return %[5]s, nil\n", err, recv, e.queryRow, call, n, zero, sig.mapped(e.fmtPkg()+".Errorf("+
			goString(strings.ToLower(string(m.verb))+" "+m.target.table+": %w")+", "+err+")"))
	case verbDelete:
		res := e.execResult(sc)
		wrap := sig.mapped(e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", "delete from "+m.target.table+": %w") + ", " +
			err + ")")
		zero := ""
		if m.returns == returnsCount {
			zero = "0, "
		}
		e.printf("const %s = %s\n", q, goString("DELETE FROM "+table+where))
		e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s)\nif %[2]s != nil {\nreturn %[6]s%[7]s\n}\n", res, err, recv, e.exec,
			call, zero, wrap)
		affected := e.rowsAffected(sc, res, err, zero, wrap)
		if m.returns == returnsCount {
			e.printf("return %s, nil\n", affected)
			return true
		}
		e.printf("if %s == 0 {\nreturn %s\n}\nreturn nil\n", affected, notFound())
	default:
		query := "SELECT " + e.columnList(e.models[m.target.table].columns) + " FROM " + table + where
		if m.verb == verbFirst {
			if key := m.table.PrimaryKey; len(key) > 0 {
				order := make([]string, 0, len(key))
				for _, name := range key {
					order = append(order, sqlName(e.d, name))
				}
				query += " ORDER BY " + strings.Join(order, ", ")
			}
			query += " LIMIT 1"
		}
		if m.lock != "" {
			query += " " + m.lock
		}
		read := "query" + e.names[m.target.table] + "(" + strings.Join(append([]string{sig.ctx, recv + ".db", q}, args...),
			", ") + ")"
		e.printf("const %s = %s\n", q, goString(query))
		if m.verb == verbList && sig.recv == "" {
			e.printf("return %s\n", read)
			return true
		}
		rows, model := sc.Name("rows"), types.TypeString(m.model, e.f.Qualifier)
		zero := model + "{}"
		if m.verb == verbList || m.optional {
			zero = "nil"
		}
		e.printf("%[1]s, %[2]s := %[3]s\nif %[2]s != nil {\nreturn %[4]s, %[5]s\n}\n", rows, err, read, zero,
			sig.mapped(err))
		switch {
		case m.verb == verbList:
			e.printf("return %s, nil\n", rows)
		case m.optional:
			e.printf("if len(%[1]s) == 0 {\nreturn nil, nil\n}\nreturn &%[1]s[0], nil\n", rows)
		default:
			e.printf("if len(%[1]s) == 0 {\nreturn %[2]s, %[3]s\n}\nreturn %[1]s[0], nil\n", rows, zero, notFound())
		}
	}
	return true
}

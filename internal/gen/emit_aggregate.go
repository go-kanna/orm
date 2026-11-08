package gen

import (
	"fmt"
	"go/types"
	"slices"
	"strings"

	"github.com/go-kanna/internal/gofile"
)

// aggregateRead writes the rest of the body of m, a Sum, a Min, a Max, or an Avg taking scopes, which reads the
// aggregate of a column of the rows q matches by the conditions alone. names are the columns of its keys, and args
// their values.
func (e repoEmitter) aggregateRead(m repoMethod, sc gofile.Scope, recv string, sig signature, q string,
	names, args []string,
) {
	zero := e.zeroOf(m.aggregate.typ)
	err, rd := sc.Name("err"), sc.Name("rd")
	e.printf("if %[1]s := %[2]s.ConditionsOnly(%[3]q); %[1]s != nil {\nreturn %[4]s, %[5]s\n}\n", err, q, m.name, zero,
		sig.mapped(err))
	e.printf("%[1]s, %[2]s := %[3]s.Render(%[4]s, 1, nil)\nif %[2]s != nil {\nreturn %[5]s, %[6]s\n}\n", rd, err, q,
		e.dialect(), zero, sig.mapped(err))
	query := goString("SELECT "+aggregateSQL(e.d, *m.aggregate)+" FROM "+sqlName(e.d, m.target.table)) + "+" + rd +
		".Where()"
	e.aggregateRow(m, sc, recv, sig, err, sig.ctx+", "+query+", "+rd+".Args()...", names, args)
}

// aggregateRow writes the rest of the body of m, which reads an aggregate as the one column of the one row the query
// call runs returns, its error into err: into a value, which is zero for no value of a Sum, and orm.ErrNotFound for
// none of the others, or into a pointer, nil for no value. names are the columns of its keys, and args their values.
func (e repoEmitter) aggregateRow(m repoMethod, sc gofile.Scope, recv string, sig signature, err, call string,
	names, args []string,
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
	format := make([]string, 0, len(names))
	for _, n := range names {
		format = append(format, n+" %v")
	}
	if len(format) > 0 {
		what += " with " + strings.Join(format, " and ")
	}
	notFound := fmt.Sprintf("%s.Errorf(%q, %s)", e.fmtPkg(), what+": %w",
		strings.Join(append(slices.Clone(args), e.ref("ErrNotFound")), ", "))
	e.printf("if %s == nil {\nreturn %s, %s\n}\nreturn *%s, nil\n", v, zero, sig.mapped(notFound), v)
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

package gen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/go-kanna/internal/gofile"
)

// copyRows writes the body of m, a Copy: its rows written by CopyFrom of pgx into the columns an insert of many rows
// writes. One COPY writes a column of every row or of none, so the column the database assigns unless given is
// written when every row gives it a value, and left to the database when none does; rows of both return an error
// before anything is written.
func (e repoEmitter) copyRows(m repoMethod, sc gofile.Scope, recv string, sig signature) {
	// The packages come first, so that they keep their names and the locals yield.
	pgx, fmtPkg := e.f.Import("github.com/jackc/pgx/v5", "pgx"), e.fmtPkg()
	cols := m.inserted()
	for _, c := range cols {
		e.storageType(c)
	}
	n, err, i := sc.Name("n"), sc.Name("err"), sc.Name("i")
	e.printf("if len(%s) == 0 {\nreturn 0, nil\n}\n", sig.row)
	names := make([]string, 0, len(cols)+1)
	for _, c := range cols {
		names = append(names, strconv.Quote(c.col.Name))
	}
	columns, values := "[]string{"+strings.Join(names, ", ")+"}", "return "+e.copyValues(sig.row, i, cols)+", nil\n"
	if given, ok := m.given(); ok {
		field := given.path[len(given.path)-1]
		with, row, list := sc.Name("with"+field), sc.Name("row"), sc.Name("columns")
		gives := func(row string) string { return row + "." + fieldPath(given) + " != 0" }
		mixed := sig.mapped(fmtPkg + ".Errorf(" + strconv.Quote("orm: "+m.name+": some rows give "+given.col.Name+
			" and some leave it to the database, which one COPY cannot do: copy them apart") + ")")
		e.printf("%s := %s\nfor _, %s := range %s[1:] {\nif (%s) != %s {\nreturn 0, %s\n}\n}\n", with,
			gives(sig.row+"[0]"), row, sig.row, gives(row), with, mixed)
		e.printf("%[1]s := %[2]s\nif %[3]s {\n%[1]s = append(%[1]s, %[4]q)\n}\n", list, columns, with, given.col.Name)
		columns = list
		withValues := e.copyValues(sig.row, i, slices.Concat(cols, []column{given}))
		values = "if " + with + " {\nreturn " + withValues + ", nil\n}\n" + values
	}
	e.printf("%[1]s, %[2]s := %[3]s.db.CopyFrom(%[4]s, %[5]s.Identifier{%[6]q}, %[7]s,\n"+
		"%[5]s.CopyFromSlice(len(%[8]s), func(%[9]s int) ([]any, error) {\n%[10]s}))\n", n, err, recv, sig.ctx, pgx,
		m.target.table, columns, sig.row, i, values)
	e.printf("if %s != nil {\nreturn 0, %s\n}\nreturn %s, nil\n", err,
		sig.mapped(fmtPkg+".Errorf("+fmt.Sprintf("%q", m.action()+": %w")+", "+err+")"), n)
}

// copyValues is the values of cols of the row of rows at index i, as CopyFrom takes them.
func (e repoEmitter) copyValues(rows, i string, cols []column) string {
	values := make([]string, 0, len(cols))
	for _, c := range cols {
		values = append(values, e.storageValue(c, rows+"["+i+"]."+fieldPath(c)))
	}
	return "[]any{" + strings.Join(values, ", ") + "}"
}

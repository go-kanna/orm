package gen

import (
	"fmt"
	"go/types"
	"slices"
	"strings"

	"github.com/go-kanna/internal/gofile"

	"github.com/go-kanna/orm/internal/snapshot"
)

// given is the column of m's model that the database assigns unless an insert gives it a value, which m writes when
// the model's value is not zero, and leaves to the database when it is.
func (m repoMethod) given() (column, bool) {
	for _, c := range m.columns {
		if b, ok := c.typ.Underlying().(*types.Basic); ok && b.Info()&types.IsInteger != 0 && c.col.Assigned &&
			c.col.Writable && !c.insertDefault {
			return c, true
		}
	}
	return column{}, false
}

// gives reports whether m writes the column name, which the database assigns, when the model gives it a value.
func (m repoMethod) gives(name string) bool {
	c, ok := m.given()
	return ok && c.col.Name == name
}

// insertText is the statement inserting the columns cols of m, a row of them on PostgreSQL, or each of the rows of the
// arrays unnest takes of a many, but for what follows VALUES on MySQL, where the rows are written as many as a chunk
// has.
func (e repoEmitter) insertText(m repoMethod, cols []column) string {
	var b strings.Builder
	fmt.Fprintf(&b, "INSERT INTO %s", sqlName(e.d, m.target.table))
	if len(cols) == 0 && e.d != snapshot.MySQL {
		b.WriteString(" DEFAULT VALUES")
	} else {
		fmt.Fprintf(&b, " (%s)\n", e.columnList(cols))
	}
	switch {
	case e.d == snapshot.MySQL:
		b.WriteString("VALUES " + mysqlValues(len(cols)))
	case len(cols) > 0 && m.many:
		casts := make([]string, 0, len(cols))
		for _, c := range cols {
			casts = append(casts, fmt.Sprintf("$%d::%s[]", len(casts)+1, castType(c.col.Type)))
		}
		fmt.Fprintf(&b, "SELECT * FROM unnest(%s)", //nolint:unqueryvet // unnest gives the columns listed
			strings.Join(casts, ", "))
	case len(cols) > 0:
		params := make([]string, 0, len(cols))
		for range cols {
			params = append(params, fmt.Sprintf("$%d", len(params)+1))
		}
		fmt.Fprintf(&b, "VALUES (%s)", strings.Join(params, ", "))
	}
	return b.String()
}

// mysqlValues is the row of VALUES of n columns.
func mysqlValues(n int) string {
	if n == 0 {
		return "()"
	}
	return "(" + strings.Repeat("?, ", n-1) + "?)"
}

// unnestRows selects the rows of a many on PostgreSQL from the arrays casts take, one for each of cols. When the rows
// give the column of given, the last of cols, one whose value is zero gets what the database assigns, so that the rows
// that give it and those that do not are written by one statement.
func (e repoEmitter) unnestRows(cols []column, casts []string, given column, gives bool) string {
	if !gives {
		//nolint:unqueryvet // unnest gives the columns listed
		return fmt.Sprintf("SELECT * FROM unnest(%s)", strings.Join(casts, ", "))
	}
	names := make([]string, 0, len(cols))
	selected := make([]string, 0, len(cols))
	for _, c := range cols {
		name := sqlName(e.d, c.col.Name)
		names = append(names, name)
		selected = append(selected, "u."+name)
	}
	selected[len(selected)-1] = fmt.Sprintf("COALESCE(NULLIF(u.%s, 0), %s)", sqlName(e.d, given.col.Name),
		given.col.AssignedBy)
	return fmt.Sprintf("SELECT %s FROM unnest(%s) AS u(%s)", strings.Join(selected, ", "), strings.Join(casts, ", "),
		strings.Join(names, ", "))
}

// insertGiven writes the body of m, an Insert or an Upsert on PostgreSQL of a row that holds given, writing the
// column when the row's value is not zero, and leaving it to the database when it is: the statements of the two kept
// as constants, and the row going by the one of its value.
func (e repoEmitter) insertGiven(m repoMethod, sc gofile.Scope, recv string, sig signature, given column, set,
	err string,
) {
	model := types.TypeString(m.model, e.f.Qualifier)
	cols := m.inserted()
	withCols := slices.Concat(cols, []column{given})
	without, with := sc.Name("without"+given.path[len(given.path)-1]), sc.Name("with"+given.path[len(given.path)-1])
	e.printf("const %s = %s\n", without, goString(e.insertText(m, cols)+e.insertTail(m, len(cols)+1)))
	e.printf("const %s = %s\n", with, goString(e.insertText(m, withCols)+e.insertTail(m, len(withCols)+1)))
	wrap := e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", m.action()+": %w") + ", " + err + ")"
	value := sig.row + "." + fieldPath(given)
	q, args := sc.Name("q"), sc.Name("args")
	values := make([]string, 0, len(cols))
	for _, c := range cols {
		values = append(values, e.written(c, sig.row, sig.now))
	}
	e.printf("%s, %s := %s, []any{%s}\nif %s != 0 {\n%s, %s = %s, append(%s, %s)\n}\n", q, args, without,
		strings.Join(values, ", "), value, q, args, with, args, value)
	if set != "" {
		e.printf("%[1]s = append(%[1]s, %[2]s...)\n", args, set)
	}
	call := sig.ctx + ", " + q + ", " + args + "..."
	switch m.returns { //nolint:exhaustive // shaped turns the others down for a write
	case returnsRow:
		row := sc.Name("row")
		e.printf("var %s %s\n", row, model)
		dests, assignments := e.scanTargets(sc, row, m.columns)
		e.printf("%[1]s %[8]s %[2]s.db.%[3]s(%[4]s).\nScan(%[5]s)\nif %[1]s != nil {\nreturn %[6]s{}, %[7]s\n}\n",
			err, recv, e.queryRow, call, strings.Join(dests, ", "), model, sig.mapped(wrap), declare(set))
		e.assignScanned(assignments)
		e.printf("return %s, nil\n", row)
	case returnsNone:
		e.printf("if _, %[1]s := %[2]s.db.%[3]s(%[4]s); %[1]s != nil {\nreturn %[5]s\n}\nreturn nil\n", err, recv,
			e.exec, call, sig.mapped(wrap))
	}
}

// insertGivenMySQL is insertGiven on MySQL, where a row the database assigns the column of is read back by the
// value LAST_INSERT_ID() tells, and one that gives it by its key. The rows of a many are written as many to a
// statement as its placeholders take, a row whose value is zero giving NULL, which AUTO_INCREMENT assigns, so that
// the rows that give the column and those that do not go by one statement.
func (e repoEmitter) insertGivenMySQL(m repoMethod, sc gofile.Scope, recv string, sig signature, given column, tail,
	set, err string,
) {
	model := types.TypeString(m.model, e.f.Qualifier)
	cols := m.inserted()
	withCols := slices.Concat(cols, []column{given})
	without, with := sc.Name("without"+given.path[len(given.path)-1]), sc.Name("with"+given.path[len(given.path)-1])
	wrap := sig.mapped(e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", m.action()+": %w") + ", " + err + ")")
	if m.many {
		e.printf("const %s = %s\n", with, goString(e.insertText(m, withCols)))
		e.printf("if len(%s) == 0 {\nreturn nil\n}\n", sig.row)
		e.readClock(recv, sig)
		// The packages come first, so that they keep their names and the locals, one named after a field, yield.
		chunks, repeat := e.f.Import("slices", "slices"), e.f.Import("strings", "strings")
		e.importStorage(withCols)
		inner := e.f.Scope()
		chunk, args, row, value := inner.Name("chunk"), inner.Name("args"), inner.Name("row"),
			inner.Name(lowerFirst(given.path[len(given.path)-1]))
		fields := make([]string, 0, len(withCols))
		for _, c := range cols {
			fields = append(fields, e.written(c, row, sig.now))
		}
		fields = append(fields, value)
		query := with + "+" + repeat + ".Repeat(" +
			goString(", "+mysqlValues(len(withCols))) + ", len(" + chunk + ")-1)"
		if tail != "" {
			query += "+" + goString(tail)
		}
		e.printf("for %[1]s := range %[2]s.Chunk(%[3]s, %[4]d) {\n%[5]s := make([]any, 0, len(%[1]s)*%[6]d)\n"+
			"for _, %[7]s := range %[1]s {\nvar %[8]s any\nif %[7]s.%[9]s != 0 {\n%[8]s = %[10]s\n}\n"+
			"%[5]s = append(%[5]s, %[11]s)\n}\n", chunk, chunks, sig.row, 65535/len(withCols),
			args, len(withCols), row, value, fieldPath(given), e.storageValue(given, row+"."+fieldPath(given)),
			strings.Join(fields, ", "))
		e.printf("if _, %[1]s := %[2]s.db.%[3]s(%[4]s, %[5]s, %[6]s...); %[1]s != nil {\nreturn %[7]s\n}\n}\n", err,
			recv, e.exec, sig.ctx, query, args, wrap)
		e.printf("return nil\n")
		return
	}

	value := sig.row + "." + fieldPath(given)
	q, args := sc.Name("q"), sc.Name("args")
	values := make([]string, 0, len(cols))
	for _, c := range cols {
		values = append(values, e.written(c, sig.row, sig.now))
	}
	e.printf("const %s = %s\n", without, goString(e.insertText(m, cols)+tail))
	e.printf("const %s = %s\n", with, goString(e.insertText(m, withCols)+tail))
	e.printf("%s, %s := %s, []any{%s}\nif %s != 0 {\n%s, %s = %s, append(%s, %s)\n}\n", q, args, without,
		strings.Join(values, ", "), value, q, args, with, args, value)
	if set != "" {
		e.printf("%[1]s = append(%[1]s, %[2]s...)\n", args, set)
	}
	call := sig.ctx + ", " + q + ", " + args + "..."
	if m.returns != returnsRow {
		e.printf("if _, %[1]s := %[2]s.db.%[3]s(%[4]s); %[1]s != nil {\nreturn %[5]s\n}\nreturn nil\n", err, recv,
			e.exec, call, wrap)
		return
	}
	zero := model + "{}, "
	res, id := e.execResult(sc), sc.Name("id")
	e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s)\nif %[2]s != nil {\nreturn %[6]s%[7]s\n}\n", res, err, recv, e.exec,
		call, zero, wrap)
	// A row whose value the database assigned is read back by the value LAST_INSERT_ID() tells, which an upsert
	// meeting a row sets to that of the row met; one that gave its value, by its key, which LAST_INSERT_ID() does not
	// tell.
	e.printf("if %s == 0 {\n%s, %s := %s.LastInsertId()\nif %s != nil {\nreturn %s%s\n}\n", value, id, err, res, err,
		zero, wrap)
	e.readBack(m, recv, sig, []string{given.col.Name}, []string{id}, zero, err, wrap)
	e.printf("}\n")
	key := m.table.PrimaryKey
	if m.verb == verbUpsert {
		key = m.rowKey()
	}
	keyArgs := make([]string, 0, len(key))
	for _, name := range key {
		keyArgs = append(keyArgs, sig.row+"."+fieldPath(m.field(name)))
	}
	e.readBack(m, recv, sig, key, keyArgs, zero, err, wrap)
}

// readBack writes the read of the row of m whose key columns hold args, and its return, wrapping its error err by
// wrap.
func (e repoEmitter) readBack(m repoMethod, recv string, sig signature, key, args []string, zero, err, wrap string) {
	conds := make([]string, 0, len(key))
	for _, name := range key {
		conds = append(conds, sqlName(e.d, name)+" = ?")
	}
	inner := e.f.Scope()
	row := inner.Name("row")
	query := "SELECT " + e.columnList(m.columns) + " FROM " + sqlName(e.d, m.target.table) + " WHERE " +
		strings.Join(conds, " AND ")
	e.printf("var %s %s\n", row, types.TypeString(m.model, e.f.Qualifier))
	dests, assignments := e.scanTargets(inner, row, m.columns)
	e.printf("%s = %s.db.%s(%s, %s, %s).Scan(%s)\n", err, recv, e.queryRow, sig.ctx, goString(query),
		strings.Join(args, ", "), strings.Join(dests, ", "))
	e.readBackError(m, sig, key, args, zero, err, wrap)
	e.assignScanned(assignments)
	e.printf("return %s, nil\n", row)
}

// readBackError writes the return of err, the error of the read back of the row of m whose key columns hold args:
// wrapped by wrap, or, when the row is not there, an error saying so, which is not orm.ErrNotFound, as the row was
// written.
func (e repoEmitter) readBackError(m repoMethod, sig signature, key, args []string, zero, err, wrap string) {
	format, shown := e.keyValues(m.fields(key), args)
	missing := sig.mapped(fmt.Sprintf("%s.Errorf(%q, %s)", e.fmtPkg(),
		m.action()+": wrote the row, but no row with "+format+" was there to read back", shown))
	e.printf("if %s.Is(%s, %s.ErrNoRows) {\nreturn %s%s\n}\nif %s != nil {\nreturn %s%s\n}\n",
		e.f.Import("errors", "errors"), err, e.driverPkg(), zero, missing, err, zero, wrap)
}

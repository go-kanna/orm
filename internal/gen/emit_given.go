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

// insertGiven writes the body of m, an Insert or an Upsert on PostgreSQL of rows that hold given, writing the column
// of a row whose value is not zero, and leaving it to the database for one whose value is: the statements of the two
// kept as constants, a row going by the one of its value, and the rows of a many by two statements, read back in the
// order they are given.
func (e repoEmitter) insertGiven(m repoMethod, sc gofile.Scope, recv string, sig signature, given column, tail,
	what string,
) {
	model := types.TypeString(m.model, e.f.Qualifier)
	cols := m.inserted()
	withCols := slices.Concat(cols, []column{given})
	without, with := sc.Name("without"+given.path[len(given.path)-1]), sc.Name("with"+given.path[len(given.path)-1])
	e.printf("const %s = %s\n", without, goString(e.insertText(m, cols)+tail))
	e.printf("const %s = %s\n", with, goString(e.insertText(m, withCols)+tail))
	err := sc.Name("err")
	wrap := e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", what+": %w") + ", " + err + ")"
	if !m.many {
		value := sig.row + "." + fieldPath(given)
		q, args := sc.Name("q"), sc.Name("args")
		values := make([]string, 0, len(cols))
		for _, c := range cols {
			values = append(values, e.storageValue(c, sig.row+"."+fieldPath(c)))
		}
		e.printf("%s, %s := %s, []any{%s}\nif %s != 0 {\n%s, %s = %s, append(%s, %s)\n}\n", q, args, without,
			strings.Join(values, ", "), value, q, args, with, args, value)
		call := sig.ctx + ", " + q + ", " + args + "..."
		switch m.returns { //nolint:exhaustive // shaped turns the others down for a write
		case returnsRow:
			row := sc.Name("row")
			e.printf("var %s %s\n", row, model)
			dests, assignments := e.scanTargets(sc, row, m.columns)
			e.printf("%[1]s := %[2]s.db.%[3]s(%[4]s).\nScan(%[5]s)\nif %[1]s != nil {\nreturn %[6]s{}, %[7]s\n}\n",
				err, recv, e.queryRow, call, strings.Join(dests, ", "), model, sig.mapped(wrap))
			e.assignScanned(assignments)
			e.printf("return %s, nil\n", row)
		case returnsNone:
			e.printf("if _, %[1]s := %[2]s.db.%[3]s(%[4]s); %[1]s != nil {\nreturn %[5]s\n}\nreturn nil\n", err, recv,
				e.exec, call, sig.mapped(wrap))
		}
		return
	}

	none, zero := "nil", ""
	out := sc.Name("out")
	if m.returns == returnsRows {
		none, zero = "[]"+model+"{}, nil", "nil, "
	}
	e.printf("if len(%s) == 0 {\nreturn %s\n}\n", sig.row, none)
	if m.returns == returnsRows {
		e.printf("%s := make([]%s, len(%s))\n", out, model, sig.row)
	}
	assigned, givenRows, i := sc.Name("assigned"), sc.Name("given"), sc.Name("i")
	e.printf("// The rows whose %s is zero, which the database assigns, and those which give it.\n", given.col.Name)
	e.printf("var %[1]s, %[2]s []int\nfor %[3]s := range %[4]s {\nif %[4]s[%[3]s].%[5]s == 0 {\n"+
		"%[1]s = append(%[1]s, %[3]s)\n} else {\n%[2]s = append(%[2]s, %[3]s)\n}\n}\n", assigned, givenRows, i, sig.row,
		fieldPath(given))
	for _, part := range []struct {
		at, query string
		cols      []column
	}{{assigned, without, cols}, {givenRows, with, withCols}} {
		e.printf("if len(%s) > 0 {\n", part.at)
		inner := e.f.Scope()
		args := make([]string, 0, len(part.cols))
		for _, c := range part.cols {
			v := inner.Name(lowerFirst(c.path[len(c.path)-1]))
			e.printf("%s := make([]%s, len(%s))\n", v, e.storageType(c), part.at)
			args = append(args, v)
		}
		j, k := inner.Name("j"), inner.Name("k")
		e.printf("for %s, %s := range %s {\n", j, k, part.at)
		for n, c := range part.cols {
			e.printf("%s[%s] = %s\n", args[n], j, e.storageValue(c, sig.row+"["+k+"]."+fieldPath(c)))
		}
		e.printf("}\n")
		call := strings.Join(append([]string{sig.ctx, part.query}, args...), ", ")
		if m.returns != returnsRows {
			e.printf("if _, %[1]s := %[2]s.db.%[3]s(%[4]s); %[1]s != nil {\nreturn %[5]s%[6]s\n}\n}\n", err, recv,
				e.exec, call, zero, sig.mapped(wrap))
			continue
		}
		rows, row, n := inner.Name("rows"), inner.Name("row"), inner.Name("n")
		e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s)\nif %[2]s != nil {\nreturn nil, %[6]s\n}\n", rows, err, recv,
			e.query, call, sig.mapped(wrap))
		e.printf("for %[1]s := 0; %[1]s < len(%[2]s) && %[3]s.Next(); %[1]s++ {\nvar %[4]s %[5]s\n", n, part.at, rows,
			row, model)
		dests, assignments := e.scanTargets(inner, row, m.columns)
		e.printf("if %[1]s := %[2]s.Scan(%[3]s); %[1]s != nil {\n%[2]s.Close()\nreturn nil, %[4]s\n}\n", err, rows,
			strings.Join(dests, ", "), sig.mapped(wrap))
		e.assignScanned(assignments)
		e.printf("%s[%s[%s]] = %s\n}\n%s.Close()\n", out, part.at, n, row, rows)
		e.printf("if %[1]s := %[2]s.Err(); %[1]s != nil {\nreturn nil, %[3]s\n}\n}\n", err, rows, sig.mapped(wrap))
	}
	if m.returns == returnsRows {
		e.printf("return %s, nil\n", out)
		return
	}
	e.printf("return nil\n")
}

// insertGivenMySQL is insertGiven on MySQL, where a row the database assigns the column of is read back by the
// value LAST_INSERT_ID() tells, and one that gives it by its key, and the rows of a many are written as many to a
// statement as its placeholders take.
func (e repoEmitter) insertGivenMySQL(m repoMethod, sc gofile.Scope, recv string, sig signature, given column, tail,
	what string,
) {
	model := types.TypeString(m.model, e.f.Qualifier)
	cols := m.inserted()
	withCols := slices.Concat(cols, []column{given})
	without, with := sc.Name("without"+given.path[len(given.path)-1]), sc.Name("with"+given.path[len(given.path)-1])
	err := sc.Name("err")
	wrap := sig.mapped(e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", what+": %w") + ", " + err + ")")
	if m.many {
		e.printf("const %s = %s\n", without, goString(e.insertText(m, cols)))
		e.printf("const %s = %s\n", with, goString(e.insertText(m, withCols)))
		e.printf("if len(%s) == 0 {\nreturn nil\n}\n", sig.row)
		assigned, givenRows, i := sc.Name("assigned"), sc.Name("given"), sc.Name("i")
		e.printf("// The rows whose %s is zero, which the database assigns, and those which give it.\n", given.col.Name)
		e.printf("var %[1]s, %[2]s []int\nfor %[3]s := range %[4]s {\nif %[4]s[%[3]s].%[5]s == 0 {\n"+
			"%[1]s = append(%[1]s, %[3]s)\n} else {\n%[2]s = append(%[2]s, %[3]s)\n}\n}\n", assigned, givenRows, i,
			sig.row, fieldPath(given))
		for _, part := range []struct {
			at, head string
			cols     []column
		}{{assigned, without, cols}, {givenRows, with, withCols}} {
			inner := e.f.Scope()
			chunk, args, k := inner.Name("chunk"), inner.Name("args"), inner.Name("k")
			fields := make([]string, 0, len(part.cols))
			for _, c := range part.cols {
				fields = append(fields, e.storageValue(c, sig.row+"["+k+"]."+fieldPath(c)))
			}
			query := part.head + "+" + e.f.Import("strings", "strings") + ".Repeat(" +
				goString(", "+mysqlValues(len(part.cols))) + ", len(" + chunk + ")-1)"
			if tail != "" {
				query += "+" + goString(tail)
			}
			e.printf("for %[1]s := range %[2]s.Chunk(%[3]s, %[4]d) {\n%[5]s := make([]any, 0, len(%[1]s)*%[6]d)\n"+
				"for _, %[7]s := range %[1]s {\n%[5]s = append(%[5]s, %[8]s)\n}\n", chunk, e.f.Import("slices", "slices"),
				part.at, 65535/len(part.cols), args, len(part.cols), k, strings.Join(fields, ", "))
			e.printf("if _, %[1]s := %[2]s.db.%[3]s(%[4]s, %[5]s, %[6]s...); %[1]s != nil {\nreturn %[7]s\n}\n}\n", err,
				recv, e.exec, sig.ctx, query, args, wrap)
		}
		e.printf("return nil\n")
		return
	}

	value := sig.row + "." + fieldPath(given)
	q, args := sc.Name("q"), sc.Name("args")
	values := make([]string, 0, len(cols))
	for _, c := range cols {
		values = append(values, e.storageValue(c, sig.row+"."+fieldPath(c)))
	}
	e.printf("const %s = %s\n", without, goString(e.insertText(m, cols)+tail))
	e.printf("const %s = %s\n", with, goString(e.insertText(m, withCols)+tail))
	e.printf("%s, %s := %s, []any{%s}\nif %s != 0 {\n%s, %s = %s, append(%s, %s)\n}\n", q, args, without,
		strings.Join(values, ", "), value, q, args, with, args, value)
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
	e.printf("if %[1]s := %[2]s.db.%[3]s(%[4]s, %[5]s, %[6]s).Scan(%[7]s); %[1]s != nil {\nreturn %[8]s%[9]s\n}\n",
		err, recv, e.queryRow, sig.ctx, goString(query), strings.Join(args, ", "), strings.Join(dests, ", "), zero,
		wrap)
	e.assignScanned(assignments)
	e.printf("return %s, nil\n", row)
}

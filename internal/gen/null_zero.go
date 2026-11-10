package gen

import (
	"fmt"
	"go/types"

	"github.com/go-kanna/internal/gofile"
)

func (e repoEmitter) storageType(c column) string {
	typ := types.TypeString(c.typ, e.f.Qualifier)
	if c.nullzero {
		return e.f.Import("database/sql", "sql") + ".Null[" + typ + "]"
	}
	return typ
}

// importStorage imports the packages storageValue names for cols, and only those, so that they keep their names when
// called before the locals are named.
func (e repoEmitter) importStorage(cols []column) {
	for _, c := range cols {
		if c.nullzero {
			e.storageType(c)
		}
	}
}

func (e repoEmitter) storageValue(c column, expression string) string {
	if c.nullzero {
		return fmt.Sprintf("%s{V: %s, Valid: %s != \"\"}", e.storageType(c), expression, expression)
	}
	return expression
}

// scanTargets prepares nullable strings separately so NULL never reaches a non-nullable enum scanner.
func (e repoEmitter) scanTargets(sc gofile.Scope, row string, columns []column) ([]string, []string) {
	var targets, assignments []string
	for _, c := range columns {
		field := row + "." + fieldPath(c)
		if !c.nullzero {
			targets = append(targets, "&"+field)
			continue
		}
		local := sc.Name("nullable" + c.path[len(c.path)-1])
		e.printf("var %s %s\n", local, e.storageType(c))
		targets = append(targets, "&"+local)
		assignments = append(assignments, field+" = "+local+".V")
	}
	return targets, assignments
}

func (e repoEmitter) assignScanned(assignments []string) {
	for _, assignment := range assignments {
		e.printf("%s\n", assignment)
	}
}

// scalarRows writes the loop reading rows of one column of typ, as scanRows does rows of a struct: into one value, set
// to its zero value before each row.
func (e repoEmitter) scalarRows(n scanNames, typ string, sc gofile.Scope) {
	none, dest := sc.Name("none"), sc.Name("dest")
	e.printf("%[1]s := []%[2]s{}\nvar %[3]s, %[4]s %[2]s\n%[5]s := []any{&%[3]s}\nfor %[6]s.Next() {\n%[3]s = %[4]s\n",
		n.out, typ, n.row, none, dest, n.rows)
	e.printf("if %[1]s := %[2]s.Scan(%[3]s...); %[1]s != nil {\nreturn nil, %[4]s\n}\n", n.err, n.rows, dest,
		n.wrap)
	e.printf("%[1]s = append(%[1]s, %[2]s)\n}\n", n.out, n.row)
	e.printf("if %[1]s := %[2]s.Err(); %[1]s != nil {\nreturn nil, %[3]s\n}\n", n.err, n.rows, n.wrap)
}

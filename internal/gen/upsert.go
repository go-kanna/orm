package gen

import (
	"slices"
	"strconv"

	"github.com/go-kanna/internal/gofile"

	"github.com/go-kanna/orm/internal/snapshot"
)

// upsertAssignment is the assignment an upsert makes of c over the row it meets: incoming, the value of the row given;
// or, of an upsert taking a change set, the value the change set sets when it sets one, existing plus the value it
// adds when it adds one to an additive column, and else existing, the value kept. The flag and the value of a set are
// at placeholders first and first+1, and those of an add at first+2 and first+3, numbered on PostgreSQL.
func (e repoEmitter) upsertAssignment(m repoMethod, c column, incoming, existing string, first int) string {
	name := sqlName(e.d, c.col.Name)
	switch {
	case m.changes == "":
		return name + " = " + incoming
	case e.d == snapshot.MySQL && additive(c):
		return name + " = IF(?, ?, IF(?, " + existing + " + ?, " + existing + "))"
	case e.d == snapshot.MySQL:
		return name + " = IF(?, ?, " + existing + ")"
	}
	p := func(i int) string { return "$" + strconv.Itoa(first+i) }
	added := ""
	if additive(c) {
		added = " WHEN " + p(2) + " THEN " + existing + " + " + p(3)
	}
	return name + " = CASE WHEN " + p(0) + " THEN " + p(1) + added + " ELSE " + existing + " END"
}

// upsertParams is the number of placeholders upsertAssignment gives c of m.
func upsertParams(m repoMethod, c column) int {
	switch {
	case m.changes == "":
		return 0
	case additive(c):
		return 4
	}
	return 2
}

// changeValues writes, for m, an upsert taking a change set, the values of upsertAssignment that the change set gives,
// in a slice of sc it names, and the return of zero and the error of a change set writing what it cannot, first in the
// body. It returns the names of the slice and of the error, or, for any other write, "" and the name of the error the
// statement declares.
func (e repoEmitter) changeValues(m repoMethod, sc gofile.Scope, sig signature, zero string) (string, string) {
	if m.changes == "" {
		return "", sc.Name("err")
	}
	set, err := sc.Name("set"), sc.Name("err")
	updated := m.updated()
	added := "nil"
	if cs := slices.DeleteFunc(slices.Clone(updated), func(c column) bool { return !additive(c) }); len(cs) > 0 {
		added = columnNamesLiteral(cs)
	}
	e.printf("%[1]s, %[2]s := %[3]s.Of().UpsertValues(%[4]q, %[5]s, %[6]s, %[7]s)\n"+
		"if %[2]s != nil {\nreturn %[8]s%[9]s\n}\n", set, err, sig.params[m.changes], m.name, columnNamesLiteral(updated),
		added, namesLiteral(m.rowKey()), zero, sig.mapped(err))
	return set, err
}

// declare is how a statement declaring the error alone assigns it: by = after changeValues declared it with set.
func declare(set string) string {
	if set != "" {
		return "="
	}
	return ":="
}

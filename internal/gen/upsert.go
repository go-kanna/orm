package gen

import (
	"slices"
	"strconv"
	"strings"

	"github.com/go-kanna/internal/gofile"

	"github.com/go-kanna/orm/internal/snapshot"
)

// upsertAssignment is the assignment an upsert makes of c over the row it meets: incoming, the value of the row given;
// or, of an upsert taking a change set, the value the change set sets when it sets one, existing plus the value it
// adds when it adds one to an additive column, the greater or the lesser of existing and the value it raises or lowers
// an ordered column to, and else existing, the value kept. The flag and the value of a set are at placeholders first
// and first+1, followed by those of an add, a raise, and a lower that c takes, numbered on PostgreSQL.
func (e repoEmitter) upsertAssignment(m repoMethod, c column, incoming, existing string, first int) string {
	name := sqlName(e.d, c.col.Name)
	if m.changes == "" {
		return name + " = " + incoming
	}
	branches := []func(v string) string{func(v string) string { return v }}
	if additive(c) {
		branches = append(branches, func(v string) string { return existing + " + " + v })
	}
	if ordered(c) {
		branches = append(branches, func(v string) string { return "GREATEST(" + existing + ", " + v + ")" },
			func(v string) string { return "LEAST(" + existing + ", " + v + ")" })
	}
	if e.d == snapshot.MySQL {
		value := existing
		for _, b := range slices.Backward(branches) {
			value = "IF(?, " + b("?") + ", " + value + ")"
		}
		return name + " = " + value
	}
	var value strings.Builder
	for i, b := range branches {
		value.WriteString(" WHEN $" + strconv.Itoa(first+2*i) + " THEN " + b("$"+strconv.Itoa(first+2*i+1)))
	}
	return name + " = CASE" + value.String() + " ELSE " + existing + " END"
}

// upsertParams is the number of placeholders upsertAssignment gives c of m.
func upsertParams(m repoMethod, c column) int {
	if m.changes == "" {
		return 0
	}
	n := 2
	if additive(c) {
		n += 2
	}
	if ordered(c) {
		n += 4
	}
	return n
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
	of := func(holds func(column) bool) string {
		if cs := slices.DeleteFunc(slices.Clone(updated), func(c column) bool { return !holds(c) }); len(cs) > 0 {
			return columnNamesLiteral(cs)
		}
		return "nil"
	}
	e.printf("%[1]s, %[2]s := %[3]s.UpsertValues(%[4]q, %[5]s, %[6]s, %[7]s, %[8]s)\n"+
		"if %[2]s != nil {\nreturn %[9]s%[10]s\n}\n", set, err, e.changesOf(m, sig.params[m.changes], sig.now), m.name,
		columnNamesLiteral(updated), of(additive), of(ordered), namesLiteral(m.rowKey()), zero, sig.mapped(err))
	return set, err
}

// declare is how a statement declaring the error alone assigns it: by = after changeValues declared it with set.
func declare(set string) string {
	if set != "" {
		return "="
	}
	return ":="
}

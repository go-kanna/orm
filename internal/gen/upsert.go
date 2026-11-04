package gen

import (
	"strconv"
	"strings"

	"github.com/go-kanna/internal/gofile"

	"github.com/go-kanna/orm/internal/snapshot"
)

// upsertAssignment is the assignment an upsert makes of c over the row it meets: incoming, the value of the row given;
// or, of an upsert taking a change set, the value the change set gives when it gives one, and else existing, the value
// kept. The flag and the value of the change set are at placeholders first and first+1, numbered on PostgreSQL.
func (e repoEmitter) upsertAssignment(m repoMethod, c column, incoming, existing string, first int) string {
	name := sqlName(e.d, c.col.Name)
	switch {
	case m.changes == "":
		return name + " = " + incoming
	case e.d == snapshot.MySQL:
		return name + " = IF(?, ?, " + existing + ")"
	}
	return name + " = CASE WHEN $" + strconv.Itoa(first) + " THEN $" + strconv.Itoa(first+1) + " ELSE " + existing +
		" END"
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
	allowed := make([]string, 0, len(m.updated()))
	for _, c := range m.updated() {
		allowed = append(allowed, strconv.Quote(c.col.Name))
	}
	key := make([]string, 0, len(m.rowKey()))
	for _, name := range m.rowKey() {
		key = append(key, strconv.Quote(name))
	}
	e.printf("%[1]s, %[2]s := %[3]s.Of().UpsertValues(%[4]q, []string{%[5]s}, []string{%[6]s})\n"+
		"if %[2]s != nil {\nreturn %[7]s%[8]s\n}\n", set, err, sig.params[m.changes], m.name, strings.Join(allowed, ", "),
		strings.Join(key, ", "), zero, sig.mapped(err))
	return set, err
}

// declare is how a statement declaring the error alone assigns it: by = after changeValues declared it with set.
func declare(set string) string {
	if set != "" {
		return "="
	}
	return ":="
}

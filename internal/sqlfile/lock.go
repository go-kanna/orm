package sqlfile

import (
	"go/token"
	"strings"

	"github.com/go-kanna/orm/internal/snapshot"
)

// Lock returns where the first locking clause of the statement s, on a database of dialect d, starts, as in FOR
// UPDATE, and the clause in upper case, or false if s has none. It looks at the statement outside strings, quoted
// names, and comments, its subqueries included.
func (s Statement) Lock(d snapshot.Dialect) (token.Position, string, bool) {
	mysql := d == snapshot.MySQL
	clauses := [][]string{{"FOR", "UPDATE"}, {"FOR", "SHARE"}}
	if mysql {
		clauses = append(clauses, []string{"LOCK", "IN", "SHARE", "MODE"})
	} else {
		clauses = append(clauses, []string{"FOR", "NO", "KEY", "UPDATE"}, []string{"FOR", "KEY", "SHARE"})
	}

	lx := lexer{src: s.Text, mysql: mysql}
	for t := lx.next(); t.kind != end; t = lx.next() {
		for _, clause := range clauses {
			if !t.is(clause[0]) {
				continue
			}
			after := lx
			if !matches(&after, clause[1:]) {
				continue
			}
			return s.at(t.start), strings.Join(clause, " "), true
		}
	}
	return token.Position{}, "", false
}

// matches reports whether the next tokens of l are the keywords words.
func matches(l *lexer, words []string) bool {
	for _, w := range words {
		if !l.next().is(w) {
			return false
		}
	}
	return true
}

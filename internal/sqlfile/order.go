package sqlfile

import "github.com/go-kanna/orm/internal/snapshot"

// OrderWithoutLimit reports whether the statement s, on a database of dialect d, ends in an ORDER BY of its own that
// no LIMIT or FETCH FIRST follows: an order which a statement reading its rows does not keep. It looks at the keywords
// of the statement outside parentheses, strings, quoted names, and comments.
func (s Statement) OrderWithoutLimit(d snapshot.Dialect) bool {
	lx := lexer{src: s.Text, mysql: d == snapshot.MySQL}
	order, depth := false, 0
	for t := lx.next(); t.kind != end; t = lx.next() {
		switch {
		case t.isPunct("("):
			depth++
		case t.isPunct(")"):
			depth--
		case depth != 0:
		case t.is("ORDER") && keyword(&lx, "BY"):
			order = true
		case t.is("LIMIT"), t.is("FETCH") && (keyword(&lx, "FIRST") || keyword(&lx, "NEXT")):
			order = false
		}
	}
	return order
}

// keyword reports whether the next token of l is the keyword w, and steps over it if it is.
func keyword(l *lexer, w string) bool {
	after := *l
	if !after.next().is(w) {
		return false
	}
	*l = after
	return true
}

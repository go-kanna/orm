package sqlfile

import (
	"fmt"
	"go/token"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-kanna/internal/diag"

	"github.com/go-kanna/orm/internal/snapshot"
)

// Condition is the condition a scope narrows its table by.
type Condition struct {
	Text string
	// Pos is where Text starts.
	Pos token.Position
}

// At is the position of line and column of c.Text, both counted from 1.
func (c Condition) At(line, column int) token.Position {
	pos := c.Pos
	if line == 1 {
		pos.Column += column - 1
	} else {
		pos.Column = column
	}
	pos.Line += line - 1
	return pos
}

// Condition returns the condition of the scope s, whose table is table, on a database of dialect d. A scope is written
// as its condition alone, or as SELECT 1 FROM <table> WHERE <condition>, a statement SQL linters can read. kanna-orm
// puts the condition alone into statements of its own, where nothing else of the statement would mean anything, so a
// statement of another shape is a mistake.
func (s Statement) Condition(table string, d snapshot.Dialect) (Condition, *diag.Diag) {
	lx := lexer{src: s.Text, mysql: d == snapshot.MySQL}
	if !lx.next().is("SELECT") {
		return Condition{Text: s.Text, Pos: token.Position{Filename: s.Header.Filename, Line: s.Line, Column: 1}}, nil
	}
	problem := lx.head(table)
	var start, stop int
	if problem == "" {
		start, stop, problem = lx.condition()
	}
	if problem != "" {
		d := diag.Errorf(s.Header, "-- scope: %s.%s: the statement %s", s.Type, s.Name, problem).
			WithHints(fmt.Sprintf("write it as SELECT 1 FROM %s WHERE <condition>, or as the condition alone", table))
		return Condition{}, &d
	}

	text := strings.TrimRightFunc(s.Text[start:stop], unicode.IsSpace)
	trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
	before := s.Text[:start+len(text)-len(trimmed)]
	return Condition{Text: trimmed, Pos: token.Position{
		Filename: s.Header.Filename,
		Line:     s.Line + strings.Count(before, "\n"),
		Column:   utf8.RuneCountInString(before[strings.LastIndexByte(before, '\n')+1:]) + 1,
	}}, nil
}

const notShaped = "is not of the shape a scope takes"

// head reads what follows the SELECT of a statement up to its WHERE, and tells what is wrong with it, if anything,
// for a scope of table.
func (l *lexer) head(table string) string {
	if t := l.next(); t.kind != literal || t.text != "1" {
		return "selects other than 1"
	}
	switch t := l.next(); {
	case t.is("FROM"):
	case t.is("WHERE"), t.kind == end, t.isPunct(";"):
		return notShaped
	default:
		return "selects other than 1"
	}

	t := l.next()
	from := t.start
	var (
		parts []sqlToken
		to    int
	)
	for {
		if t.kind != word && t.kind != quoted {
			return notShaped
		}
		parts, to = append(parts, t), t.end
		if t = l.next(); !t.isPunct(".") {
			break
		}
		t = l.next()
	}
	if !slices.EqualFunc(parts, strings.Split(table, "."), l.names) {
		return fmt.Sprintf("selects from %s, not from its table %s", l.src[from:to], table)
	}

	switch after := *l; {
	case t.is("WHERE"):
		return ""
	case t.is("AS"), (t.kind == word || t.kind == quoted) && after.next().is("WHERE"):
		return fmt.Sprintf("gives %s an alias, which means nothing where kanna-orm puts its condition", table)
	case t.kind == end, t.isPunct(";"):
		return "has no WHERE condition"
	}
	return notShaped
}

// names reports whether t, a part of the name of a table, names part as the database reads it: a quoted name as it
// is written, and one unquoted as PostgreSQL folds it, to lower case, or on MySQL, whose rule depends on how its
// server is set up, whatever its case.
func (l *lexer) names(t sqlToken, part string) bool {
	switch {
	case t.kind == quoted:
		return t.text == part
	case l.mysql:
		return strings.EqualFold(t.text, part)
	}
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, t.text) == part
}

// condition reads the condition after the WHERE of a statement, and returns where it starts and stops, or what is
// wrong with it.
func (l *lexer) condition() (start, stop int, problem string) {
	start, stop = l.pos, len(l.src)
	depth, empty := 0, true
	for t := l.next(); t.kind != end; t = l.next() {
		switch {
		case stop < len(l.src):
			return 0, 0, "holds more than a condition after WHERE"
		case t.isPunct("("):
			depth++
		case t.isPunct(")"):
			depth--
		case t.isPunct(";") && depth == 0:
			stop = t.start
			continue
		case depth == 0 && t.kind == word && clause(t.text, l.mysql):
			return 0, 0, "holds more than a condition after WHERE"
		}
		empty = false
	}
	if empty {
		return 0, 0, "has no WHERE condition"
	}
	return start, stop, ""
}

// clause reports whether word, met outside parentheses after the condition of a statement, starts a clause of a
// SELECT of a database of the dialect mysql tells, words a condition cannot hold there.
func clause(word string, mysql bool) bool {
	switch strings.ToUpper(word) {
	case "GROUP", "HAVING", "WINDOW", "ORDER", "LIMIT", "FETCH", "FOR", "INTO", "UNION", "INTERSECT", "EXCEPT":
		return true
	case "OFFSET": // a name MySQL lets a column have
		return !mysql
	case "LOCK": // as in LOCK IN SHARE MODE, a name PostgreSQL lets a column have
		return mysql
	}
	return false
}

type tokenKind int

const (
	end tokenKind = iota
	// word is a keyword or a name, unquoted.
	word
	quoted
	// literal is a string, a dollar-quoted body, or a number.
	literal
	// punct is any other character.
	punct
)

type sqlToken struct {
	kind tokenKind
	// text is what the token is written as, and for a quoted name, the name.
	text       string
	start, end int
}

func (t sqlToken) is(keyword string) bool {
	return t.kind == word && strings.EqualFold(t.text, keyword)
}

func (t sqlToken) isPunct(c string) bool {
	return t.kind == punct && t.text == c
}

// lexer reads the tokens of a statement of a database, as far as telling a condition from the rest of a statement
// needs, stepping over space and comments. What does not end, such as a string, runs to the end of the statement, for
// the database to tell of.
type lexer struct {
	src   string
	mysql bool
	pos   int
}

func (l *lexer) next() sqlToken {
	l.skip()
	s, i := l.src, l.pos
	if i == len(s) {
		return sqlToken{kind: end, start: i, end: i}
	}
	t := sqlToken{kind: punct, start: i}
	c, size := utf8.DecodeRuneInString(s[i:])
	switch {
	case (c == 'E' || c == 'e') && !l.mysql && strings.HasPrefix(s[i+size:], "'"):
		t.kind, t.end = literal, l.quoted(i+size, true)
	case c == '_' || unicode.IsLetter(c):
		t.kind, t.end = word, l.span(i, func(r rune) bool { return r == '$' || nameRune(r) })
	case c >= '0' && c <= '9':
		t.kind, t.end = literal, l.span(i, func(r rune) bool { return r == '.' || nameRune(r) })
	case c == '"' && !l.mysql, c == '`' && l.mysql:
		t.kind, t.end = quoted, l.quoted(i, false)
		q := s[i : i+1]
		name := strings.TrimSuffix(s[i+1:t.end], q)
		l.pos = t.end
		t.text = strings.ReplaceAll(name, q+q, q)
		return t
	case c == '\'', c == '"':
		t.kind, t.end = literal, l.quoted(i, l.mysql)
	case c == '$' && !l.mysql:
		t.end = l.dollar(i)
		if t.end > i+1 {
			t.kind = literal
		}
	default:
		t.end = i + size
	}
	t.text, l.pos = s[t.start:t.end], t.end
	return t
}

// skip steps over space and comments.
func (l *lexer) skip() {
	s := l.src
	for l.pos < len(s) {
		i := l.pos
		c, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case unicode.IsSpace(c):
			l.pos += size
		case strings.HasPrefix(s[i:], "--") && (!l.mysql || i+2 == len(s) || s[i+2] <= ' '), c == '#' && l.mysql:
			if n := strings.IndexByte(s[i:], '\n'); n >= 0 {
				l.pos += n
			} else {
				l.pos = len(s)
			}
		case strings.HasPrefix(s[i:], "/*"):
			l.pos = l.blockComment(i)
		default:
			return
		}
	}
}

// span returns the end of the run of runes in starting at i.
func (l *lexer) span(i int, in func(rune) bool) int {
	s := l.src
	for i < len(s) {
		c, size := utf8.DecodeRuneInString(s[i:])
		if !in(c) {
			break
		}
		i += size
	}
	return i
}

// quoted returns the end of the quoted text starting at i: a quote is escaped by doubling it, and by a backslash too
// where backslash says so.
func (l *lexer) quoted(i int, backslash bool) int {
	s, q := l.src, l.src[i]
	for j := i + 1; j < len(s); j++ {
		switch {
		case backslash && s[j] == '\\':
			j++
		case s[j] == q && j+1 < len(s) && s[j+1] == q:
			j++
		case s[j] == q:
			return j + 1
		}
	}
	return len(s)
}

// blockComment returns the end of the comment starting at i. PostgreSQL nests them; MySQL does not.
func (l *lexer) blockComment(i int) int {
	s, depth := l.src, 0
	for j := i; j+1 < len(s); j++ {
		switch {
		case s[j] == '/' && s[j+1] == '*' && (depth == 0 || !l.mysql):
			depth++
			j++
		case s[j] == '*' && s[j+1] == '/':
			depth--
			j++
			if depth == 0 {
				return j + 1
			}
		}
	}
	return len(s)
}

// dollar returns the end of the dollar-quoted body starting at i, or i+1 when the $ starts none.
func (l *lexer) dollar(i int) int {
	s := l.src
	j := l.span(i+1, nameRune)
	if j < len(s) && s[j] == '$' && (j == i+1 || s[i+1] < '0' || s[i+1] > '9') {
		tag := s[i : j+1]
		if n := strings.Index(s[j+1:], tag); n >= 0 {
			return j + 1 + n + len(tag)
		}
		return len(s)
	}
	return i + 1
}

func nameRune(c rune) bool {
	return c == '_' || unicode.IsLetter(c) || unicode.IsDigit(c)
}

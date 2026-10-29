// Package placeholder turns the :name parameters of a statement written by hand into the placeholders of its
// database. It does not parse SQL: it steps over what may hold a colon that starts no parameter.
package placeholder

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-kanna/orm/internal/snapshot"
)

// Error is a mistake in a statement, at a line and a column of its text, both counted from 1.
type Error struct {
	Line, Column int
	Message      string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%d:%d: %s", e.Line, e.Column, e.Message)
}

// Rewrite replaces each :name in sql with a placeholder of dialect: on PostgreSQL $1, $2, and so on, one number for
// each name however often it is used, and on MySQL a ? for each use. It returns the names in the order of the
// placeholders.
//
// A : after another :, a letter, a digit, _, or ] starts no parameter, so that a cast such as ::int and a slice such as
// arr[1:n] are left alone, as is anything in a string, a quoted name, a comment, or a dollar-quoted body.
//
// The ; that ends the statement goes, with the space and the comments after it, so that the statement can be prepared
// or wrapped in another as it is.
func Rewrite(sql string, dialect snapshot.Dialect) (string, []string, error) {
	r := rewriter{sql: sql, mysql: dialect == snapshot.MySQL, numbers: map[string]int{}, end: -1}
	if err := r.run(); err != nil {
		return "", nil, err
	}
	out := r.out.String()
	if r.end >= 0 && !r.after {
		out = out[:r.end]
	}
	return strings.TrimRightFunc(out, unicode.IsSpace), r.names, nil
}

type rewriter struct {
	sql     string
	mysql   bool
	out     strings.Builder
	names   []string
	numbers map[string]int

	// end is where the last ; was written, or -1, and after reports whether anything but space and comments
	// followed it.
	end   int
	after bool
}

func (r *rewriter) run() error {
	s := r.sql
	for i := 0; i < len(s); {
		var (
			end  int
			err  error
			code = true
		)
		switch c := s[i]; {
		case c == '\'':
			end, err = r.quoted(i, "a string", r.mysql || r.escapeString(i))
		case c == '"' && r.mysql:
			end, err = r.quoted(i, "a string", true)
		case c == '"', c == '`' && r.mysql:
			end, err = r.quoted(i, "a quoted name", false)
		case c == '-' && r.lineComment(i), c == '#' && r.mysql:
			end, code = r.lineEnd(i), false
		case strings.HasPrefix(s[i:], "/*"):
			end, err = r.blockComment(i)
			code = false
		case c == '$' && !r.mysql:
			end, err = r.dollar(i)
		case c == '?' && r.mysql:
			err = r.errorAt(i, "? is a placeholder of MySQL's own: write the parameter as :name")
		case c == ':' && r.startsParameter(i):
			r.after = true
			i = r.parameter(i)
			continue
		case c == ';':
			r.end, r.after = r.out.Len(), false
			end, code = i+1, false
		case c == ' ', c == '\t', c == '\n', c == '\r':
			end, code = i+1, false
		default:
			end = i + 1
		}
		if err != nil {
			return err
		}
		r.after = r.after || code
		r.out.WriteString(s[i:end])
		i = end
	}
	return nil
}

// quoted returns the end of the quoted text starting at i: a quote is escaped by doubling it, and by a backslash too
// where backslash says so.
func (r *rewriter) quoted(i int, what string, backslash bool) (int, error) {
	s, q := r.sql, r.sql[i]
	for j := i + 1; j < len(s); j++ {
		switch {
		case backslash && s[j] == '\\':
			j++
		case s[j] == q && j+1 < len(s) && s[j+1] == q:
			j++
		case s[j] == q:
			return j + 1, nil
		}
	}
	return 0, r.errorAt(i, what+" that starts here does not end")
}

// escapeString reports whether the quote at i starts a PostgreSQL string with backslash escapes: E'...'.
func (r *rewriter) escapeString(i int) bool {
	if i == 0 || (r.sql[i-1] != 'E' && r.sql[i-1] != 'e') {
		return false
	}
	return !identifier(lastRune(r.sql[:i-1]))
}

// lineComment reports whether i starts a comment that runs to the end of the line. MySQL takes -- for one only when
// a space or a control character follows.
func (r *rewriter) lineComment(i int) bool {
	s := r.sql
	if !strings.HasPrefix(s[i:], "--") {
		return false
	}
	return !r.mysql || i+2 == len(s) || s[i+2] <= ' '
}

func (r *rewriter) lineEnd(i int) int {
	if n := strings.IndexByte(r.sql[i:], '\n'); n >= 0 {
		return i + n
	}
	return len(r.sql)
}

// blockComment returns the end of the comment starting at i. PostgreSQL nests them; MySQL does not.
func (r *rewriter) blockComment(i int) (int, error) {
	s, depth := r.sql, 0
	for j := i; j+1 < len(s); j++ {
		switch {
		case s[j] == '/' && s[j+1] == '*' && (depth == 0 || !r.mysql):
			depth++
			j++
		case s[j] == '*' && s[j+1] == '/':
			depth--
			j++
			if depth == 0 {
				return j + 1, nil
			}
		}
	}
	return 0, r.errorAt(i, "a comment that starts here does not end")
}

// dollar returns the end of the dollar-quoted body starting at i, or i+1 when the $ starts none.
func (r *rewriter) dollar(i int) (int, error) {
	s := r.sql
	if identifier(lastRune(s[:i])) {
		return i + 1, nil
	}
	j := i + 1
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j > i+1 {
		return 0, r.errorAt(i, s[i:j]+" is a placeholder of PostgreSQL's own: write the parameter as :name")
	}
	for j < len(s) {
		c, size := utf8.DecodeRuneInString(s[j:])
		if !identifier(c) {
			break
		}
		j += size
	}
	if j == len(s) || s[j] != '$' {
		return i + 1, nil
	}
	tag := s[i : j+1]
	n := strings.Index(s[j+1:], tag)
	if n < 0 {
		return 0, r.errorAt(i, "a dollar-quoted body that starts here does not end")
	}
	return j + 1 + n + len(tag), nil
}

func (r *rewriter) startsParameter(i int) bool {
	next, _ := utf8.DecodeRuneInString(r.sql[i+1:])
	if next != '_' && !unicode.IsLetter(next) {
		return false
	}
	prev := lastRune(r.sql[:i])
	return prev != ':' && prev != ']' && !identifier(prev)
}

// parameter writes the placeholder of the parameter at i and returns its end.
func (r *rewriter) parameter(i int) int {
	s, end := r.sql, i+1
	for end < len(s) {
		c, size := utf8.DecodeRuneInString(s[end:])
		if !identifier(c) {
			break
		}
		end += size
	}
	name := s[i+1 : end]

	if r.mysql {
		r.names = append(r.names, name)
		r.out.WriteByte('?')
		return end
	}
	n, ok := r.numbers[name]
	if !ok {
		r.names = append(r.names, name)
		n = len(r.names)
		r.numbers[name] = n
	}
	r.out.WriteString("$" + strconv.Itoa(n))
	return end
}

func (r *rewriter) errorAt(i int, message string) error {
	before := r.sql[:i]
	line := strings.Count(before, "\n") + 1
	column := utf8.RuneCountInString(before[strings.LastIndexByte(before, '\n')+1:]) + 1
	return &Error{Line: line, Column: column, Message: message}
}

func identifier(c rune) bool {
	return c == '_' || unicode.IsLetter(c) || unicode.IsDigit(c)
}

// lastRune returns the last rune of s, or utf8.RuneError when s is empty.
func lastRune(s string) rune {
	c, _ := utf8.DecodeLastRuneInString(s)
	return c
}

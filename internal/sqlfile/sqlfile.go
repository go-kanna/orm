// Package sqlfile reads the statements a .sql file holds, each under a -- query: or a -- scope: line.
package sqlfile

import (
	"go/token"
	"regexp"
	"strings"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/suggest"
)

// Kind tells a query from a scope.
type Kind int

const (
	Query Kind = iota
	Scope
)

func (k Kind) String() string {
	if k == Scope {
		return "scope"
	}
	return "query"
}

// Statement is a statement of a .sql file.
type Statement struct {
	Kind Kind
	// Type is the scope type a scope is a method of; it is empty for a query.
	Type string
	// Name is the name of the query, or of the scope's method.
	Name string
	// Params is the parameter list of a scope as its line writes it, between the parentheses.
	Params string
	// Text is the statement, without the blank lines around it.
	Text string
	// Header is where the -- query: or -- scope: line is, and Line the line Text starts on.
	Header token.Position
	Line   int
}

var (
	header = regexp.MustCompile(`^--\s*([A-Za-z]+):\s*(.*?)\s*$`)
	scope  = regexp.MustCompile(`^(\w+)\.(\w+)\((.*)\)$`)
)

// Parse reads the statements of the .sql file at path, data being what it holds. A file without a -- query: or a
// -- scope: line holds none of kanna-orm's, and is no mistake.
func Parse(path string, data []byte) ([]Statement, []diag.Diag) {
	var (
		stmts []Statement
		ds    []diag.Diag
		body  []string
		start int
		seen  = map[string]bool{}
	)
	flush := func() {
		if len(stmts) == 0 {
			return
		}
		st := &stmts[len(stmts)-1]
		lead := 0
		for lead < len(body) && strings.TrimSpace(body[lead]) == "" {
			lead++
		}
		st.Text = strings.TrimSpace(strings.Join(body, "\n"))
		st.Line = start + lead
		if st.Text == "" && st.Name != "" {
			ds = append(ds, diag.Errorf(st.Header, "-- %s: %s holds no statement", st.Kind, st.qualified()))
		}
	}

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for i, line := range lines {
		pos := token.Position{Filename: path, Line: i + 1, Column: 1}
		st, ok, d := parseHeader(pos, line)
		if d != nil {
			ds = append(ds, *d)
		}
		if !ok {
			body = append(body, line)
			continue
		}
		flush()
		if key := st.Kind.String() + " " + st.qualified(); seen[key] && st.Name != "" {
			ds = append(ds, diag.Errorf(pos, "-- %s: %s is written a second time", st.Kind, st.qualified()))
		} else {
			seen[key] = true
		}
		stmts = append(stmts, st)
		body, start = nil, i+2
	}
	if len(stmts) == 0 {
		return nil, ds
	}
	flush()

	for i, line := range lines {
		if _, ok, _ := parseHeader(token.Position{}, line); ok {
			break
		}
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "--") {
			ds = append(ds, diag.Errorf(token.Position{Filename: path, Line: i + 1, Column: 1},
				"a statement comes before any -- query: or -- scope: line"))
			break
		}
	}
	return stmts, ds
}

// parseHeader reads line as a -- query: or a -- scope: line, if it is one. A comment that looks like a misspelled one
// is a warning, and stays the comment it is.
func parseHeader(pos token.Position, line string) (Statement, bool, *diag.Diag) {
	m := header.FindStringSubmatch(strings.TrimRight(line, "\r"))
	if m == nil {
		return Statement{}, false, nil
	}
	word, rest := m[1], m[2]
	switch word {
	case "query":
		if !token.IsIdentifier(rest) {
			d := diag.Errorf(pos, "-- query: wants the name of its method").
				WithHints("as in -- query: MonthCounts")
			return Statement{Kind: Query, Header: pos}, true, &d
		}
		return Statement{Kind: Query, Name: rest, Header: pos}, true, nil
	case "scope":
		s := scope.FindStringSubmatch(rest)
		if s == nil || !token.IsIdentifier(s[1]) || !token.IsIdentifier(s[2]) {
			d := diag.Errorf(pos, "-- scope: wants the method it is, with its parameters").
				WithHints("as in -- scope: Order.PlacedInMonth(at time.Time)")
			return Statement{Kind: Scope, Header: pos}, true, &d
		}
		return Statement{Kind: Scope, Type: s[1], Name: s[2], Params: strings.TrimSpace(s[3]), Header: pos}, true, nil
	}
	if near, ok := suggest.Closest(word, []string{"query", "scope"}); ok {
		d := diag.Warningf(pos, "-- %s: is not recognized, so the line is a comment", word).
			WithHints("did you mean -- " + near + ":?")
		return Statement{}, false, &d
	}
	return Statement{}, false, nil
}

func (s Statement) qualified() string {
	if s.Kind == Scope {
		return s.Type + "." + s.Name
	}
	return s.Name
}

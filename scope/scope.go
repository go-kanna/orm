// Package scope holds Of, the one field of a scope type.
package scope

import (
	"errors"
	"fmt"

	"github.com/go-kanna/orm/impl"
)

// Of is the scope of a query that reads Ms: its conditions, the order of its rows, and how many rows it keeps. A scope
// type holds one in an unexported field, so only its methods can narrow it. These are the methods kanna-orm writes and
// your own, which narrow it with those or with SQL through Raw.
type Of[M any] impl.Of[M]

// Raw returns q as a Raw scope, which you narrow or order with SQL written by hand. Nothing checks that SQL before it
// runs.
func (q Of[M]) Raw() Raw[M] { return Raw[M](q) }

// Raw is a scope that you narrow or order with SQL written by hand. Use it for a condition or order whose shape changes
// while the program runs, which a scope in a .sql file cannot express.
type Raw[M any] impl.Of[M]

// SQL marks a piece of SQL in the condition that Raw's Where takes. Every other part of the condition is a value.
type SQL string

// Where narrows the scope to the rows that satisfy a condition. Write the condition as pieces of SQL, each of type SQL,
// with the values they compare in between, as in Where(scope.SQL("status <> "), v) or
// Where(scope.SQL("date_trunc('month', placed_at) = date_trunc('month', "), at, scope.SQL(")")). Every part that is not
// of type SQL is a value, even a string. A condition with no SQL, or with two values and no SQL between them, is an
// error, and every query of the scope returns it.
func (r Raw[M]) Where(parts ...any) Of[M] {
	q := impl.Of[M](r)
	split := make([]any, 0, len(parts)+1)
	hasSQL, value := false, false
	for i, p := range parts {
		if s, ok := p.(SQL); ok {
			if !value && len(split) > 0 {
				split[len(split)-1] = split[len(split)-1].(string) + string(s) //nolint:forcetypeassert // SQL is a string
				continue
			}
			split = append(split, string(s))
			hasSQL, value = true, false
			continue
		}
		switch {
		case value:
			return Of[M](q.Fail(fmt.Errorf("orm: Raw().Where: parts %d and %d are values with no SQL between them "+
				"(SQL is marked as scope.SQL)", i-1, i)))
		case len(split) == 0:
			split = append(split, "")
		}
		split = append(split, p)
		value = true
	}
	if !hasSQL {
		return Of[M](q.Fail(errors.New("orm: Raw().Where without SQL (SQL is marked as scope.SQL)")))
	}
	return Of[M](q.Where(split...))
}

// OrderBy orders the rows by expr, a column or an SQL expression, after any order set earlier.
func (r Raw[M]) OrderBy(expr SQL, desc bool) Of[M] {
	return Of[M](impl.Of[M](r).OrderBy(string(expr), desc))
}

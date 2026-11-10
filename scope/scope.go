// Package scope holds Of, the one field of a scope type.
package scope

import (
	"errors"
	"fmt"

	"github.com/go-kanna/orm/impl"
)

// Of is the scope of a query reading Ms: its conditions, the order of its rows, and how many of them it keeps. A scope
// type holds one in an unexported field, so that nothing but its methods narrows it: those kanna-orm writes, and its
// own, which narrow it by those, or by SQL through Raw.
type Of[M any] impl.Of[M]

// Raw is q as SQL written by hand narrows or orders it, which nothing checks before it runs.
func (q Of[M]) Raw() Raw[M] { return Raw[M](q) }

// Raw is a scope that SQL written by hand narrows or orders: a condition or an order whose shape changes as the program
// runs, which a scope written in a .sql file cannot say.
type Raw[M any] impl.Of[M]

// SQL is a piece of SQL in the condition Raw's Where takes, which every other part of it is a value apart from.
type SQL string

// Where narrows to the rows a condition holds for. The condition is pieces of SQL, each marked as SQL, with the values
// they compare in between, as in Where(scope.SQL("status <> "), v) or
// Where(scope.SQL("date_trunc('month', placed_at) = date_trunc('month', "), at, scope.SQL(")")). Every part not of
// type SQL is a value, a string too. A condition without SQL, or with two values and no SQL between them, is an error,
// which every query of the scope returns.
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

// OrderBy orders the rows by expr, a column or an expression in SQL, after any order given before.
func (r Raw[M]) OrderBy(expr string, desc bool) Of[M] {
	return Of[M](impl.Of[M](r).OrderBy(expr, desc))
}

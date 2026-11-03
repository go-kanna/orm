// Package scope holds Of, the one field of a scope type.
package scope

import "github.com/go-kanna/orm/impl"

// Of is the scope of a query reading Ms: its conditions, the order of its rows, and how many of them it keeps. A scope
// type holds one in an unexported field, so that nothing but its methods narrows it: those kanna-orm writes, and its
// own, which narrow it by those, or by SQL through Raw.
type Of[M any] impl.Of[M]

// Raw is q as SQL written by hand narrows or orders it, which nothing checks before it runs.
func (q Of[M]) Raw() Raw[M] { return Raw[M](q) }

// Raw is a scope that SQL written by hand narrows or orders: a condition or an order whose shape changes as the program
// runs, which a scope written in a .sql file cannot say.
type Raw[M any] impl.Of[M]

// Where narrows to the rows a condition holds for. The condition is SQL with its arguments in between, as in
// Where("status <> ", v) or Where("date_trunc('month', placed_at) = date_trunc('month', ", at, ")").
func (r Raw[M]) Where(parts ...any) Of[M] { return Of[M](impl.Of[M](r).Where(parts...)) }

// OrderBy orders the rows by expr, a column or an expression in SQL, after any order given before.
func (r Raw[M]) OrderBy(expr string, desc bool) Of[M] {
	return Of[M](impl.Of[M](r).OrderBy(expr, desc))
}

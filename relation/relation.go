// Package relation holds what the closures given to the SelectX and PreloadX methods of a scope type return and
// read: the related rows selected, and the columns of the parent row they are correlated by.
package relation

import "github.com/go-kanna/orm/impl"

type (
	// Single selects zero or one related row; Required makes no row an error.
	Single[M any] = impl.Single[M]
	// Many selects the related rows of each parent.
	Many[M any] = impl.Many[M]
	// Column is a column of the parent row a related query is correlated by.
	Column[T any] = impl.Column[T]
)

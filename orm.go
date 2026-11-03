// Package orm is what code written by hand uses of kanna-orm: what a relation is selected by, the options of a
// repository, and the errors a repository returns. A scope type holds a scope.Of, and what the generated code runs
// on is in package impl.
package orm

import (
	"errors"

	"github.com/go-kanna/orm/impl"
)

// ErrNotFound is what a repository returns, wrapped, for a row that is not there.
var ErrNotFound = errors.New("orm: not found")

// ErrRequiredRelation means a required selection returned no value.
var ErrRequiredRelation = impl.ErrRequiredRelation

type (
	// Single selects zero or one related row; Required makes no row an error.
	Single[M any] = impl.Single[M]
	// Many selects the related rows of each parent.
	Many[M any] = impl.Many[M]
	// Column is a column of the parent row a related query is correlated by.
	Column[T any] = impl.Column[T]
	// Option configures a generated repository.
	Option = impl.Option
	// QueryLog describes one completed SQL statement, including reading its results.
	QueryLog = impl.QueryLog
	// QueryLogger receives each statement once.
	QueryLogger = impl.QueryLogger
)

// WithQueryLogger has a repository report each statement it runs to logger.
func WithQueryLogger(logger QueryLogger) Option { return impl.WithQueryLogger(logger) }

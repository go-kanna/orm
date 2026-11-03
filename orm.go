// Package orm is what code written by hand uses of kanna-orm: the options of a repository and the errors it returns.
// A scope type holds a scope.Of, a relation is selected by the types of package relation, and what the generated code
// runs on is in package impl.
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
	// Option configures a generated repository.
	Option = impl.Option
	// QueryLog describes one completed SQL statement, including reading its results.
	QueryLog = impl.QueryLog
	// QueryLogger receives each statement once.
	QueryLogger = impl.QueryLogger
)

// WithQueryLogger has a repository report each statement it runs to logger.
func WithQueryLogger(logger QueryLogger) Option { return impl.WithQueryLogger(logger) }

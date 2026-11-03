// Package option holds the options a generated repository's constructor takes.
package option

import "github.com/go-kanna/orm/impl"

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

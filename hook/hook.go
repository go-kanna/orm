// Package hook holds what a generated repository calls around each statement it runs: a Before as it starts, and an
// After once it is done. It depends on no logging or tracing library; what the hooks do is yours.
package hook

import (
	"context"
	"time"
)

// Query is one SQL statement about to run: its text, with placeholders, and their arguments. Args are borrowed: do not
// modify them, and copy those you keep after the call.
type Query struct {
	SQL  string
	Args []any
}

// Execution is a statement that has run, including reading its results. Err is the error of running or reading it,
// before the repository translates it. A query a method written by hand runs on database/sql, whose *sql.Rows and
// *sql.Row nothing can read through, has run as QueryContext or QueryRowContext returns, before its rows are read.
type Execution struct {
	Query

	Duration time.Duration
	Err      error
}

// Hook is a pair of functions a repository calls around each statement it runs. Either may be nil. Of the hooks a
// repository is given, those given first enclose the others: their Before runs first, and their After last. Calls from
// concurrent statements may overlap.
type Hook struct {
	// Before is called as a statement starts. It returns ctx, or a context derived from it: the statement runs with that
	// context, its cancellation and deadline included, and the next Before receives it.
	Before func(ctx context.Context, q Query) context.Context
	// After is called once the statement has run and its results have been read, or it has failed. It receives the
	// context its own Before returned, or, without a Before, the one that Before would have received.
	After func(ctx context.Context, e Execution)
}

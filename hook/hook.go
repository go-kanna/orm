// Package hook holds the functions that a generated repository calls around each statement it runs: Before when the
// statement starts and After when it is done. It depends on no logging or tracing library. What the hooks do is up to
// you.
package hook

import (
	"context"
	"time"
)

// Query is an SQL statement that is about to run: its text, with placeholders, and their arguments. Args are borrowed.
// Do not modify them, and copy any you keep after the call.
type Query struct {
	SQL  string
	Args []any
}

// Execution is a statement that has run, including reading its results. Err is the error from running or reading it,
// before the repository translates it. For a query that a hand-written method runs on database/sql, nothing can see
// through *sql.Rows or *sql.Row, so the statement counts as run when QueryContext or QueryRowContext returns, before
// its rows are read.
type Execution struct {
	Query

	Duration time.Duration
	Err      error
}

// Hook is a pair of functions that a repository calls around each statement it runs. Either may be nil. Hooks given
// first enclose the ones given later: their Before runs first and their After runs last. Calls from concurrent
// statements may overlap.
type Hook struct {
	// Before is called when a statement starts. It returns ctx or a context derived from it. The statement runs with
	// that context, including its cancellation and deadline, and the next Before receives it.
	Before func(ctx context.Context, q Query) context.Context
	// After is called once the statement has run and its results have been read, or has failed. It receives the context
	// that its own Before returned. If there is no Before, it receives the context that Before would have received.
	After func(ctx context.Context, e Execution)
}

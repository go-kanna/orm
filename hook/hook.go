// Package hook holds the functions a generated repository calls around each statement it runs: Before as it starts,
// After once it is done. They depend on no logging or tracing library; what they do is yours.
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
// before the repository translates it.
type Execution struct {
	Query

	Duration time.Duration
	Err      error
}

// Before is called as a statement starts. The statement runs with the context it returns, which After then receives:
// return ctx itself to leave it as it was. Calls from concurrent statements may overlap.
type Before func(ctx context.Context, q Query) context.Context

// After is called once a statement has run and its results have been read, or it has failed. Calls from concurrent
// statements may overlap.
type After func(ctx context.Context, e Execution)

package impl

import (
	"context"
	"slices"
	"time"

	"github.com/go-kanna/orm/hook"
)

// Options configures generated repositories: the hooks each statement they run is called between.
type Options struct {
	Before []hook.Before
	After  []hook.After
}

// Option is one setting of Options.
type Option func(*Options)

// WithBefore adds f to the hooks called as each statement starts, after those added before it. A nil f adds none.
func WithBefore(f hook.Before) Option {
	return func(o *Options) {
		if f != nil {
			o.Before = append(slices.Clip(o.Before), f)
		}
	}
}

// WithAfter adds f to the hooks called once each statement is done, before those added before it, so that the first
// hooks added enclose the others. A nil f adds none.
func WithAfter(f hook.After) Option {
	return func(o *Options) {
		if f != nil {
			o.After = append(slices.Clip(o.After), f)
		}
	}
}

func NewOptions(opts ...Option) Options {
	var o Options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Option is the option giving a repository o, as Bind gives the repository it returns those of its receiver.
func (o Options) Option() Option { return func(p *Options) { *p = o } }

// QueryTrace is used by generated repositories to observe one statement.
// A nil trace is disabled and never reads the clock.
type QueryTrace struct {
	complete func(error)
	finished bool
}

// StartQuery calls the Before hooks of o for a statement of sql and args. It returns the context the statement runs
// with, and the trace calling the After hooks once the statement ends, nil without any.
func (o Options) StartQuery(ctx context.Context, sql string, args []any) (context.Context, *QueryTrace) {
	q := hook.Query{SQL: sql, Args: args}
	for _, f := range o.Before {
		ctx = f(ctx, q) //nolint:fatcontext // each Before wraps the context the one before it returned, once a statement
	}
	if len(o.After) == 0 {
		return ctx, nil
	}
	started := time.Now()
	return ctx, &QueryTrace{complete: func(err error) {
		e := hook.Execution{Query: q, Duration: time.Since(started), Err: err}
		for _, f := range slices.Backward(o.After) {
			f(ctx, e)
		}
	}}
}

func (q *QueryTrace) End(err error) {
	if q == nil || q.finished {
		return
	}
	q.finished = true
	q.complete(err)
}

// Row and Rows are the result operations used by generated repositories.
type Row interface {
	Scan(dest ...any) error
}

type Rows interface {
	Row
	Next() bool
	Err() error
	Close()
}

func (q *QueryTrace) Row(row Row) Row {
	if q == nil {
		return row
	}
	return tracedRow{Row: row, trace: q}
}

func (q *QueryTrace) Rows(rows Rows, err error) (Rows, error) {
	if err != nil {
		q.End(err)
		return nil, err
	}
	if q == nil {
		return rows, nil
	}
	return tracedRows{Rows: rows, trace: q}, nil
}

type tracedRow struct {
	Row

	trace *QueryTrace
}

func (r tracedRow) Scan(dest ...any) error {
	err := r.Row.Scan(dest...)
	r.trace.End(err)
	return err //nolint:wrapcheck // preserve the driver's error for repository translation
}

type tracedRows struct {
	Rows

	trace *QueryTrace
}

func (r tracedRows) Next() bool {
	next := r.Rows.Next()
	if !next {
		r.trace.End(r.Err())
	}
	return next
}

func (r tracedRows) Scan(dest ...any) error {
	err := r.Rows.Scan(dest...)
	if err != nil {
		r.trace.End(err)
	}
	return err //nolint:wrapcheck // preserve the driver's error for repository translation
}

func (r tracedRows) Close() {
	r.Rows.Close()
	r.trace.End(r.Err())
}

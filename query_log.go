package orm

import (
	"context"
	"time"
)

// QueryLog describes one completed SQL statement, including reading its results.
type QueryLog struct {
	SQL      string
	Args     []any
	Duration time.Duration
	Err      error
}

// QueryLogger receives each statement once. Calls from concurrent queries may overlap.
// Args must not be modified or retained after the callback returns without copying.
type QueryLogger func(context.Context, QueryLog)

// Options configures generated repositories.
type Options struct {
	QueryLogger QueryLogger
}

type Option func(*Options)

func WithQueryLogger(logger QueryLogger) Option {
	return func(o *Options) { o.QueryLogger = logger }
}

func NewOptions(opts ...Option) Options {
	var o Options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// QueryTrace is used by generated repositories to observe one statement.
// A nil trace is disabled and never reads the clock.
type QueryTrace struct {
	complete func(error)
	finished bool
}

func (o Options) StartQuery(ctx context.Context, sql string, args []any) *QueryTrace {
	if o.QueryLogger == nil {
		return nil
	}
	started := time.Now()
	return &QueryTrace{complete: func(err error) {
		o.QueryLogger(ctx, QueryLog{SQL: sql, Args: args, Duration: time.Since(started), Err: err})
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

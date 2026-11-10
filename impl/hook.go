package impl

import (
	"context"
	"slices"
	"time"

	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/hook"
)

// Options configures generated repositories: the hooks each statement they run is called between, the first given
// first, and the clock they write the creation and update times of rows by.
type Options struct {
	hooks []hook.Hook
	clock orm.Clocker
}

// Option is one setting of Options, which option.Option is defined by. Its zero value sets nothing.
type Option struct{ apply func(*Options) }

// anyOption is Option, or a type defined by it, as option.Option.
type anyOption interface {
	~struct{ apply func(*Options) }
}

// WithHook adds h to the hooks, inside those added before it. A hook of neither function adds none.
func WithHook(h hook.Hook) Option {
	return Option{func(o *Options) {
		if h.Before != nil || h.After != nil {
			o.hooks = append(slices.Clip(o.hooks), h)
		}
	}}
}

// WithClock sets the clock to c, in place of one set before. A nil c leaves time.Now.
func WithClock(c orm.Clocker) Option {
	return Option{func(o *Options) { o.clock = c }}
}

// Join is opts as one option, setting each in their order.
func Join[O anyOption](opts ...O) Option {
	opts = slices.Clone(opts)
	return Option{func(o *Options) { set(o, opts) }}
}

func NewOptions[O anyOption](opts ...O) Options {
	var o Options
	set(&o, opts)
	return o
}

func set[O anyOption](o *Options, opts []O) {
	for _, opt := range opts {
		if apply := Option(opt).apply; apply != nil {
			apply(o)
		}
	}
}

// Option is the option giving a repository o, as Bind gives the repository it returns those of its receiver.
func (o Options) Option() Option { return Option{func(p *Options) { *p = o }} }

// Now is the time of the clock, or time.Now without one, in UTC, so that a column keeping no time zone keeps the same
// time whatever zone the clock tells it in.
func (o Options) Now(ctx context.Context) time.Time {
	if o.clock == nil {
		return time.Now().UTC()
	}
	return o.clock.Now(ctx).UTC()
}

// QueryTrace is used by generated repositories to observe one statement.
// A nil trace is disabled and never reads the clock.
type QueryTrace struct {
	complete func(error)
	finished bool
}

// StartQuery calls the Before of each hook of o for a statement of sql and args, in order. It returns the context the
// statement runs with, the one the last Before returned, and the trace calling each After once the statement ends, in
// reverse, with the context its own Before returned. The trace is nil without any After.
func (o Options) StartQuery(ctx context.Context, sql string, args []any) (context.Context, *QueryTrace) {
	q := hook.Query{SQL: sql, Args: args}
	contexts := make([]context.Context, len(o.hooks))
	after := false
	for i, h := range o.hooks {
		if h.Before != nil {
			ctx = h.Before(ctx, q) //nolint:fatcontext // each Before wraps the context of the one before it, once a statement
		}
		contexts[i] = ctx
		after = after || h.After != nil
	}
	if !after {
		return ctx, nil
	}
	started := time.Now()
	return ctx, &QueryTrace{complete: func(err error) {
		e := hook.Execution{Query: q, Duration: time.Since(started), Err: err}
		for i, h := range slices.Backward(o.hooks) {
			if h.After != nil {
				h.After(contexts[i], e)
			}
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

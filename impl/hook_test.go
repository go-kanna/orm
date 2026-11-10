package impl_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-kanna/orm/hook"
	"github.com/go-kanna/orm/impl"
)

func TestStartQuery_Disabled(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	got, trace := impl.NewOptions(impl.WithHook(hook.Hook{})).StartQuery(ctx, "SELECT 1", nil)
	if got != ctx || trace != nil {
		t.Fatal("no hooks changed the context or started a trace")
	}
	trace.End(errors.New("not reported"))
}

func TestStartQuery_Completion(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("database failed")
	for _, how := range []string{"exhausted", "closed", "scan error", "iteration error", "query error", "single row"} {
		t.Run(how, func(t *testing.T) {
			t.Parallel()
			var done []hook.Execution
			ctx := t.Context()
			o := impl.NewOptions(impl.WithHook(hook.Hook{After: func(got context.Context, e hook.Execution) {
				if got != ctx {
					t.Error("query context was not preserved")
				}
				done = append(done, e)
			}}))
			_, trace := o.StartQuery(ctx, "SELECT $1", []any{int64(42)})
			var want error
			switch how {
			case "query error":
				_, _ = trace.Rows(nil, sentinel)
				want = sentinel
			case "single row":
				_ = trace.Row(testRows{}).Scan()
			default:
				inner := testRows{}
				if how == "scan error" {
					inner.scanErr, want = sentinel, sentinel
				}
				if how == "iteration error" {
					inner.err, want = sentinel, sentinel
				}
				rows, err := trace.Rows(inner, nil)
				if err != nil || len(done) != 0 {
					t.Fatal("After ran before the results were consumed")
				}
				if how == "scan error" {
					_ = rows.Scan()
				} else if how != "closed" {
					rows.Next()
				}
				rows.Close()
			}
			trace.End(nil)
			if len(done) != 1 {
				t.Fatalf("After ran %d times, want once", len(done))
			}
			e := done[0]
			if e.SQL != "SELECT $1" || len(e.Args) != 1 || e.Args[0] != int64(42) || e.Duration <= 0 ||
				!errors.Is(e.Err, want) {
				t.Fatalf("wrong execution: %+v", e)
			}
		})
	}
}

type contextKey string

func TestStartQuery_Pairs(t *testing.T) {
	t.Parallel()
	var calls []string
	pair := func(name string) impl.Option {
		return impl.WithHook(hook.Hook{
			Before: func(ctx context.Context, q hook.Query) context.Context {
				if q.SQL != "SELECT 1" {
					t.Errorf("Before %s got %q", name, q.SQL)
				}
				calls = append(calls, "before "+name+" sees "+seen(ctx))
				return context.WithValue(ctx, contextKey(name), true)
			},
			After: func(ctx context.Context, _ hook.Execution) {
				calls = append(calls, "after "+name+" sees "+seen(ctx))
			},
		})
	}
	afterAlone := impl.WithHook(hook.Hook{After: func(ctx context.Context, _ hook.Execution) {
		calls = append(calls, "after alone sees "+seen(ctx))
	}})
	o := impl.NewOptions(pair("a"), afterAlone, pair("b"))
	ctx, trace := o.StartQuery(t.Context(), "SELECT 1", nil)
	if seen(ctx) != "a b" {
		t.Errorf("the statement runs with a context of %q, want that of every Before", seen(ctx))
	}
	trace.End(nil)
	want := []string{"before a sees ", "before b sees a", "after b sees a b", "after alone sees a", "after a sees a"}
	if !slices.Equal(calls, want) {
		t.Errorf("calls =\n%q\nwant\n%q", calls, want)
	}
}

// seen names the Before hooks whose values ctx carries.
func seen(ctx context.Context) string {
	var names []string
	for _, name := range []string{"a", "b"} {
		if ctx.Value(contextKey(name)) != nil {
			names = append(names, name)
		}
	}
	return strings.Join(names, " ")
}

func TestStartQuery_BeforeAlone(t *testing.T) {
	t.Parallel()
	o := impl.NewOptions(impl.WithHook(hook.Hook{Before: func(ctx context.Context, _ hook.Query) context.Context {
		return context.WithValue(ctx, contextKey("a"), true)
	}}))
	ctx, trace := o.StartQuery(t.Context(), "SELECT 1", nil)
	if seen(ctx) != "a" || trace != nil {
		t.Fatal("Before alone did not give its context, or started a trace")
	}
}

func TestOptions_Option(t *testing.T) {
	t.Parallel()
	n := 0
	counted := impl.WithHook(hook.Hook{After: func(context.Context, hook.Execution) { n++ }})
	afters := func(o impl.Options) int {
		n = 0
		_, trace := o.StartQuery(t.Context(), "SELECT 1", nil)
		trace.End(nil)
		return n
	}
	o := impl.NewOptions(counted)
	more := impl.NewOptions(o.Option(), counted)
	if a, b := afters(o), afters(more); a != 1 || b != 2 {
		t.Fatalf("Option gave %d hooks and kept %d of its own, want 2 and 1", b, a)
	}
}

func TestJoin(t *testing.T) {
	t.Parallel()
	var calls []string
	named := func(name string) impl.Option {
		return impl.WithHook(hook.Hook{Before: func(ctx context.Context, _ hook.Query) context.Context {
			calls = append(calls, name)
			return ctx
		}})
	}
	opts := []impl.Option{named("b"), named("c")}
	joined := impl.Join(opts...)
	opts[0] = named("x")
	o := impl.NewOptions(named("a"), joined, impl.Join[impl.Option](), impl.Option{}, named("d"))
	o.StartQuery(t.Context(), "SELECT 1", nil)
	if want := []string{"a", "b", "c", "d"}; !slices.Equal(calls, want) {
		t.Errorf("Befores ran as %q, want %q", calls, want)
	}
}

type fixedClock time.Time

func (c fixedClock) Now(context.Context) time.Time { return time.Time(c) }

func TestOptions_Now(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	for _, o := range []impl.Options{impl.NewOptions[impl.Option](), impl.NewOptions(impl.WithClock(nil))} {
		if now := o.Now(ctx); time.Since(now).Abs() > time.Minute || now.Location() != time.UTC {
			t.Errorf("Now with no clock = %v, want time.Now in UTC", now)
		}
	}
	jst := time.FixedZone("JST", 9*60*60)
	first, last := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 2, 1, 9, 0, 0, 0, jst)
	o := impl.NewOptions(impl.WithClock(fixedClock(first)), impl.WithClock(fixedClock(last)))
	if now := o.Now(ctx); !now.Equal(last) || now.Location() != time.UTC {
		t.Errorf("Now = %v, want %v of the clock given last, in UTC", now, last)
	}
	if now := impl.NewOptions(o.Option()).Now(ctx); !now.Equal(last) {
		t.Errorf("Now of the options Option gives = %v, want %v, as Bind keeps the clock", now, last)
	}
}

type testRows struct{ scanErr, err error }

func (r testRows) Scan(...any) error { return r.scanErr }
func (r testRows) Next() bool        { return false }
func (r testRows) Err() error        { return r.err }
func (r testRows) Close()            {}

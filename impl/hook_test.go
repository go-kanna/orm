package impl_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/go-kanna/orm/hook"
	"github.com/go-kanna/orm/impl"
)

func TestStartQuery_Disabled(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	got, trace := impl.NewOptions(impl.WithBefore(nil), impl.WithAfter(nil)).StartQuery(ctx, "SELECT 1", nil)
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
			o := impl.NewOptions(impl.WithAfter(func(got context.Context, e hook.Execution) {
				if got != ctx {
					t.Error("query context was not preserved")
				}
				done = append(done, e)
			}))
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

func TestStartQuery_Order(t *testing.T) {
	t.Parallel()
	var calls []string
	before := func(name string) impl.Option {
		return impl.WithBefore(func(ctx context.Context, q hook.Query) context.Context {
			if q.SQL != "SELECT 1" {
				t.Errorf("Before %s got %q", name, q.SQL)
			}
			calls = append(calls, "before "+name+" sees "+seen(ctx))
			return context.WithValue(ctx, contextKey(name), true)
		})
	}
	after := func(name string) impl.Option {
		return impl.WithAfter(func(ctx context.Context, _ hook.Execution) {
			calls = append(calls, "after "+name+" sees "+seen(ctx))
		})
	}
	o := impl.NewOptions(before("a"), after("a"), before("b"), after("b"))
	ctx, trace := o.StartQuery(t.Context(), "SELECT 1", nil)
	if seen(ctx) != "a b" {
		t.Errorf("the statement runs with a context of %q, want that of every Before", seen(ctx))
	}
	trace.End(nil)
	want := []string{"before a sees ", "before b sees a", "after b sees a b", "after a sees a b"}
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
	o := impl.NewOptions(impl.WithBefore(func(ctx context.Context, _ hook.Query) context.Context {
		return context.WithValue(ctx, contextKey("a"), true)
	}))
	ctx, trace := o.StartQuery(t.Context(), "SELECT 1", nil)
	if seen(ctx) != "a" || trace != nil {
		t.Fatal("Before alone did not give its context, or started a trace")
	}
}

func TestOptions_Option(t *testing.T) {
	t.Parallel()
	noop := func(context.Context, hook.Execution) {}
	o := impl.NewOptions(impl.WithAfter(noop))
	more := impl.NewOptions(o.Option(), impl.WithAfter(noop))
	if len(o.After) != 1 || len(more.After) != 2 {
		t.Fatalf("Option gave %d hooks and kept %d of its own, want 2 and 1", len(more.After), len(o.After))
	}
}

type testRows struct{ scanErr, err error }

func (r testRows) Scan(...any) error { return r.scanErr }
func (r testRows) Next() bool        { return false }
func (r testRows) Err() error        { return r.err }
func (r testRows) Close()            {}

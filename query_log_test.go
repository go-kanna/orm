package orm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-kanna/orm"
)

func TestQueryTrace_Disabled(t *testing.T) {
	t.Parallel()
	trace := orm.NewOptions().StartQuery(t.Context(), "SELECT 1", nil)
	if trace != nil {
		t.Fatal("disabled logger started a trace")
	}
	trace.End(errors.New("not logged"))
}

func TestQueryTrace_Completion(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("database failed")
	for _, how := range []string{"exhausted", "closed", "scan error", "iteration error", "query error", "single row"} {
		t.Run(how, func(t *testing.T) {
			t.Parallel()
			var logs []orm.QueryLog
			ctx := t.Context()
			o := orm.NewOptions(orm.WithQueryLogger(func(got context.Context, q orm.QueryLog) {
				if got != ctx {
					t.Error("query context was not preserved")
				}
				logs = append(logs, q)
			}))
			trace := o.StartQuery(ctx, "SELECT $1", []any{int64(42)})
			var want error
			switch how {
			case "query error":
				_, _ = trace.Rows(nil, sentinel)
				want = sentinel
			case "single row":
				_ = trace.Row(testLogRows{}).Scan()
			default:
				inner := testLogRows{}
				if how == "scan error" {
					inner.scanErr, want = sentinel, sentinel
				}
				if how == "iteration error" {
					inner.err, want = sentinel, sentinel
				}
				rows, err := trace.Rows(inner, nil)
				if err != nil || len(logs) != 0 {
					t.Fatal("query was logged before its results were consumed")
				}
				if how == "scan error" {
					_ = rows.Scan()
				} else if how != "closed" {
					rows.Next()
				}
				rows.Close()
			}
			trace.End(nil)
			if len(logs) != 1 {
				t.Fatalf("logged %d times, want once", len(logs))
			}
			q := logs[0]
			if q.SQL != "SELECT $1" || len(q.Args) != 1 || q.Args[0] != int64(42) || q.Duration <= 0 ||
				!errors.Is(q.Err, want) {
				t.Fatalf("wrong event: %+v", q)
			}
		})
	}
}

type testLogRows struct{ scanErr, err error }

func (r testLogRows) Scan(...any) error { return r.scanErr }
func (r testLogRows) Next() bool        { return false }
func (r testLogRows) Err() error        { return r.err }
func (r testLogRows) Close()            {}

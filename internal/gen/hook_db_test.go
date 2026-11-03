package gen_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-kanna/orm/hook"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
	mysqlscopes "github.com/go-kanna/orm/internal/gen/testdata/mysql/scopes"
	mysqlstore "github.com/go-kanna/orm/internal/gen/testdata/mysql/sqlstore"
	"github.com/go-kanna/orm/internal/gen/testdata/postgres/pgxstore"
	pgscopes "github.com/go-kanna/orm/internal/gen/testdata/postgres/scopes"
	"github.com/go-kanna/orm/option"
)

func TestHooks_Postgres(t *testing.T) {
	t.Parallel()
	var done []hook.Execution
	repo := pgxstore.NewOrderRepository(pgxConn(t, postgresSchema(t)), captureExecutions(t, &done)...)
	ctx := t.Context()
	if _, err := repo.Get(ctx, 1, pgscopes.Orders().WithLines()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Count(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CloseOld(ctx, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertLine(ctx, model.OrderLine{OrderID: 1, LineNo: 1, SKU: "duplicate"}); err == nil {
		t.Fatal("duplicate key was accepted")
	}
	checkExecutions(t, done)
}

func TestHooks_MySQL(t *testing.T) {
	t.Parallel()
	var done []hook.Execution
	repo := mysqlstore.NewOrderRepository(mysqlDB(t), captureExecutions(t, &done)...)
	ctx := t.Context()
	if _, err := repo.Get(ctx, 1, mysqlscopes.Orders().WithLines()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Count(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CloseOld(ctx, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.InsertLine(ctx, model.OrderLine{OrderID: 1, LineNo: 1, SKU: "duplicate"}); err == nil {
		t.Fatal("duplicate key was accepted")
	}
	checkExecutions(t, done)
}

type hookKey struct{}

// captureExecutions has a repository mark the context of each statement before it runs, and record it after.
func captureExecutions(t *testing.T, done *[]hook.Execution) []option.Option {
	t.Helper()
	return []option.Option{
		option.WithBefore(func(ctx context.Context, q hook.Query) context.Context {
			return context.WithValue(ctx, hookKey{}, q.SQL)
		}),
		option.WithAfter(func(ctx context.Context, e hook.Execution) {
			if ctx.Value(hookKey{}) != e.SQL {
				t.Error("After did not get the context of Before")
			}
			e.Args = slices.Clone(e.Args)
			*done = append(*done, e)
		}),
	}
}

func checkExecutions(t *testing.T, done []hook.Execution) {
	t.Helper()
	if len(done) != 5 {
		t.Fatalf("got %d executions, want parent, preload, count, custom SQL, failed insert: %+v", len(done), done)
	}
	statements := []string{"FROM orders", "FROM order_lines", "count(*)", "UPDATE orders", "INSERT INTO order_lines"}
	for i, text := range statements {
		e := done[i]
		if !strings.Contains(e.SQL, text) || e.Duration <= 0 || (e.Err != nil) != (i == 4) {
			t.Errorf("execution %d: %+v", i, e)
		}
	}
	if len(done[0].Args) != 1 || done[0].Args[0] != model.OrderID(1) {
		t.Fatalf("query arguments: %+v", done[0].Args)
	}
}

package gen_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-kanna/orm/hook"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
	mscopes "github.com/go-kanna/orm/internal/gen/testdata/mysql/scopes"
	mstdlib "github.com/go-kanna/orm/internal/gen/testdata/mysql/stdlib"
	ppgx "github.com/go-kanna/orm/internal/gen/testdata/postgres/pgx"
	pscopes "github.com/go-kanna/orm/internal/gen/testdata/postgres/scopes"
	"github.com/go-kanna/orm/option"
)

func TestHooks_Postgres(t *testing.T) {
	t.Parallel()
	var done []hook.Execution
	repo := ppgx.NewOrderRepository(pgxConn(t, postgresSchema(t)), captureExecutions(t, &done))
	ctx := t.Context()
	if _, err := repo.Get(ctx, 1, pscopes.Orders().WithLines()); err != nil {
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
	repo := mstdlib.NewOrderRepository(mysqlDB(t), captureExecutions(t, &done))
	ctx := t.Context()
	if _, err := repo.Get(ctx, 1, mscopes.Orders().WithLines()); err != nil {
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

// A statement written by hand on database/sql gets the rows of database/sql, and the hooks see it done as it returns.
func TestHooks_ByHand_MySQL(t *testing.T) {
	t.Parallel()
	var done []hook.Execution
	repo := mstdlib.NewOrderRepository(mysqlDB(t), captureExecutions(t, &done))
	ids, n, err := repo.OrderIDsByHand(t.Context())
	if err != nil || len(ids) == 0 || int64(len(ids)) != n {
		t.Fatalf("read by hand: %v, %d, %v", ids, n, err)
	}
	if len(done) != 2 || !strings.Contains(done[0].SQL, "SELECT id") || !strings.Contains(done[1].SQL, "count(*)") ||
		done[0].Err != nil || done[1].Err != nil {
		t.Fatalf("executions: %+v", done)
	}
}

type hookKey struct{}

// captureExecutions has a repository mark the context of each statement before it runs, and record it after.
func captureExecutions(t *testing.T, done *[]hook.Execution) option.Option {
	t.Helper()
	return option.WithHook(hook.Hook{
		Before: func(ctx context.Context, q hook.Query) context.Context {
			return context.WithValue(ctx, hookKey{}, q.SQL)
		},
		After: func(ctx context.Context, e hook.Execution) {
			if ctx.Value(hookKey{}) != e.SQL {
				t.Error("After did not get the context of Before")
			}
			e.Args = slices.Clone(e.Args)
			*done = append(*done, e)
		},
	})
}

func checkExecutions(t *testing.T, done []hook.Execution) {
	t.Helper()
	if len(done) != 5 {
		t.Fatalf("got %d executions, want parent, preload, count, custom SQL, failed insert: %+v", len(done), done)
	}
	statements := []string{"FROM orders", "order_lines", "count(*)", "UPDATE orders", "INSERT INTO order_lines"}
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

func TestHooks_CanceledContext_Postgres(t *testing.T) {
	t.Parallel()
	repo := ppgx.NewOrderRepository(pgxConn(t, postgresSchema(t)), cancelStatements())
	ctx := t.Context()
	_, query := repo.Get(ctx, 1)
	_, queryRow := repo.Count(ctx)
	_, exec := repo.CloseOld(ctx, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	checkCanceled(t, map[string]error{"Query": query, "QueryRow": queryRow, "Exec": exec})
}

func TestHooks_CanceledContext_MySQL(t *testing.T) {
	t.Parallel()
	repo := mstdlib.NewOrderRepository(mysqlDB(t), cancelStatements())
	ctx := t.Context()
	_, query := repo.Get(ctx, 1)
	_, queryRow := repo.Count(ctx)
	_, exec := repo.CloseOld(ctx, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	checkCanceled(t, map[string]error{"Query": query, "QueryRow": queryRow, "Exec": exec})
}

// cancelStatements has a repository run each statement with a context its Before has canceled.
func cancelStatements() option.Option {
	return option.WithHook(hook.Hook{Before: func(ctx context.Context, _ hook.Query) context.Context {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx
	}})
}

// checkCanceled checks that each statement, run through the method of DBTX its key names, stopped on the canceled
// context.
func checkCanceled(t *testing.T, errs map[string]error) {
	t.Helper()
	for through, err := range errs {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("a statement through %s ran with a context Before canceled: %v", through, err)
		}
	}
}

package gen_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-kanna/orm/internal/gen/testdata/model"
	"github.com/go-kanna/orm/internal/gen/testdata/mysql"
	"github.com/go-kanna/orm/internal/gen/testdata/mysqlrepo"
	"github.com/go-kanna/orm/internal/gen/testdata/pgxrepo"
	"github.com/go-kanna/orm/internal/gen/testdata/postgres"
	"github.com/go-kanna/orm/option"
)

func TestQueryLogging_Postgres(t *testing.T) {
	t.Parallel()
	var logs []option.QueryLog
	repo := pgxrepo.NewOrderRepository(pgxConn(t, postgresSchema(t)), captureQueries(t, &logs))
	ctx := t.Context()
	if _, err := repo.Get(ctx, 1, postgres.Orders().WithLines()); err != nil {
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
	checkQueryLogs(t, logs)
}

func TestQueryLogging_MySQL(t *testing.T) {
	t.Parallel()
	var logs []option.QueryLog
	repo := mysqlrepo.NewOrderRepository(mysqlDB(t), captureQueries(t, &logs))
	ctx := t.Context()
	if _, err := repo.Get(ctx, 1, mysql.Orders().WithLines()); err != nil {
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
	checkQueryLogs(t, logs)
}

func captureQueries(t *testing.T, logs *[]option.QueryLog) option.Option {
	t.Helper()
	ctx := t.Context()
	return option.WithQueryLogger(func(got context.Context, q option.QueryLog) {
		if got != ctx {
			t.Error("query context was not preserved")
		}
		q.Args = slices.Clone(q.Args)
		*logs = append(*logs, q)
	})
}

func checkQueryLogs(t *testing.T, logs []option.QueryLog) {
	t.Helper()
	if len(logs) != 5 {
		t.Fatalf("got %d events, want parent, preload, count, custom SQL, failed insert: %+v", len(logs), logs)
	}
	statements := []string{"FROM orders", "FROM order_lines", "count(*)", "UPDATE orders", "INSERT INTO order_lines"}
	for i, text := range statements {
		q := logs[i]
		if !strings.Contains(q.SQL, text) || q.Duration <= 0 || (q.Err != nil) != (i == 4) {
			t.Errorf("event %d: %+v", i, q)
		}
	}
	if len(logs[0].Args) != 1 || logs[0].Args[0] != model.OrderID(1) {
		t.Fatalf("query arguments: %+v", logs[0].Args)
	}
}

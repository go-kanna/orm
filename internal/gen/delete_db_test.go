package gen_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
	mysqlscopes "github.com/go-kanna/orm/internal/gen/testdata/mysql/scopes"
	mysqlstore "github.com/go-kanna/orm/internal/gen/testdata/mysql/sqlstore"
	pgrepository "github.com/go-kanna/orm/internal/gen/testdata/postgres/repository"
	pgscopes "github.com/go-kanna/orm/internal/gen/testdata/postgres/scopes"
)

func TestDeleteReturning_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo pgrepository.OrderRepository) {
		ctx := t.Context()
		targets := pgscopes.Orders().IDIn(2, 3).TotalAsc().IDAsc()
		if rows, err := repo.DeleteSummaries(ctx, targets.Limit(0)); err != nil || rows == nil || len(rows) != 0 {
			t.Fatalf("zero limit: %+v, %v", rows, err)
		}
		for _, scope := range []pgscopes.Order{pgscopes.Orders(), targets.Limit(1).WithTags()} {
			if _, err := repo.DeleteSummaries(ctx, scope); err == nil {
				t.Fatal("unsafe or relation-loading deletion accepted")
			}
		}
		before, err := repo.Get(ctx, 3)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := repo.DeleteReturning(ctx, targets.Offset(1).Limit(1))
		if err != nil || !reflect.DeepEqual(rows, []model.Order{before}) {
			t.Fatalf("full deleted row: %+v, %v; want %+v", rows, err, before)
		}
		if _, err := repo.Get(ctx, 3); !errors.Is(err, orm.ErrNotFound) {
			t.Fatalf("returned row still exists: %v", err)
		}
		before, err = repo.Get(ctx, 2)
		if err != nil {
			t.Fatal(err)
		}
		summaries, err := repo.DeleteByCustomer(ctx, before.CustomerID, targets.Limit(1))
		want := []pgrepository.DeletedOrder{{ID: before.ID, Memo: before.Memo}}
		if err != nil || !reflect.DeepEqual(summaries, want) {
			t.Fatalf("projected deleted row: %+v, %v; want %+v", summaries, err, want)
		}
		if rows, err := repo.DeleteSummaries(ctx, targets.Limit(1)); err != nil || rows == nil || len(rows) != 0 {
			t.Fatalf("empty batch: %+v, %v", rows, err)
		}
	})
}

func TestDeletePage_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo pgrepository.OrderRepository) {
		ctx := t.Context()
		targets := pgscopes.Orders().IDIn(2, 3).TotalAsc().IDAsc()
		if n, err := repo.DeleteStale(ctx, targets.Limit(0)); err != nil || n != 0 {
			t.Fatalf("zero limit: %d, %v", n, err)
		}
		if _, err := repo.DeleteStale(ctx, targets.Limit(1).WithTags()); err == nil {
			t.Fatal("deletion with relation loading accepted")
		}
		if n, err := repo.DeleteStale(ctx, targets.Offset(1).Limit(1)); err != nil || n != 1 {
			t.Fatalf("offset page: %d, %v", n, err)
		}
		if _, err := repo.Get(ctx, 3); !errors.Is(err, orm.ErrNotFound) {
			t.Fatalf("wrong row removed: %v", err)
		}
		if _, err := repo.Get(ctx, 2); err != nil {
			t.Fatal(err)
		}
		if n, err := repo.DeleteStale(ctx, targets.Limit(1)); err != nil || n != 1 {
			t.Fatalf("next batch: %d, %v", n, err)
		}
		if n, err := repo.DeleteStale(ctx, targets.Limit(1)); err != nil || n != 0 {
			t.Fatalf("empty batch: %d, %v", n, err)
		}

		_, err := repo.InsertLines(ctx, []model.OrderLine{
			{OrderID: 1, LineNo: 3, SKU: "C"}, {OrderID: 1, LineNo: 4, SKU: "D"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if n, err := repo.DeleteLines(ctx, pgscopes.OrderLines().OrderIDEq(1).LineNoGte(3).
			LineNoAsc().Limit(1)); err != nil || n != 1 {
			t.Fatalf("composite key: %d, %v", n, err)
		}
		if _, err := repo.GetLine(ctx, 1, 4); err != nil {
			t.Fatalf("composite sibling removed: %v", err)
		}
		if _, err := repo.GetLine(ctx, 1, 3); !errors.Is(err, orm.ErrNotFound) {
			t.Fatalf("wrong composite row removed: %v", err)
		}
		if err := repo.InsertTags(ctx, []model.Tag{{Name: "alpha"}, {Name: "beta"}}); err != nil {
			t.Fatal(err)
		}
		if n, err := repo.DeleteTags(ctx, pgscopes.Tags().NameIn("alpha", "beta").NameDesc().Limit(1)); err != nil || n != 1 {
			t.Fatalf("unique key without a primary key: %d, %v", n, err)
		}
		if n, err := repo.DeleteTags(ctx, pgscopes.Tags().NameEq("beta")); err != nil || n != 0 {
			t.Fatalf("descending order: %d, %v", n, err)
		}
	})
}

func TestDeletePage_MySQL(t *testing.T) {
	t.Parallel()
	repo := mysqlstore.NewOrderRepository(mysqlDB(t))
	ctx := t.Context()
	targets := mysqlscopes.Orders().IDIn(2, 3).TotalAsc().IDAsc()
	if n, err := repo.DeleteStale(ctx, targets.Limit(0)); err != nil || n != 0 {
		t.Fatalf("zero limit: %d, %v", n, err)
	}
	if n, err := repo.DeleteStale(ctx, targets.Offset(1).Limit(1)); err != nil || n != 1 {
		t.Fatalf("offset page: %d, %v", n, err)
	}
	if _, err := repo.Get(ctx, 3); !errors.Is(err, orm.ErrNotFound) {
		t.Fatalf("wrong row removed: %v", err)
	}
	if _, err := repo.Get(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.DeleteStale(ctx, targets.Offset(0)); err != nil || n != 1 {
		t.Fatalf("offset without a limit: %d, %v", n, err)
	}
	if n, err := repo.DeleteStale(ctx, targets.Limit(1)); err != nil || n != 0 {
		t.Fatalf("empty batch: %d, %v", n, err)
	}
	if err := repo.InsertLines(ctx, []model.OrderLine{
		{OrderID: 1, LineNo: 3, SKU: "C"}, {OrderID: 1, LineNo: 4, SKU: "D"},
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.DeleteLines(ctx, mysqlscopes.OrderLines().OrderIDEq(1).LineNoGte(3).
		LineNoAsc().Limit(1)); err != nil || n != 1 {
		t.Fatalf("composite key: %d, %v", n, err)
	}
	if _, err := repo.GetLine(ctx, 1, 4); err != nil {
		t.Fatalf("composite sibling removed: %v", err)
	}
	if _, err := repo.GetLine(ctx, 1, 3); !errors.Is(err, orm.ErrNotFound) {
		t.Fatalf("wrong composite row removed: %v", err)
	}
	if err := repo.InsertTags(ctx, []model.Tag{{Name: "alpha"}, {Name: "beta"}}); err != nil {
		t.Fatal(err)
	}
	tags := mysqlscopes.Tags().NameIn("alpha", "beta").NameDesc().Limit(1)
	if n, err := repo.DeleteTags(ctx, tags); err != nil || n != 1 {
		t.Fatalf("unique key without a primary key: %d, %v", n, err)
	}
	if n, err := repo.DeleteTags(ctx, mysqlscopes.Tags().NameEq("beta")); err != nil || n != 0 {
		t.Fatalf("descending order: %d, %v", n, err)
	}
}

func TestDeleteCursor_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo pgrepository.OrderRepository) {
		n, err := repo.DeleteStale(t.Context(), pgscopes.Orders().IDIn(2, 3).TotalAsc().
			After(model.Order{ID: 2, Total: 200}).Limit(1))
		if err != nil || n != 1 {
			t.Fatalf("cursor delete: %d, %v", n, err)
		}
		if _, err := repo.Get(t.Context(), 2); err != nil {
			t.Fatalf("row before cursor removed: %v", err)
		}
		if _, err := repo.Get(t.Context(), 3); !errors.Is(err, orm.ErrNotFound) {
			t.Fatalf("wrong cursor target: %v", err)
		}
	})
}

func TestDeleteCursor_MySQL(t *testing.T) {
	t.Parallel()
	repo := mysqlstore.NewOrderRepository(mysqlDB(t))
	n, err := repo.DeleteStale(t.Context(), mysqlscopes.Orders().IDIn(2, 3).TotalAsc().
		After(model.Order{ID: 2, Total: 200}).Limit(1))
	if err != nil || n != 1 {
		t.Fatalf("cursor delete: %d, %v", n, err)
	}
	if _, err := repo.Get(t.Context(), 2); err != nil {
		t.Fatalf("row before cursor removed: %v", err)
	}
	if _, err := repo.Get(t.Context(), 3); !errors.Is(err, orm.ErrNotFound) {
		t.Fatalf("wrong cursor target: %v", err)
	}
}

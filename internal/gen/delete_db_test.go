package gen_test

import (
	"errors"
	"testing"

	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
	"github.com/go-kanna/orm/internal/gen/testdata/mysql"
	"github.com/go-kanna/orm/internal/gen/testdata/mysqlrepo"
	"github.com/go-kanna/orm/internal/gen/testdata/postgres"
	"github.com/go-kanna/orm/internal/gen/testdata/repository"
)

func TestDeletePage_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo repository.OrderRepository) {
		ctx := t.Context()
		targets := postgres.Orders().IDIn(2, 3).TotalAsc().IDAsc()
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
		if n, err := repo.DeleteLines(ctx, postgres.OrderLines().OrderIDEq(1).LineNoGte(3).
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
		if n, err := repo.DeleteTags(ctx, postgres.Tags().NameIn("alpha", "beta").NameDesc().Limit(1)); err != nil || n != 1 {
			t.Fatalf("unique key without a primary key: %d, %v", n, err)
		}
		if n, err := repo.DeleteTags(ctx, postgres.Tags().NameEq("beta")); err != nil || n != 0 {
			t.Fatalf("descending order: %d, %v", n, err)
		}
	})
}

func TestDeletePage_MySQL(t *testing.T) {
	t.Parallel()
	repo := mysqlrepo.NewOrderRepository(mysqlDB(t))
	ctx := t.Context()
	targets := mysql.Orders().IDIn(2, 3).TotalAsc().IDAsc()
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
	if n, err := repo.DeleteLines(ctx, mysql.OrderLines().OrderIDEq(1).LineNoGte(3).
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
	if n, err := repo.DeleteTags(ctx, mysql.Tags().NameIn("alpha", "beta").NameDesc().Limit(1)); err != nil || n != 1 {
		t.Fatalf("unique key without a primary key: %d, %v", n, err)
	}
	if n, err := repo.DeleteTags(ctx, mysql.Tags().NameEq("beta")); err != nil || n != 0 {
		t.Fatalf("descending order: %d, %v", n, err)
	}
}

func TestDeleteCursor_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo repository.OrderRepository) {
		n, err := repo.DeleteStale(t.Context(), postgres.Orders().IDIn(2, 3).TotalAsc().
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
	repo := mysqlrepo.NewOrderRepository(mysqlDB(t))
	n, err := repo.DeleteStale(t.Context(), mysql.Orders().IDIn(2, 3).TotalAsc().
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

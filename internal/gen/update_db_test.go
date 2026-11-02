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

func TestUpdateChanges_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo repository.OrderRepository) {
		ctx := t.Context()
		changed, err := repo.UpdateFields(ctx, 1, postgres.OrderChanges().SetMemo(nil).SetTotal(0).SetPaid(false),
			postgres.Orders().StatusEq("open"))
		if err != nil {
			t.Fatal(err)
		}
		if changed.Memo != nil || changed.Total != 0 || changed.Paid || changed.CustomerID != 10 || changed.Contact == nil {
			t.Fatalf("partial update: %+v", changed)
		}
		if _, err := repo.UpdateFields(ctx, 1, postgres.OrderChanges()); err == nil {
			t.Fatal("empty changes accepted")
		}
		_, err = repo.UpdateFields(ctx, 1, postgres.OrderChanges().SetStatus("closed"), postgres.Orders().StatusEq("open"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = repo.UpdateFields(ctx, 1, postgres.OrderChanges().SetStatus("open"), postgres.Orders().StatusEq("open"))
		if !errors.Is(err, orm.ErrNotFound) {
			t.Fatalf("failed precondition: %v", err)
		}
		n, err := repo.UpdateMany(ctx, postgres.OrderChanges().SetPaid(true), postgres.Orders().IDIn(1, 3))
		if err != nil || n != 2 {
			t.Fatalf("bulk update: %d, %v", n, err)
		}
		rows, err := repo.UpdateManyReturning(ctx, postgres.OrderChanges().SetTotal(111), postgres.Orders().IDIn(1, 3))
		if err != nil || len(rows) != 2 {
			t.Fatalf("returning rows: %+v, %v", rows, err)
		}
		for _, row := range rows {
			if row.Total != 111 || !row.Paid {
				t.Fatalf("stored values: %+v", row)
			}
		}
		if _, err := repo.UpdateMany(ctx, postgres.OrderChanges().SetPaid(false)); err == nil {
			t.Fatal("unbounded update accepted")
		}
		line, err := repo.UpdateLineFields(ctx, 1, 2, postgres.OrderLineChanges().SetSKU("changed"))
		if err != nil || line.SKU != "changed" || line.OrderID != 1 || line.LineNo != 2 {
			t.Fatalf("composite key: %+v, %v", line, err)
		}
		changed.Total = 222
		stored, err := repo.UpdateScoped(ctx, changed, postgres.Orders().StatusEq("closed"))
		if err != nil || stored.Total != 222 {
			t.Fatalf("full row returning: %+v, %v", stored, err)
		}
	})
}

func TestUpdateChanges_MySQL(t *testing.T) {
	t.Parallel()
	repo := mysqlrepo.NewOrderRepository(mysqlDB(t))
	ctx := t.Context()
	changes := mysql.OrderChanges().SetMemo(nil).SetTotal(0)
	for range 2 {
		if err := repo.UpdateFieldsOnly(ctx, 1, changes, mysql.Orders().StatusEq("open")); err != nil {
			t.Fatalf("matched update, including unchanged values: %v", err)
		}
	}
	row, err := repo.Get(ctx, model.OrderID(1))
	if err != nil || row.Memo != nil || row.Total != 0 || row.CustomerID != 10 {
		t.Fatalf("partial update: %+v, %v", row, err)
	}
	err = repo.UpdateFieldsOnly(ctx, 1, changes, mysql.Orders().StatusEq("closed"))
	if !errors.Is(err, orm.ErrNotFound) {
		t.Fatalf("missing update: %v", err)
	}
	n, err := repo.UpdateMany(ctx, mysql.OrderChanges().SetPaid(true), mysql.Orders().IDIn(1, 3))
	if err != nil || n != 2 {
		t.Fatalf("bulk update: %d, %v", n, err)
	}
	if _, err := repo.UpdateMany(ctx, changes); err == nil {
		t.Fatal("unbounded update accepted")
	}
}

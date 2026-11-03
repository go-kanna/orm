package gen_test

import (
	"testing"
	"time"

	"github.com/go-kanna/orm/internal/gen/testdata/model"
	mstdlib "github.com/go-kanna/orm/internal/gen/testdata/mysql/stdlib"
	prepository "github.com/go-kanna/orm/internal/gen/testdata/postgres/repository"
)

// givenOrder is an order of the id given, which the database assigns when it is zero, at position.
func givenOrder(id model.OrderID, position int) model.Order {
	return model.Order{ID: id, CustomerID: 10, Status: "open", PlacedAt: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		Total: 1, Position: position}
}

func TestGivenID_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		ctx := t.Context()
		if o, err := repo.Insert(ctx, givenOrder(0, 101)); err != nil || o.ID == 0 || o.ID == 500 {
			t.Errorf("Insert of no id = %+v, %v; want one the database assigns", o, err)
		}
		if o, err := repo.Insert(ctx, givenOrder(500, 102)); err != nil || o.ID != 500 {
			t.Errorf("Insert of id 500 = %+v, %v", o, err)
		}
		rows, err := repo.InsertOrders(ctx, []model.Order{givenOrder(600, 103), givenOrder(0, 104), givenOrder(601, 105)})
		if err != nil || len(rows) != 3 || rows[0].ID != 600 || rows[1].ID == 0 || rows[1].Position != 104 ||
			rows[2].ID != 601 {
			t.Errorf("InsertOrders of ids 600, none, and 601 = %+v, %v; want them in the order given", rows, err)
		}
		closed := givenOrder(500, 102)
		closed.Status = "closed"
		if o, err := repo.UpsertOrder(ctx, closed); err != nil || o.ID != 500 || o.Status != "closed" {
			t.Errorf("UpsertOrder of id 500 = %+v, %v; want order 500 closed", o, err)
		}
		if o, err := repo.UpsertOrder(ctx, givenOrder(0, 106)); err != nil || o.ID == 0 || o.Position != 106 {
			t.Errorf("UpsertOrder of no id = %+v, %v; want one inserted", o, err)
		}
	})
}

func TestGivenID_MySQL(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := mstdlib.NewOrderRepository(mysqlDB(t))
	if o, err := repo.Insert(ctx, givenOrder(0, 101)); err != nil || o.ID == 0 || o.ID == 500 {
		t.Errorf("Insert of no id = %+v, %v; want one the database assigns", o, err)
	}
	if o, err := repo.Insert(ctx, givenOrder(500, 102)); err != nil || o.ID != 500 {
		t.Errorf("Insert of id 500 = %+v, %v", o, err)
	}
	if err := repo.InsertOrders(ctx, []model.Order{givenOrder(600, 103), givenOrder(0, 104)}); err != nil {
		t.Fatal(err)
	}
	if o, err := repo.Get(ctx, 600); err != nil || o.Position != 103 {
		t.Errorf("Get of order 600 = %+v, %v", o, err)
	}
	closed := givenOrder(500, 102)
	closed.Status = "closed"
	if o, err := repo.UpsertOrder(ctx, closed); err != nil || o.ID != 500 || o.Status != "closed" {
		t.Errorf("UpsertOrder of id 500 = %+v, %v; want order 500 closed", o, err)
	}
	if o, err := repo.UpsertOrder(ctx, givenOrder(0, 106)); err != nil || o.ID == 0 || o.Position != 106 {
		t.Errorf("UpsertOrder of no id = %+v, %v; want one inserted", o, err)
	}
}

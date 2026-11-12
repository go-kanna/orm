package gen_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-kanna/orm"
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
		ids := map[int]model.OrderID{}
		for _, o := range rows {
			ids[o.Position] = o.ID
		}
		if err != nil || len(rows) != 3 || ids[103] != 600 || ids[104] == 0 || ids[104] == 600 || ids[105] != 601 {
			t.Errorf("InsertOrders of ids 600, none, and 601 = %+v, %v", rows, err)
		}
		checkOneStatement(t, repo.Count, func() bool {
			_, err := repo.InsertOrders(ctx, []model.Order{givenOrder(0, 107), givenOrder(600, 108)})
			return err != nil
		})
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
	checkOneStatement(t, repo.Count, func() bool {
		return repo.InsertOrders(ctx, []model.Order{givenOrder(0, 107), givenOrder(600, 108)}) != nil
	})
	closed := givenOrder(500, 102)
	closed.Status = "closed"
	if o, err := repo.UpsertOrder(ctx, closed); err != nil || o.ID != 500 || o.Status != "closed" {
		t.Errorf("UpsertOrder of id 500 = %+v, %v; want order 500 closed", o, err)
	}
	if o, err := repo.UpsertOrder(ctx, givenOrder(0, 106)); err != nil || o.ID == 0 || o.Position != 106 {
		t.Errorf("UpsertOrder of no id = %+v, %v; want one inserted", o, err)
	}
}

// TestReadBackMissing_MySQL checks that a row written but not there to read back, as a trigger moved its id, is told
// so through mapError, rather than as no row of a Get.
func TestReadBackMissing_MySQL(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db := mysqlDB(t)
	if _, err := db.ExecContext(ctx, "CREATE TRIGGER move_id BEFORE INSERT ON orders FOR EACH ROW "+
		"SET NEW.id = NEW.id + 1000"); err != nil {
		t.Fatal(err)
	}
	repo := mstdlib.NewOrderRepository(db)
	for name, write := range map[string]func(model.Order) (model.Order, error){
		"Insert":      func(o model.Order) (model.Order, error) { return repo.Insert(ctx, o) },
		"UpsertOrder": func(o model.Order) (model.Order, error) { return repo.UpsertOrder(ctx, o) },
	} {
		position := 201 + len(name)
		_, err := write(givenOrder(500+model.OrderID(position), position))
		var mapped mstdlib.MappedError
		if err == nil || !strings.Contains(err.Error(), "wrote the row, but no row with id") ||
			errors.Is(err, sql.ErrNoRows) || errors.Is(err, orm.ErrNotFound) || !errors.As(err, &mapped) {
			t.Errorf("%s of an order whose id the trigger moves = %v; want it told not read back, through mapError",
				name, err)
		}
	}
}

// checkOneStatement checks that insert, writing a row the database assigns the id of and one of an id taken, fails,
// reporting so, as one statement does, leaving the rows as they were.
func checkOneStatement[S any](t *testing.T, count func(context.Context, ...S) (int64, error), insert func() bool) {
	t.Helper()
	before, err := count(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !insert() {
		t.Fatal("InsertOrders of an id taken succeeded")
	}
	if after, err := count(t.Context()); err != nil || after != before {
		t.Errorf("InsertOrders of an id taken left %d orders, %v; want the %d before", after, err, before)
	}
}

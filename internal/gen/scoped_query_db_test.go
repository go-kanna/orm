package gen_test

import (
	"testing"
	"time"

	mscopes "github.com/go-kanna/orm/internal/gen/testdata/mysql/scopes"
	mstdlib "github.com/go-kanna/orm/internal/gen/testdata/mysql/stdlib"
	prepository "github.com/go-kanna/orm/internal/gen/testdata/postgres/repository"
	pscopes "github.com/go-kanna/orm/internal/gen/testdata/postgres/scopes"
)

// since is when the second of the orders of the tests was placed.
var since = time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)

func TestScopedQuery_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		ctx := t.Context()
		rows, err := repo.ListWithCustomers(ctx, pscopes.Orders().StatusEq("open").IDAsc().WithLines())
		if err != nil || len(rows) != 2 || rows[0].ID != 1 || rows[0].CustomerName != "Ann" ||
			len(rows[0].Lines) != 2 || rows[1].ID != 3 || rows[1].CustomerName != "Bob" || rows[1].Lines == nil {
			t.Errorf("ListWithCustomers of the open orders with their lines = %+v, %v", rows, err)
		}
		if o, err := repo.FirstPlacedSince(ctx, since, pscopes.Orders().PlacedAtAsc()); err != nil || o.ID != 2 {
			t.Errorf("FirstPlacedSince = %+v, %v; want order 2", o, err)
		}
		if o, err := repo.FirstPlacedSince(ctx, since, pscopes.Orders().StatusEq("gone")); err == nil {
			t.Errorf("FirstPlacedSince of no order = %+v; want orm.ErrNotFound", o)
		}
	})
}

func TestScopedQuery_MySQL(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := mstdlib.NewOrderRepository(mysqlDB(t))
	rows, err := repo.ListWithCustomers(ctx, mscopes.Orders().StatusEq("open").IDAsc().WithLines())
	if err != nil || len(rows) != 2 || rows[0].ID != 1 || rows[0].CustomerName != "Ann" ||
		len(rows[0].Lines) != 2 || rows[1].ID != 3 || rows[1].CustomerName != "Bob" || rows[1].Lines == nil {
		t.Errorf("ListWithCustomers of the open orders with their lines = %+v, %v", rows, err)
	}
	if o, err := repo.FirstPlacedSince(ctx, since, mscopes.Orders().PlacedAtAsc()); err != nil || o.ID != 2 {
		t.Errorf("FirstPlacedSince = %+v, %v; want order 2", o, err)
	}
}

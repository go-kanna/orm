package gen_test

import (
	"slices"
	"testing"
	"time"

	"github.com/go-kanna/orm/internal/gen/testdata/model"
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
		rows, err := repo.ListWithCustomers(ctx, pscopes.Order{}.StatusEq("open").IDAsc().WithLines())
		if err != nil || len(rows) != 2 || rows[0].ID != 1 || rows[0].CustomerName != "Ann" ||
			len(rows[0].Lines) != 2 || rows[1].ID != 3 || rows[1].CustomerName != "Bob" || rows[1].Lines == nil {
			t.Errorf("ListWithCustomers of the open orders with their lines = %+v, %v", rows, err)
		}
		if o, err := repo.FirstPlacedSince(ctx, since, pscopes.Order{}.PlacedAtAsc()); err != nil || o.ID != 2 {
			t.Errorf("FirstPlacedSince = %+v, %v; want order 2", o, err)
		}
		if o, err := repo.FirstPlacedSince(ctx, since, pscopes.Order{}.StatusEq("gone")); err == nil {
			t.Errorf("FirstPlacedSince of no order = %+v; want orm.ErrNotFound", o)
		}
	})
}

// repeated is how many times ListRepeated returns each order: more than the keys of one chunk of a relation, so that
// a row repeated is in two chunks.
const repeated = 1331

// checkRepeated reports rows, order 1 as ListRepeated returns it with lines of its, unless each holds its two lines
// once.
func checkRepeated(t *testing.T, what string, rows []model.Order, err error) {
	t.Helper()
	if err != nil || len(rows) != repeated || slices.ContainsFunc(rows, func(o model.Order) bool {
		return len(o.Lines) != 2
	}) {
		t.Errorf("ListRepeated with %s = %d rows, %v; want %d, each with its 2 lines once", what, len(rows), err,
			repeated)
	}
}

func TestScopedQuery_RepeatedRowsPostgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		ctx := t.Context()
		rows, err := repo.ListRepeated(ctx, pscopes.Order{}.IDEq(1).WithLines())
		checkRepeated(t, "its lines", rows, err)
		rows, err = repo.ListRepeated(ctx, pscopes.Order{}.IDEq(1).WithLines(pscopes.OrderLine{}.Limit(5)))
		checkRepeated(t, "a page of its lines", rows, err)
	})
}

func TestScopedQuery_RepeatedRowsMySQL(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := mstdlib.NewOrderRepository(mysqlDB(t))
	rows, err := repo.ListRepeated(ctx, mscopes.Order{}.IDEq(1).WithLines())
	checkRepeated(t, "its lines", rows, err)
	rows, err = repo.ListRepeated(ctx, mscopes.Order{}.IDEq(1).WithLines(mscopes.OrderLine{}.Limit(5)))
	checkRepeated(t, "a page of its lines", rows, err)
}

func TestScopedQuery_MySQL(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := mstdlib.NewOrderRepository(mysqlDB(t))
	rows, err := repo.ListWithCustomers(ctx, mscopes.Order{}.StatusEq("open").IDAsc().WithLines())
	if err != nil || len(rows) != 2 || rows[0].ID != 1 || rows[0].CustomerName != "Ann" ||
		len(rows[0].Lines) != 2 || rows[1].ID != 3 || rows[1].CustomerName != "Bob" || rows[1].Lines == nil {
		t.Errorf("ListWithCustomers of the open orders with their lines = %+v, %v", rows, err)
	}
	if o, err := repo.FirstPlacedSince(ctx, since, mscopes.Order{}.PlacedAtAsc()); err != nil || o.ID != 2 {
		t.Errorf("FirstPlacedSince = %+v, %v; want order 2", o, err)
	}
}

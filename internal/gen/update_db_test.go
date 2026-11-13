package gen_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
	mscopes "github.com/go-kanna/orm/internal/gen/testdata/mysql/scopes"
	mstdlib "github.com/go-kanna/orm/internal/gen/testdata/mysql/stdlib"
	ppgx "github.com/go-kanna/orm/internal/gen/testdata/postgres/pgx"
	prepository "github.com/go-kanna/orm/internal/gen/testdata/postgres/repository"
	pscopes "github.com/go-kanna/orm/internal/gen/testdata/postgres/scopes"
)

func TestUpdatePage_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		ctx := t.Context()
		every := pscopes.Order{}.TotalGt(0)
		held := func() []model.OrderID {
			t.Helper()
			rows, err := repo.List(ctx, pscopes.Order{}.StatusEq("held").IDAsc())
			if err != nil {
				t.Fatal(err)
			}
			return ids(rows)
		}
		hold := pscopes.OrderChanges().SetStatus("held")
		if n, err := repo.UpdateMany(ctx, hold, every.TotalDesc().Limit(0)); err != nil || n != 0 {
			t.Fatalf("zero limit: %d, %v", n, err)
		}
		if n, err := repo.UpdateMany(ctx, hold, every.TotalDesc().Limit(2)); err != nil || n != 2 ||
			!slices.Equal(held(), []model.OrderID{2, 3}) {
			t.Fatalf("first page by total, descending: %d, %v; held %v", n, err, held())
		}
		rows, err := repo.UpdateManyReturning(ctx, pscopes.OrderChanges().SetStatus("open"),
			every.TotalAsc().Offset(1).Limit(1))
		if err != nil || !slices.Equal(ids(rows), []model.OrderID{2}) || !slices.Equal(held(), []model.OrderID{3}) {
			t.Fatalf("offset page: %v, %v; held %v", ids(rows), err, held())
		}
		rows, err = repo.UpdateManyReturning(ctx, hold, every.TotalAsc().After(model.Order{ID: 1, Total: 100}).Limit(1))
		if err != nil || !slices.Equal(ids(rows), []model.OrderID{2}) {
			t.Fatalf("page continued: %v, %v", ids(rows), err)
		}
		if n, err := repo.UpdateMany(ctx, pscopes.OrderChanges().SetPaid(true), every.Limit(1)); err != nil || n != 1 {
			t.Fatalf("page of no order: %d, %v", n, err)
		}
		for _, tt := range []struct {
			name string
			s    pscopes.Order
			want string
		}{
			{"an order alone", every.TotalAsc(), "orm: Update ordering requires Limit or Offset"},
			{"a page of every row", pscopes.Order{}.Limit(1),
				"orm: Update would update every row of orders: give it a condition"},
			{"a relation", every.Limit(1).WithTags(), "orm: Update cannot load relations"},
		} {
			if _, err := repo.UpdateMany(ctx, hold, tt.s); err == nil || err.Error() != tt.want {
				t.Errorf("UpdateMany of %s error = %v, want %q", tt.name, err, tt.want)
			}
		}
		// A key parameter that is no unique key is a condition, as of a Delete; a unique key names the one row.
		note := "batch"
		n, err := repo.UpdateByCustomer(ctx, 10, pscopes.OrderChanges().SetMemo(&note), pscopes.Order{}.TotalDesc().Limit(1))
		if noted, _ := repo.List(ctx, pscopes.Order{}.MemoEq(note)); err != nil || n != 1 ||
			!slices.Equal(ids(noted), []model.OrderID{2}) {
			t.Errorf("page of a customer's orders: %d, %v; noted %v", n, err, ids(noted))
		}
		_, err = repo.UpdateFields(ctx, 1, hold, every.Limit(1))
		if want := "orm: Update takes conditions alone, but its scope keeps only some of the rows"; err == nil ||
			err.Error() != want {
			t.Errorf("UpdateFields with a limit error = %v, want %q", err, want)
		}
	})
}

func TestUpdatePage_MySQL(t *testing.T) {
	t.Parallel()
	repo := mstdlib.NewOrderRepository(mysqlDB(t))
	ctx := t.Context()
	every := mscopes.Order{}.TotalGt(0)
	held := func() []model.OrderID {
		t.Helper()
		rows, err := repo.List(ctx, mscopes.Order{}.StatusEq("held").IDAsc())
		if err != nil {
			t.Fatal(err)
		}
		return ids(rows)
	}
	hold := mscopes.OrderChanges().SetStatus("held")
	if n, err := repo.UpdateMany(ctx, hold, every.TotalDesc().Limit(0)); err != nil || n != 0 {
		t.Fatalf("zero limit: %d, %v", n, err)
	}
	if n, err := repo.UpdateMany(ctx, hold, every.TotalDesc().Limit(2)); err != nil || n != 2 ||
		!slices.Equal(held(), []model.OrderID{2, 3}) {
		t.Fatalf("first page by total, descending: %d, %v; held %v", n, err, held())
	}
	n, err := repo.UpdateMany(ctx, mscopes.OrderChanges().SetStatus("open"), every.TotalAsc().Offset(1).Limit(1))
	if err != nil || n != 1 || !slices.Equal(held(), []model.OrderID{3}) {
		t.Fatalf("offset page: %d, %v; held %v", n, err, held())
	}
	n, err = repo.UpdateMany(ctx, hold, every.TotalAsc().After(model.Order{ID: 1, Total: 100}).Limit(1))
	if err != nil || n != 1 || !slices.Equal(held(), []model.OrderID{2, 3}) {
		t.Fatalf("page continued: %d, %v; held %v", n, err, held())
	}
	if _, err := repo.UpdateMany(ctx, hold, every.TotalAsc()); err == nil ||
		err.Error() != "orm: Update ordering requires Limit or Offset" {
		t.Errorf("UpdateMany of an order alone error = %v", err)
	}
	note := "batch"
	n, err = repo.UpdateByCustomer(ctx, 10, mscopes.OrderChanges().SetMemo(&note), mscopes.Order{}.TotalDesc().Limit(1))
	if noted, _ := repo.List(ctx, mscopes.Order{}.MemoEq(note)); err != nil || n != 1 ||
		!slices.Equal(ids(noted), []model.OrderID{2}) {
		t.Errorf("page of a customer's orders: %d, %v; noted %v", n, err, ids(noted))
	}
	err = repo.UpdateFieldsOnly(ctx, 1, hold, every.Limit(1))
	if want := "orm: Update takes conditions alone, but its scope keeps only some of the rows"; err == nil ||
		err.Error() != want {
		t.Errorf("UpdateFieldsOnly with a limit error = %v, want %q", err, want)
	}
}

func TestUpdateLocked_Postgres(t *testing.T) {
	t.Parallel()

	url := postgresSchema(t)
	ctx := t.Context()
	holder := ppgx.NewOrderRepository(pgxTx(t, url))
	if _, err := holder.GetForUpdate(ctx, 1); err != nil {
		t.Fatal(err)
	}

	// The lock lasts as long as the one statement, so the repository needs no transaction. Were the update to wait
	// for the row held, it would fail by the timeout rather than hang.
	conn := pgxConn(t, url)
	if _, err := conn.Exec(ctx, "SET lock_timeout = '5s'"); err != nil {
		t.Fatal(err)
	}
	repo := ppgx.NewOrderRepository(conn)
	rows, err := repo.UpdateUnlocked(ctx, pscopes.OrderChanges().SetStatus("held"),
		pscopes.Order{}.StatusEq("open").IDAsc().Limit(1))
	if err != nil || !slices.Equal(ids(rows), []model.OrderID{3}) {
		t.Fatalf("first free open order = %v, %v; want 3", ids(rows), err)
	}
	rows, err = repo.UpdateUnlocked(ctx, pscopes.OrderChanges().SetPaid(true), pscopes.Order{}.TotalGt(0))
	paid := ids(rows)
	slices.Sort(paid)
	if err != nil || !slices.Equal(paid, []model.OrderID{2, 3}) {
		t.Fatalf("free orders of no page = %v, %v; want 2 and 3", paid, err)
	}

	_, err = repo.UpdateNoWait(ctx, pscopes.OrderChanges().SetStatus("held"), pscopes.Order{}.TotalGt(0).IDAsc().Limit(1))
	if !lockNotAvailable(err) || errors.Is(err, orm.ErrNotFound) {
		t.Errorf("UpdateNoWait of a page holding the row held error = %v, want %s", err, pgLockNotAvailable)
	}
	if n, err := repo.UpdateNoWait(ctx, pscopes.OrderChanges().SetStatus("held"), pscopes.Order{}.IDEq(2)); err != nil ||
		n != 1 {
		t.Errorf("UpdateNoWait of a free row = %d, %v", n, err)
	}
	o, err := repo.Get(ctx, 1)
	if err != nil || o.Status != "open" || o.Paid {
		t.Errorf("the row held = %+v, %v; want it as it was", o, err)
	}
}

func TestUpdateChanges_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		ctx := t.Context()
		changed, err := repo.UpdateFields(ctx, 1, pscopes.OrderChanges().SetMemo(nil).SetTotal(0).SetPaid(false),
			pscopes.Order{}.StatusEq("open"))
		if err != nil {
			t.Fatal(err)
		}
		if changed.Memo != nil || changed.Total != 0 || changed.Paid || changed.CustomerID != 10 || changed.Contact == nil {
			t.Fatalf("partial update: %+v", changed)
		}
		if _, err := repo.UpdateFields(ctx, 1, pscopes.OrderChanges()); !errors.Is(err, orm.ErrEmptyChangeSet) {
			t.Fatalf("empty changes: %v, want orm.ErrEmptyChangeSet", err)
		}
		_, err = repo.UpdateFields(ctx, 1, pscopes.OrderChanges().SetStatus("closed"), pscopes.Order{}.StatusEq("open"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = repo.UpdateFields(ctx, 1, pscopes.OrderChanges().SetStatus("open"), pscopes.Order{}.StatusEq("open"))
		if !errors.Is(err, orm.ErrNotFound) {
			t.Fatalf("failed precondition: %v", err)
		}
		n, err := repo.UpdateMany(ctx, pscopes.OrderChanges().SetPaid(true), pscopes.Order{}.IDIn(1, 3))
		if err != nil || n != 2 {
			t.Fatalf("bulk update: %d, %v", n, err)
		}
		rows, err := repo.UpdateManyReturning(ctx, pscopes.OrderChanges().SetTotal(111), pscopes.Order{}.IDIn(1, 3))
		if err != nil || len(rows) != 2 {
			t.Fatalf("returning rows: %+v, %v", rows, err)
		}
		for _, row := range rows {
			if row.Total != 111 || !row.Paid {
				t.Fatalf("stored values: %+v", row)
			}
		}
		if _, err := repo.UpdateMany(ctx, pscopes.OrderChanges().SetPaid(false)); err == nil {
			t.Fatal("unbounded update accepted")
		}
		line, err := repo.UpdateLineFields(ctx, 1, 2, pscopes.OrderLineChanges().SetSKU("changed"))
		if err != nil || line.SKU != "changed" || line.OrderID != 1 || line.LineNo != 2 {
			t.Fatalf("composite key: %+v, %v", line, err)
		}
		changed.Total = 222
		stored, err := repo.UpdateScoped(ctx, changed, pscopes.Order{}.StatusEq("closed"))
		if err != nil || stored.Total != 222 {
			t.Fatalf("full row returning: %+v, %v", stored, err)
		}
	})
}

func TestUpdateReturning_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		ctx := t.Context()
		o, err := repo.Get(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		o.Total, o.Status = 333, "held"
		stored, err := repo.UpdateReturning(ctx, o)
		if err != nil || stored.ID != 1 || stored.Total != 333 || stored.Status != "held" ||
			stored.CustomerID != o.CustomerID {
			t.Fatalf("UpdateReturning = %+v, %v", stored, err)
		}
		o.ID = 999
		if stored, err := repo.UpdateReturning(ctx, o); !errors.Is(err, orm.ErrNotFound) {
			t.Fatalf("UpdateReturning of no row = %+v, %v; want orm.ErrNotFound", stored, err)
		}
	})
}

func TestUpdateChanges_MySQL(t *testing.T) {
	t.Parallel()
	repo := mstdlib.NewOrderRepository(mysqlDB(t))
	ctx := t.Context()
	changes := mscopes.OrderChanges().SetMemo(nil).SetTotal(0)
	for range 2 {
		if err := repo.UpdateFieldsOnly(ctx, 1, changes, mscopes.Order{}.StatusEq("open")); err != nil {
			t.Fatalf("matched update, including unchanged values: %v", err)
		}
	}
	row, err := repo.Get(ctx, model.OrderID(1))
	if err != nil || row.Memo != nil || row.Total != 0 || row.CustomerID != 10 {
		t.Fatalf("partial update: %+v, %v", row, err)
	}
	err = repo.UpdateFieldsOnly(ctx, 1, changes, mscopes.Order{}.StatusEq("closed"))
	if !errors.Is(err, orm.ErrNotFound) {
		t.Fatalf("missing update: %v", err)
	}
	n, err := repo.UpdateMany(ctx, mscopes.OrderChanges().SetPaid(true), mscopes.Order{}.IDIn(1, 3))
	if err != nil || n != 2 {
		t.Fatalf("bulk update: %d, %v", n, err)
	}
	if _, err := repo.UpdateMany(ctx, changes); err == nil {
		t.Fatal("unbounded update accepted")
	}
}

func TestUpdateAdd_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		ctx := t.Context()
		totals := func() []int64 {
			t.Helper()
			rows, err := repo.List(ctx, pscopes.Order{}.IDAsc())
			if err != nil {
				t.Fatal(err)
			}
			return orderTotals(rows)
		}
		o, err := repo.UpdateFields(ctx, 1, pscopes.OrderChanges().AddTotal(5).SetStatus("held"))
		if err != nil || o.Total != 105 || o.Status != "held" {
			t.Fatalf("Add by key = %+v, %v; want a total of 105", o, err)
		}
		n, err := repo.UpdateMany(ctx, pscopes.OrderChanges().AddTotal(-10), pscopes.Order{}.CustomerIDEq(10))
		if err != nil || n != 2 || !slices.Equal(totals(), []int64{95, 190, 300}) {
			t.Fatalf("Add by scopes = %d, %v; totals %v", n, err, totals())
		}
		rows, err := repo.UpdateManyReturning(ctx, pscopes.OrderChanges().AddTotal(1),
			pscopes.Order{}.TotalGt(0).TotalDesc().Limit(1))
		if err != nil || len(rows) != 1 || rows[0].ID != 3 || rows[0].Total != 301 {
			t.Fatalf("Add to a page = %+v, %v", rows, err)
		}
		if _, err := repo.UpdateFields(ctx, 2, pscopes.OrderChanges().AddTotal(1).SetTotal(7)); err != nil ||
			!slices.Equal(totals(), []int64{95, 7, 301}) {
			t.Fatalf("Add then Set: %v; totals %v", err, totals())
		}

		placed := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
		met, err := repo.UpsertByPosition(ctx, model.Order{
			CustomerID: 30, Status: "given", PlacedAt: placed, Total: 900, Position: 1,
		}, pscopes.OrderChanges().AddCustomerID(1).SetStatus("met").AddTotal(50))
		if err != nil || met.ID != 1 || met.Total != 145 || met.CustomerID != 11 || met.Status != "met" {
			t.Fatalf("Add over the row met = %+v, %v; want order 1 with 50 added to its total", met, err)
		}
		added, err := repo.UpsertByPosition(ctx, model.Order{
			CustomerID: 30, Status: "given", PlacedAt: placed, Total: 900, Position: 7,
		}, pscopes.OrderChanges().AddTotal(50))
		if err != nil || added.Position != 7 || added.Total != 900 {
			t.Fatalf("Add of no row met = %+v, %v; want the row as given", added, err)
		}
	})
}

func TestUpdateAdd_MySQL(t *testing.T) {
	t.Parallel()
	repo := mstdlib.NewOrderRepository(mysqlDB(t))
	ctx := t.Context()
	totals := func() []int64 {
		t.Helper()
		rows, err := repo.List(ctx, mscopes.Order{}.IDAsc())
		if err != nil {
			t.Fatal(err)
		}
		return orderTotals(rows)
	}
	if err := repo.UpdateFieldsOnly(ctx, 1, mscopes.OrderChanges().SetStatus("held").AddTotal(5)); err != nil ||
		!slices.Equal(totals(), []int64{105, 200, 300}) {
		t.Fatalf("Add by key: %v; totals %v", err, totals())
	}
	n, err := repo.UpdateMany(ctx, mscopes.OrderChanges().AddTotal(-10), mscopes.Order{}.CustomerIDEq(10))
	if err != nil || n != 2 || !slices.Equal(totals(), []int64{95, 190, 300}) {
		t.Fatalf("Add by scopes = %d, %v; totals %v", n, err, totals())
	}
	n, err = repo.UpdateMany(ctx, mscopes.OrderChanges().AddTotal(1), mscopes.Order{}.TotalGt(0).TotalDesc().Limit(1))
	if err != nil || n != 1 || !slices.Equal(totals(), []int64{95, 190, 301}) {
		t.Fatalf("Add to a page = %d, %v; totals %v", n, err, totals())
	}
	if err := repo.UpdateFieldsOnly(ctx, 2, mscopes.OrderChanges().AddTotal(1).SetTotal(7)); err != nil ||
		!slices.Equal(totals(), []int64{95, 7, 301}) {
		t.Fatalf("Add then Set: %v; totals %v", err, totals())
	}

	// MySQL assigns from left to right, each assignment seeing those before it, but an Add reads its own column alone.
	placed := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	met, err := repo.UpsertByPosition(ctx, model.Order{
		CustomerID: 30, Status: "given", PlacedAt: placed, Total: 900, Position: 1,
	}, mscopes.OrderChanges().AddCustomerID(1).SetStatus("met").AddTotal(50))
	if err != nil || met.ID != 1 || met.Total != 145 || met.CustomerID != 11 || met.Status != "met" {
		t.Fatalf("Add over the row met = %+v, %v; want order 1 with 50 added to its total", met, err)
	}
	added, err := repo.UpsertByPosition(ctx, model.Order{
		CustomerID: 30, Status: "given", PlacedAt: placed, Total: 900, Position: 7,
	}, mscopes.OrderChanges().AddTotal(50))
	if err != nil || added.Position != 7 || added.Total != 900 {
		t.Fatalf("Add of no row met = %+v, %v; want the row as given", added, err)
	}
}

func TestUpdateRaiseLower_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		ctx := t.Context()
		totals := func() []int64 {
			t.Helper()
			rows, err := repo.List(ctx, pscopes.Order{}.IDAsc())
			if err != nil {
				t.Fatal(err)
			}
			return orderTotals(rows)
		}
		day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
		o, err := repo.UpdateFields(ctx, 1, pscopes.OrderChanges().RaisePlacedAt(day(5)).LowerTotal(150))
		if err != nil || !o.PlacedAt.Equal(day(5)) || o.Total != 100 {
			t.Fatalf("Raise and Lower by key = %+v, %v; want placed on the 5th, a total of 100", o, err)
		}
		o, err = repo.UpdateFields(ctx, 1, pscopes.OrderChanges().RaisePlacedAt(day(2)).LowerPlacedAt(day(3)))
		if err != nil || !o.PlacedAt.Equal(day(3)) {
			t.Fatalf("Raise then Lower = %+v, %v; want placed on the 3rd", o, err)
		}
		n, err := repo.UpdateMany(ctx, pscopes.OrderChanges().RaiseTotal(250), pscopes.Order{}.CustomerIDEq(10))
		if err != nil || n != 2 || !slices.Equal(totals(), []int64{250, 250, 300}) {
			t.Fatalf("Raise by scopes = %d, %v; totals %v", n, err, totals())
		}
		rows, err := repo.UpdateManyReturning(ctx, pscopes.OrderChanges().LowerTotal(260),
			pscopes.Order{}.TotalGt(0).TotalDesc().Limit(1))
		if err != nil || len(rows) != 1 || rows[0].ID != 3 || rows[0].Total != 260 {
			t.Fatalf("Lower of a page = %+v, %v", rows, err)
		}

		met, err := repo.UpsertByPosition(ctx, model.Order{
			CustomerID: 30, Status: "given", PlacedAt: day(9), Total: 900, Position: 1,
		}, pscopes.OrderChanges().LowerPlacedAt(day(1)).RaiseTotal(999).SetStatus("met"))
		if err != nil || met.ID != 1 || !met.PlacedAt.Equal(day(1)) || met.Total != 999 || met.Status != "met" ||
			met.CustomerID != 10 {
			t.Fatalf("Raise and Lower over the row met = %+v, %v", met, err)
		}
		met, err = repo.UpsertByPosition(ctx, model.Order{
			CustomerID: 30, Status: "given", PlacedAt: day(9), Total: 900, Position: 2,
		}, pscopes.OrderChanges().RaisePlacedAt(day(1)).LowerTotal(999))
		if err != nil || met.ID != 2 || !met.PlacedAt.Equal(day(2)) || met.Total != 250 {
			t.Fatalf("Raise and Lower keeping the row met = %+v, %v", met, err)
		}
		added, err := repo.UpsertByPosition(ctx, model.Order{
			CustomerID: 30, Status: "given", PlacedAt: day(9), Total: 900, Position: 7,
		}, pscopes.OrderChanges().LowerPlacedAt(day(1)).RaiseTotal(999))
		if err != nil || added.Position != 7 || !added.PlacedAt.Equal(day(9)) || added.Total != 900 {
			t.Fatalf("Raise and Lower of no row met = %+v, %v; want the row as given", added, err)
		}
	})
}

func TestUpdateRaiseLower_MySQL(t *testing.T) {
	t.Parallel()
	repo := mstdlib.NewOrderRepository(mysqlDB(t))
	ctx := t.Context()
	list := func() []model.Order {
		t.Helper()
		rows, err := repo.List(ctx, mscopes.Order{}.IDAsc())
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	if err := repo.UpdateFieldsOnly(ctx, 1, mscopes.OrderChanges().RaisePlacedAt(day(5)).LowerTotal(150)); err != nil ||
		!list()[0].PlacedAt.Equal(day(5)) || list()[0].Total != 100 {
		t.Fatalf("Raise and Lower by key: %v; orders %+v", err, list())
	}
	err := repo.UpdateFieldsOnly(ctx, 1, mscopes.OrderChanges().RaisePlacedAt(day(2)).LowerPlacedAt(day(3)))
	if err != nil || !list()[0].PlacedAt.Equal(day(3)) {
		t.Fatalf("Raise then Lower: %v; orders %+v", err, list())
	}
	n, err := repo.UpdateMany(ctx, mscopes.OrderChanges().RaiseTotal(250), mscopes.Order{}.CustomerIDEq(10))
	if err != nil || n != 2 || !slices.Equal(orderTotals(list()), []int64{250, 250, 300}) {
		t.Fatalf("Raise by scopes = %d, %v; totals %v", n, err, orderTotals(list()))
	}
	n, err = repo.UpdateMany(ctx, mscopes.OrderChanges().LowerTotal(260), mscopes.Order{}.TotalGt(0).TotalDesc().Limit(1))
	if err != nil || n != 1 || !slices.Equal(orderTotals(list()), []int64{250, 250, 260}) {
		t.Fatalf("Lower of a page = %d, %v; totals %v", n, err, orderTotals(list()))
	}

	met, err := repo.UpsertByPosition(ctx, model.Order{
		CustomerID: 30, Status: "given", PlacedAt: day(9), Total: 900, Position: 1,
	}, mscopes.OrderChanges().LowerPlacedAt(day(1)).RaiseTotal(999).SetStatus("met"))
	if err != nil || met.ID != 1 || !met.PlacedAt.Equal(day(1)) || met.Total != 999 || met.Status != "met" ||
		met.CustomerID != 10 {
		t.Fatalf("Raise and Lower over the row met = %+v, %v", met, err)
	}
	met, err = repo.UpsertByPosition(ctx, model.Order{
		CustomerID: 30, Status: "given", PlacedAt: day(9), Total: 900, Position: 2,
	}, mscopes.OrderChanges().RaisePlacedAt(day(1)).LowerTotal(999))
	if err != nil || met.ID != 2 || !met.PlacedAt.Equal(day(2)) || met.Total != 250 {
		t.Fatalf("Raise and Lower keeping the row met = %+v, %v", met, err)
	}
	added, err := repo.UpsertByPosition(ctx, model.Order{
		CustomerID: 30, Status: "given", PlacedAt: day(9), Total: 900, Position: 7,
	}, mscopes.OrderChanges().LowerPlacedAt(day(1)).RaiseTotal(999))
	if err != nil || added.Position != 7 || !added.PlacedAt.Equal(day(9)) || added.Total != 900 {
		t.Fatalf("Raise and Lower of no row met = %+v, %v; want the row as given", added, err)
	}
}

func orderTotals(orders []model.Order) []int64 {
	out := make([]int64, 0, len(orders))
	for _, o := range orders {
		out = append(out, o.Total)
	}
	return out
}

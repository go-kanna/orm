package gen_test

import (
	"errors"
	"testing"

	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
	mysqlscopes "github.com/go-kanna/orm/internal/gen/testdata/mysql/scopes"
	mysqlstore "github.com/go-kanna/orm/internal/gen/testdata/mysql/sqlstore"
	pgrepository "github.com/go-kanna/orm/internal/gen/testdata/postgres/repository"
	pgscopes "github.com/go-kanna/orm/internal/gen/testdata/postgres/scopes"
	"github.com/go-kanna/orm/join"
)

func TestJoinedProjection_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo pgrepository.OrderRepository) {
		ctx := t.Context()
		joined := pgscopes.Orders().JoinCustomers().TotalAsc().IDAsc()
		if _, err := repo.GetSummary(ctx, 1); err == nil {
			t.Fatal("projection without its join was accepted")
		}
		full, err := repo.Get(ctx, 1, pgscopes.Orders().WithCustomer())
		if err != nil {
			t.Fatal(err)
		}
		summary, err := repo.GetSummary(ctx, 1, pgscopes.Orders().JoinCustomers())
		if err != nil || summary.ID != full.ID || summary.Name != full.Customer.Name || summary.Memo == nil ||
			*summary.Memo != *full.Memo {
			t.Fatalf("joined result: %+v, %v; want %+v", summary, err, full)
		}
		byName, err := repo.GetSummary(ctx, 1, pgscopes.Orders().Join("customers"))
		if err != nil || byName.Name != summary.Name {
			t.Fatalf("string FK join: %+v, %v", byName, err)
		}
		if _, err := repo.GetTagSummary(ctx, 1, pgscopes.Orders().Join("tags")); err == nil {
			t.Fatal("FK-less join accepted without conditions")
		}
		tag, err := repo.GetTagSummary(ctx, 1, pgscopes.Orders().Join("tags", join.On("note", "name")))
		if err != nil || tag.ID != 1 || tag.TagName != "gift" {
			t.Fatalf("FK-less join: %+v, %v", tag, err)
		}

		rows, err := repo.ListSummaries(ctx, joined.TotalGte(200).Limit(1))
		if err != nil || len(rows) != 1 || rows[0].ID != 2 {
			t.Fatalf("filtered page: %+v, %v", rows, err)
		}
		rows, err = repo.ListSummaries(ctx, joined.After(model.Order{ID: 2, Total: 200}).Limit(1))
		if err != nil || len(rows) != 1 || rows[0].ID != 3 {
			t.Fatalf("cursor: %+v, %v", rows, err)
		}
		rows, err = repo.ListSummaries(ctx, joined.Before(model.Order{ID: 3, Total: 300}).Limit(2))
		if err != nil || len(rows) != 2 || rows[0].ID != 1 || rows[1].ID != 2 {
			t.Fatalf("backwards cursor: %+v, %v", rows, err)
		}
		row, err := repo.FindSummary(ctx, 999, pgscopes.Orders().JoinCustomers())
		if err != nil || row != nil {
			t.Fatalf("optional absence: %+v, %v", row, err)
		}
		if _, err := repo.GetSummary(ctx, 999, pgscopes.Orders().JoinCustomers()); !errors.Is(err, orm.ErrNotFound) {
			t.Fatalf("required absence: %v", err)
		}
	})
}

func TestJoinedProjection_MySQL(t *testing.T) {
	t.Parallel()
	repo := mysqlstore.NewOrderRepository(mysqlDB(t))
	ctx := t.Context()
	joined := mysqlscopes.Orders().JoinCustomers().TotalAsc().IDAsc()
	full, err := repo.Get(ctx, 1, mysqlscopes.Orders().WithCustomer())
	if err != nil {
		t.Fatal(err)
	}
	summary, err := repo.GetSummary(ctx, 1, mysqlscopes.Orders().JoinCustomers())
	if err != nil || summary.Name != full.Customer.Name || summary.Memo == nil || *summary.Memo != *full.Memo {
		t.Fatalf("joined result: %+v, %v", summary, err)
	}
	byName, err := repo.GetSummary(ctx, 1, mysqlscopes.Orders().Join("customers"))
	if err != nil || byName.Name != summary.Name {
		t.Fatalf("string FK join: %+v, %v", byName, err)
	}
	if _, err := repo.GetTagSummary(ctx, 1, mysqlscopes.Orders().Join("tags")); err == nil {
		t.Fatal("FK-less join accepted without conditions")
	}
	tag, err := repo.GetTagSummary(ctx, 1, mysqlscopes.Orders().Join("tags", join.On("note", "name")))
	if err != nil || tag.ID != 1 || tag.TagName != "gift" {
		t.Fatalf("FK-less join: %+v, %v", tag, err)
	}

	rows, err := repo.ListSummaries(ctx, joined.TotalGte(200).Offset(1).Limit(1))
	if err != nil || len(rows) != 1 || rows[0].ID != 3 {
		t.Fatalf("filtered page: %+v, %v", rows, err)
	}
	rows, err = repo.ListSummaries(ctx, joined.Before(model.Order{ID: 3, Total: 300}).Limit(2))
	if err != nil || len(rows) != 2 || rows[0].ID != 1 || rows[1].ID != 2 {
		t.Fatalf("backwards cursor: %+v, %v", rows, err)
	}
	row, err := repo.FindSummary(ctx, 999, mysqlscopes.Orders().JoinCustomers())
	if err != nil || row != nil {
		t.Fatalf("optional absence: %+v, %v", row, err)
	}
}

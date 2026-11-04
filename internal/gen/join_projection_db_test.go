package gen_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
	mscopes "github.com/go-kanna/orm/internal/gen/testdata/mysql/scopes"
	mstdlib "github.com/go-kanna/orm/internal/gen/testdata/mysql/stdlib"
	prepository "github.com/go-kanna/orm/internal/gen/testdata/postgres/repository"
	pscopes "github.com/go-kanna/orm/internal/gen/testdata/postgres/scopes"
)

// contacts describes the customers of orders a LEFT JOIN reads: the name, and the nickname or - when it is NULL, or
// nil when no customer joins.
func contacts(rows []model.OrderContact) []string {
	described := make([]string, 0, len(rows))
	for _, r := range rows {
		switch {
		case r.Customer == nil:
			described = append(described, "nil")
		case r.Customer.Nickname == nil:
			described = append(described, r.Customer.Name+" -")
		default:
			described = append(described, r.Customer.Name+" "+*r.Customer.Nickname)
		}
	}
	return described
}

func TestJoinedProjection_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		ctx := t.Context()
		full, err := repo.Get(ctx, 1, pscopes.Orders().WithCustomer())
		if err != nil {
			t.Fatal(err)
		}
		summary, err := repo.GetSummary(ctx, 1)
		if err != nil || summary.ID != full.ID || summary.Customer.Name != full.Customer.Name || summary.Memo == nil ||
			*summary.Memo != *full.Memo {
			t.Fatalf("joined result: %+v, %v; want %+v", summary, err, full)
		}

		ordered := pscopes.Orders().TotalAsc().IDAsc()
		rows, err := repo.ListSummaries(ctx, ordered.TotalGte(200).Limit(1))
		if err != nil || len(rows) != 1 || rows[0].ID != 2 || rows[0].Customer.Name != "Ann" {
			t.Fatalf("filtered page: %+v, %v", rows, err)
		}
		rows, err = repo.ListSummaries(ctx, ordered.After(model.Order{ID: 2, Total: 200}).Limit(1))
		if err != nil || len(rows) != 1 || rows[0].ID != 3 || rows[0].Customer.Name != "Bob" {
			t.Fatalf("cursor: %+v, %v", rows, err)
		}
		rows, err = repo.ListSummaries(ctx, ordered.Before(model.Order{ID: 3, Total: 300}).Limit(2))
		if err != nil || len(rows) != 2 || rows[0].ID != 1 || rows[1].ID != 2 {
			t.Fatalf("backwards cursor: %+v, %v", rows, err)
		}
		row, err := repo.FindSummary(ctx, 999)
		if err != nil || row != nil {
			t.Fatalf("optional absence: %+v, %v", row, err)
		}
		if _, err := repo.GetSummary(ctx, 999); !errors.Is(err, orm.ErrNotFound) {
			t.Fatalf("required absence: %v", err)
		}
	})
}

func TestJoinedProjection_PostgresLeftJoin(t *testing.T) {
	t.Parallel()
	repos := map[string]func(t *testing.T, url string) prepository.OrderRepository{"pgx": pgxRepository}
	if pgsqlRepository != nil {
		repos["database/sql"] = pgsqlRepository
	}
	for drv, open := range repos {
		t.Run(drv, func(t *testing.T) {
			t.Parallel()
			url := postgresSchema(t)
			// Order 4 is of customer 30, whom customers has no row of.
			if _, err := pgxConn(t, url).Exec(t.Context(), `INSERT INTO orders `+
				`(customer_id, status, placed_at, total, paid, "order") `+
				`VALUES (30, 'open', '2026-10-04T00:00:00Z', 400, false, 4)`); err != nil {
				t.Fatal(err)
			}
			repo := open(t, url)
			got, err := repo.ListContacts(t.Context(), pscopes.Orders().IDAsc())
			if err != nil || !slices.Equal(contacts(got), []string{"Ann -", "Ann -", "Bob bobby", "nil"}) {
				t.Fatalf("LEFT JOIN: %v, %v; want a NULL nickname told apart from no customer", contacts(got), err)
			}
			inner, err := repo.ListSummaries(t.Context(), pscopes.Orders().IDAsc())
			if err != nil || len(inner) != 3 {
				t.Fatalf("INNER JOIN: %+v, %v; want the order of no customer left out", inner, err)
			}
		})
	}
}

func TestJoinedProjection_MySQL(t *testing.T) {
	t.Parallel()
	db := mysqlDB(t)
	repo := mstdlib.NewOrderRepository(db)
	ctx := t.Context()
	full, err := repo.Get(ctx, 1, mscopes.Orders().WithCustomer())
	if err != nil {
		t.Fatal(err)
	}
	summary, err := repo.GetSummary(ctx, 1)
	if err != nil || summary.Customer.Name != full.Customer.Name || summary.Memo == nil || *summary.Memo != *full.Memo {
		t.Fatalf("joined result: %+v, %v", summary, err)
	}
	ordered := mscopes.Orders().TotalAsc().IDAsc()
	rows, err := repo.ListSummaries(ctx, ordered.TotalGte(200).Offset(1).Limit(1))
	if err != nil || len(rows) != 1 || rows[0].ID != 3 || rows[0].Customer.Name != "Bob" {
		t.Fatalf("filtered page: %+v, %v", rows, err)
	}
	rows, err = repo.ListSummaries(ctx, ordered.Before(model.Order{ID: 3, Total: 300}).Limit(2))
	if err != nil || len(rows) != 2 || rows[0].ID != 1 || rows[1].ID != 2 {
		t.Fatalf("backwards cursor: %+v, %v", rows, err)
	}
	row, err := repo.FindSummary(ctx, 999)
	if err != nil || row != nil {
		t.Fatalf("optional absence: %+v, %v", row, err)
	}

	// Order 4 is of customer 30, whom customers has no row of.
	if _, err := db.ExecContext(ctx, "INSERT INTO orders (customer_id, status, placed_at, total, paid, `order`) "+
		"VALUES (30, 'open', '2026-10-04 00:00:00', 400, false, 4)"); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ListContacts(ctx, mscopes.Orders().IDAsc())
	if err != nil || !slices.Equal(contacts(got), []string{"Ann -", "Ann -", "Bob bobby", "nil"}) {
		t.Fatalf("LEFT JOIN: %v, %v; want a NULL nickname told apart from no customer", contacts(got), err)
	}
	inner, err := repo.ListSummaries(ctx, mscopes.Orders().IDAsc())
	if err != nil || len(inner) != 3 {
		t.Fatalf("INNER JOIN: %+v, %v; want the order of no customer left out", inner, err)
	}
}

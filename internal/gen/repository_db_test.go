package gen_test

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
	"github.com/go-kanna/orm/internal/gen/testdata/pgxrepo"
	"github.com/go-kanna/orm/internal/gen/testdata/postgres"
)

// postgresDB connects to the database KANNA_ORM_TEST_POSTGRES names, with a schema of the test's own as its current
// one, dropped when the test ends, and runs schema there. Without the variable, the test is skipped.
func postgresDB(t *testing.T, schema string) *pgx.Conn {
	t.Helper()

	url := os.Getenv("KANNA_ORM_TEST_POSTGRES")
	if url == "" {
		t.Skip("KANNA_ORM_TEST_POSTGRES is not set; make db-up test-db sets it")
	}
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	name := "test_" + strings.ToLower(rand.Text())
	t.Cleanup(func() {
		ctx := context.WithoutCancel(ctx)
		if _, err := conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+name+" CASCADE"); err != nil {
			t.Error(err)
		}
		if err := conn.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+name+"; SET search_path TO "+name+";\n"+schema); err != nil {
		t.Fatal(err)
	}
	return conn
}

// ordersSchema creates the tables of the scope types under testdata, and rows in them.
const ordersSchema = `
CREATE TABLE orders (
	id bigserial PRIMARY KEY,
	customer_id bigint NOT NULL,
	status text NOT NULL,
	placed_at timestamptz NOT NULL,
	note text,
	total bigint NOT NULL,
	paid boolean NOT NULL,
	contact text,
	labels text[],
	receipt bytea,
	"order" integer NOT NULL
);
CREATE TABLE order_lines (
	order_id bigint NOT NULL REFERENCES orders,
	line_no integer NOT NULL,
	sku text NOT NULL,
	PRIMARY KEY (order_id, line_no)
);
INSERT INTO orders (customer_id, status, placed_at, note, total, paid, contact, labels, receipt, "order") VALUES
	(10, 'open', '2026-10-01T00:00:00Z', 'gift', 100, false, 'ann@example.com', '{a,b}', '\x00ff', 1),
	(10, 'closed', '2026-10-02T00:00:00Z', NULL, 200, true, NULL, NULL, NULL, 2),
	(20, 'open', '2026-10-03T00:00:00Z', NULL, 300, false, NULL, NULL, NULL, 3);
INSERT INTO order_lines VALUES (1, 1, 'A'), (1, 2, 'B');
`

func ids(orders []model.Order) []model.OrderID {
	out := make([]model.OrderID, 0, len(orders))
	for _, o := range orders {
		out = append(out, o.ID)
	}
	return out
}

func TestGeneratedRepository_Postgres(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := pgxrepo.NewOrderRepository(postgresDB(t, ordersSchema))

	o, err := repo.Get(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "open" || o.Memo == nil || *o.Memo != "gift" || o.Contact == nil || o.Contact.String() !=
		"ann@example.com" || !slices.Equal(o.Labels, []string{"a", "b"}) || !slices.Equal(o.Receipt, []byte{0, 0xff}) ||
		o.Position != 1 || o.PlacedAt.Day() != 1 {
		t.Errorf("Get(1) = %+v", o)
	}
	if _, err := repo.Get(ctx, 9); !errors.Is(err, orm.ErrNotFound) || err.Error() != "orders with id 9: orm: not found" {
		t.Errorf("Get(9) error = %v, want orm.ErrNotFound", err)
	}
	if _, err := repo.Get(ctx, 2, postgres.Orders().StatusEq("open")); !errors.Is(err, orm.ErrNotFound) {
		t.Errorf("Get(2) of the open orders error = %v, want orm.ErrNotFound", err)
	}

	if o, err := repo.First(ctx, postgres.Orders().PlacedAtDesc()); err != nil || o.ID != 3 {
		t.Errorf("First, latest first = %v, %v; want 3", o.ID, err)
	}
	for _, tt := range []struct {
		name string
		list func() ([]model.Order, error)
		want []model.OrderID
	}{
		{"open", func() ([]model.Order, error) { return repo.List(ctx, postgres.Orders().StatusEq("open").IDAsc()) },
			[]model.OrderID{1, 3}},
		{"of a customer", func() ([]model.Order, error) {
			return repo.ListByCustomer(ctx, 10, postgres.Orders().IDDesc())
		}, []model.OrderID{2, 1}},
		{"none", func() ([]model.Order, error) { return repo.List(ctx, postgres.Orders().TotalGt(1000)) },
			[]model.OrderID{}},
	} {
		got, err := tt.list()
		if err != nil || !slices.Equal(ids(got), tt.want) {
			t.Errorf("List %s = %v, %v; want %v", tt.name, ids(got), err, tt.want)
		}
	}

	page := postgres.Orders().PlacedAtDesc().Limit(2)
	first, err := repo.List(ctx, page)
	if err != nil || !slices.Equal(ids(first), []model.OrderID{3, 2}) {
		t.Fatalf("the first page = %v, %v", ids(first), err)
	}
	if next, err := repo.List(ctx, page.After(first[1])); err != nil || !slices.Equal(ids(next), []model.OrderID{1}) {
		t.Errorf("the page after = %v, %v; want [1]", ids(next), err)
	}
	if before, err := repo.List(ctx, page.Before(first[1])); err != nil ||
		!slices.Equal(ids(before), []model.OrderID{3}) {
		t.Errorf("the page before = %v, %v; want [3]", ids(before), err)
	}

	if n, err := repo.Count(ctx, postgres.Orders().StatusEq("open")); err != nil || n != 2 {
		t.Errorf("Count of the open orders = %d, %v; want 2", n, err)
	}
	if _, err := repo.Count(ctx, postgres.Orders().IDAsc()); err == nil {
		t.Error("Count takes a scope that orders the rows")
	}
	if ok, err := repo.Exists(ctx, postgres.Orders().CustomerIDEq(20)); err != nil || !ok {
		t.Errorf("Exists of customer 20 = %v, %v; want true", ok, err)
	}
	if ok, err := repo.Exists(ctx, postgres.Orders().CustomerIDEq(99)); err != nil || ok {
		t.Errorf("Exists of customer 99 = %v, %v; want false", ok, err)
	}

	if line, err := repo.GetLine(ctx, 1, 2); err != nil || line.SKU != "B" {
		t.Errorf("GetLine(1, 2) = %+v, %v", line, err)
	}

	if err := repo.Delete(ctx, 3); err != nil {
		t.Errorf("Delete(3): %v", err)
	}
	if err := repo.Delete(ctx, 3); !errors.Is(err, orm.ErrNotFound) {
		t.Errorf("Delete(3) again error = %v, want orm.ErrNotFound", err)
	}
	if _, err := repo.DeleteStale(ctx); err == nil {
		t.Error("DeleteStale deletes without a condition")
	}
	if n, err := repo.DeleteStale(ctx, postgres.Orders().StatusEq("closed")); err != nil || n != 1 {
		t.Errorf("DeleteStale of the closed orders = %d, %v; want 1", n, err)
	}
}

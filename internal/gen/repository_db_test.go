package gen_test

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
	"github.com/go-kanna/orm/internal/gen/testdata/pgxrepo"
	"github.com/go-kanna/orm/internal/gen/testdata/postgres"
)

// postgresDB connects to the database KANNA_ORM_TEST_POSTGRES names, with a schema of the test's own as its current
// one, dropped when the test ends, and runs ordersSchema there. Without the variable, the test is skipped.
func postgresDB(t *testing.T) *pgx.Conn {
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
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+name+"; SET search_path TO "+name+";\n"+ordersSchema); err != nil {
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
	"order" integer NOT NULL UNIQUE
);
CREATE TABLE order_lines (
	order_id bigint NOT NULL REFERENCES orders,
	line_no integer NOT NULL,
	sku text NOT NULL,
	PRIMARY KEY (order_id, line_no)
);
CREATE TABLE customers (id bigint PRIMARY KEY, name text NOT NULL);
CREATE TABLE tags (name text PRIMARY KEY);
CREATE TABLE order_tags (
	order_id bigint NOT NULL REFERENCES orders ON DELETE CASCADE,
	tag text NOT NULL REFERENCES tags,
	PRIMARY KEY (order_id, tag)
);
INSERT INTO orders (customer_id, status, placed_at, note, total, paid, contact, labels, receipt, "order") VALUES
	(10, 'open', '2026-10-01T00:00:00Z', 'gift', 100, false, 'ann@example.com', '{a,b}', '\x00ff', 1),
	(10, 'closed', '2026-10-02T00:00:00Z', NULL, 200, true, NULL, NULL, NULL, 2),
	(20, 'open', '2026-10-03T00:00:00Z', NULL, 300, false, NULL, NULL, NULL, 3);
INSERT INTO order_lines VALUES (1, 1, 'A'), (1, 2, 'B');
INSERT INTO customers VALUES (10, 'Ann'), (20, 'Bob');
INSERT INTO tags VALUES ('gift'), ('rush');
INSERT INTO order_tags VALUES (1, 'rush'), (1, 'gift'), (3, 'rush');
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
	repo := pgxrepo.NewOrderRepository(postgresDB(t))

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

func TestGeneratedRepository_PostgresPreloads(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := pgxrepo.NewOrderRepository(postgresDB(t))

	o, err := repo.Get(ctx, 1, postgres.Orders().
		WithCustomer().
		WithLines(postgres.OrderLines().LineNoDesc().WithOrder()).
		WithTags(postgres.Tags().NameAsc()))
	if err != nil {
		t.Fatal(err)
	}
	if o.Customer == nil || o.Customer.Name != "Ann" {
		t.Errorf("Customer = %+v, want Ann", o.Customer)
	}
	skus := make([]string, 0, len(o.Lines))
	for _, l := range o.Lines {
		if l.Order == nil || l.Order.ID != 1 {
			t.Errorf("the order of line %d = %+v, want 1", l.LineNo, l.Order)
		}
		skus = append(skus, l.SKU)
	}
	if !slices.Equal(skus, []string{"B", "A"}) {
		t.Errorf("Lines = %v, want [B A], by line_no descending", skus)
	}
	tags := make([]string, 0, len(o.Tags))
	for _, tag := range o.Tags {
		tags = append(tags, tag.Name)
	}
	if !slices.Equal(tags, []string{"gift", "rush"}) {
		t.Errorf("Tags = %v, want [gift rush], by name", tags)
	}

	all, err := repo.List(ctx, postgres.Orders().IDAsc().WithLines().WithTags())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[1].Lines == nil || len(all[1].Lines) != 0 || all[1].Tags == nil || len(all[2].Tags) != 1 {
		t.Errorf("List with lines and tags = %+v; want order 2 with no lines and no tags, and order 3 with one tag", all)
	}
	if plain, err := repo.Get(ctx, 1); err != nil || plain.Lines != nil || plain.Customer != nil {
		t.Errorf("Get without With = %+v, %v; want no relation loaded", plain, err)
	}
	if _, err := repo.List(ctx, postgres.Orders().WithLines(postgres.OrderLines().Limit(1))); err == nil {
		t.Error("List loads lines by a scope with a limit")
	}
}

func TestGeneratedRepository_PostgresWrites(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := pgxrepo.NewOrderRepository(postgresDB(t))

	memo, contact := "fragile", must(model.NewEmail("bob@example.com"))
	placed := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	o, err := repo.Insert(ctx, model.Order{
		ID: 99, CustomerID: 20, Status: "open", PlacedAt: placed, Memo: &memo, Total: 400, Contact: &contact,
		Labels: []string{"x"}, Receipt: []byte{1}, Position: 4,
		Lines: []model.OrderLine{{LineNo: 1, SKU: "ignored"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.ID != 4 || *o.Memo != "fragile" || o.Contact.String() != "bob@example.com" || !o.PlacedAt.Equal(placed) ||
		o.Lines != nil {
		t.Errorf("Insert = %+v; want the row as stored, with the id the database assigns and no relation", o)
	}

	o.Status, o.Memo, o.Labels = "closed", nil, nil
	if err := repo.Update(ctx, o); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Get(ctx, 4); err != nil || got.Status != "closed" || got.Memo != nil || got.Labels != nil ||
		got.Position != 4 {
		t.Errorf("Get after Update = %+v, %v", got, err)
	}
	o.ID = 9
	if err := repo.Update(ctx, o); !errors.Is(err, orm.ErrNotFound) || err.Error() !=
		"orders with id 9: orm: not found" {
		t.Errorf("Update of no row error = %v, want orm.ErrNotFound", err)
	}

	moved, err := repo.UpsertByPosition(ctx, model.Order{
		CustomerID: 10, Status: "moved", PlacedAt: placed.AddDate(1, 0, 0), Total: 500, Position: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if moved.ID != 4 || moved.Status != "moved" || moved.Total != 500 || !moved.PlacedAt.Equal(placed) {
		t.Errorf("UpsertByPosition over order 4 = %+v; want it updated but for placed_at, which it keeps", moved)
	}
	if added, err := repo.UpsertByPosition(ctx, model.Order{CustomerID: 10, Status: "new", Position: 5}); err != nil ||
		added.ID != 6 {
		t.Errorf("UpsertByPosition of a new position = %+v, %v; want order 6", added, err)
	}

	if c, err := repo.UpsertCustomer(ctx, model.Customer{ID: 10, Name: "Anne"}); err != nil || c.Name != "Anne" {
		t.Errorf("UpsertCustomer over customer 10 = %+v, %v", c, err)
	}

	if err := repo.InsertLine(ctx, model.OrderLine{OrderID: 4, LineNo: 1, SKU: "C"}); err != nil {
		t.Fatal(err)
	}
	lines, err := repo.InsertLines(ctx, []model.OrderLine{{OrderID: 4, LineNo: 2, SKU: "D"}, {OrderID: 4, LineNo: 3,
		SKU: "E"}})
	if err != nil || len(lines) != 2 || lines[0].SKU != "D" || lines[1].SKU != "E" {
		t.Errorf("InsertLines = %+v, %v", lines, err)
	}
	if lines, err := repo.InsertLines(ctx, nil); err != nil || lines == nil || len(lines) != 0 {
		t.Errorf("InsertLines of none = %#v, %v; want an empty slice", lines, err)
	}
	if err := repo.UpsertLines(ctx, []model.OrderLine{{OrderID: 4, LineNo: 3, SKU: "F"}, {OrderID: 4, LineNo: 4,
		SKU: "G"}}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, 4, postgres.Orders().WithLines(postgres.OrderLines().LineNoAsc()))
	if err != nil {
		t.Fatal(err)
	}
	skus := make([]string, 0, len(got.Lines))
	for _, l := range got.Lines {
		skus = append(skus, l.SKU)
	}
	if !slices.Equal(skus, []string{"C", "D", "F", "G"}) {
		t.Errorf("the lines of order 4 = %v, want [C D F G]", skus)
	}
	if err := repo.InsertLine(ctx, model.OrderLine{OrderID: 4, LineNo: 1, SKU: "C"}); err == nil {
		t.Error("InsertLine writes a line with the key of another")
	}

	if err := repo.InsertTags(ctx, []model.Tag{{Name: "new"}}); err != nil {
		t.Fatal(err)
	}
	tags, err := repo.UpsertTags(ctx, []model.Tag{{Name: "gift"}, {Name: "sale"}})
	if err != nil || len(tags) != 2 || tags[0].Name != "gift" || tags[1].Name != "sale" {
		t.Errorf("UpsertTags of one there and one not = %+v, %v; want both", tags, err)
	}
	if _, err := repo.UpsertTags(ctx, []model.Tag{{Name: "dup"}, {Name: "dup"}}); err == nil {
		t.Error("UpsertTags writes two rows of one key")
	}
}

func TestGeneratedRepository_PostgresQueries(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := pgxrepo.NewOrderRepository(postgresDB(t))

	counts, err := repo.MonthCounts(ctx, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
	if err != nil || len(counts) != 1 || counts[0].Count != 2 || counts[0].Month.Month() != time.October {
		t.Errorf("MonthCounts since October 2 = %+v, %v; want 2 in October", counts, err)
	}
	if counts, err := repo.MonthCounts(ctx, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil ||
		counts == nil || len(counts) != 0 {
		t.Errorf("MonthCounts of none = %#v, %v; want an empty slice", counts, err)
	}

	if total, err := repo.TotalOf(ctx, 10); err != nil || total.Name != "Ann" || total.Total != 300 {
		t.Errorf("TotalOf(10) = %+v, %v; want Ann, 300", total, err)
	}
	if _, err := repo.TotalOf(ctx, 99); !errors.Is(err, orm.ErrNotFound) ||
		err.Error() != "query TotalOf: orm: not found" {
		t.Errorf("TotalOf(99) error = %v, want orm.ErrNotFound", err)
	}

	if n, err := repo.CloseOld(ctx, time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)); err != nil || n != 1 {
		t.Errorf("CloseOld before October 3 = %d, %v; want 1, order 2 being closed already", n, err)
	}
	if err := repo.Reopen(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.Count(ctx, postgres.Orders().StatusEq("closed")); err != nil || n != 1 {
		t.Errorf("the closed orders = %d, %v; want order 1 alone", n, err)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

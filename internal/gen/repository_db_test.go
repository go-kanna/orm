package gen_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
	ppgx "github.com/go-kanna/orm/internal/gen/testdata/postgres/pgx"
	prepository "github.com/go-kanna/orm/internal/gen/testdata/postgres/repository"
	pscopes "github.com/go-kanna/orm/internal/gen/testdata/postgres/scopes"
)

// postgresSchema creates a schema of the test's own in the database KANNA_ORM_TEST_POSTGRES names, dropped when the
// test ends, runs ordersSchema there, and returns the URL of the database with that schema as its current one. Without
// the variable, the test is skipped.
func postgresSchema(t *testing.T) string {
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
	return url + "&search_path=" + name
}

// pgxConn connects through pgx to the database at url, closed when the test ends.
func pgxConn(t *testing.T, url string) *pgx.Conn {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	return conn
}

// pgxRepository opens OrderRepository through pgx on the database at url, closed when the test ends.
func pgxRepository(t *testing.T, url string) prepository.OrderRepository {
	t.Helper()

	return ppgx.NewOrderRepository(pgxConn(t, url))
}

// onPostgres runs test on OrderRepository through each driver, each on a schema of its own.
func onPostgres(t *testing.T, test func(t *testing.T, repo prepository.OrderRepository)) {
	t.Helper()

	repos := map[string]func(t *testing.T, url string) prepository.OrderRepository{"pgx": pgxRepository}
	if pgsqlRepository != nil {
		repos["database/sql"] = pgsqlRepository
	}
	for drv, open := range repos {
		t.Run(drv, func(t *testing.T) {
			t.Parallel()

			test(t, open(t, postgresSchema(t)))
		})
	}
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
	receipt bytea,
	"order" integer NOT NULL UNIQUE
);
CREATE TABLE order_lines (
	order_id bigint NOT NULL REFERENCES orders,
	line_no integer NOT NULL,
	sku text NOT NULL,
	PRIMARY KEY (order_id, line_no)
);
CREATE TABLE customers (id bigint PRIMARY KEY, name text NOT NULL, nickname text);
CREATE TABLE tags (name text PRIMARY KEY);
CREATE TABLE order_tags (
	order_id bigint NOT NULL REFERENCES orders ON DELETE CASCADE,
	tag_id text NOT NULL REFERENCES tags,
	PRIMARY KEY (order_id, tag_id)
);
CREATE TABLE labels (order_id bigint PRIMARY KEY REFERENCES orders ON DELETE CASCADE, names text[]);
CREATE TABLE line_notes (
	order_id bigint NOT NULL,
	line_no integer NOT NULL,
	body text NOT NULL,
	FOREIGN KEY (order_id, line_no) REFERENCES order_lines
);
CREATE TABLE line_tags (
	order_id bigint NOT NULL,
	line_no integer NOT NULL,
	tag_id text NOT NULL REFERENCES tags,
	PRIMARY KEY (order_id, line_no, tag_id),
	FOREIGN KEY (order_id, line_no) REFERENCES order_lines
);
INSERT INTO orders (customer_id, status, placed_at, note, total, paid, contact, receipt, "order") VALUES
	(10, 'open', '2026-10-01T00:00:00Z', 'gift', 100, false, 'ann@example.com', '\x00ff', 1),
	(10, 'closed', '2026-10-02T00:00:00Z', NULL, 200, true, NULL, NULL, 2),
	(20, 'open', '2026-10-03T00:00:00Z', NULL, 300, false, NULL, NULL, 3);
INSERT INTO order_lines VALUES (1, 1, 'A'), (1, 2, 'B');
INSERT INTO customers VALUES (10, 'Ann', NULL), (20, 'Bob', 'bobby');
INSERT INTO tags VALUES ('gift'), ('rush');
INSERT INTO order_tags VALUES (1, 'rush'), (1, 'gift'), (3, 'rush');
INSERT INTO labels VALUES (1, '{a,b}');
INSERT INTO line_notes VALUES (1, 1, 'wrap it'), (1, 1, 'fragile'), (1, 2, 'spare');
INSERT INTO line_tags VALUES (1, 2, 'rush'), (1, 2, 'gift'), (1, 1, 'gift');
`

func ids(orders []model.Order) []model.OrderID {
	out := make([]model.OrderID, 0, len(orders))
	for _, o := range orders {
		out = append(out, o.ID)
	}
	return out
}

// The errors of a read continuing from a row, which a scope without an order, two rows, or First given Before makes.
const (
	pageOfNoOrder = "orm: a page continues from a row in the order of its scope, which has none: " +
		"order it, as by the primary key"
	pageFromTwoRows = "orm: a page continues from one row, but its scope is given two"
	firstBefore     = "orm: First reads the first row in its scope's order, but its scope goes the other way, " +
		"to the rows before a row"
)

func TestGeneratedRepository_Postgres(t *testing.T) {
	t.Parallel()

	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		ctx := t.Context()

		o, err := repo.Get(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if o.Status != "open" || o.Memo == nil || *o.Memo != "gift" || o.Contact == nil || o.Contact.String() !=
			"ann@example.com" || !slices.Equal(o.Receipt, []byte{0, 0xff}) || o.Position != 1 || o.PlacedAt.Day() != 1 {
			t.Errorf("Get(1) = %+v", o)
		}
		if _, err := repo.Get(ctx, 9); !errors.Is(err, orm.ErrNotFound) || err.Error() != "orders with id 9: orm: not found" {
			t.Errorf("Get(9) error = %v, want orm.ErrNotFound", err)
		}
		if _, err := repo.Get(ctx, 2, pscopes.Order{}.StatusEq("open")); !errors.Is(err, orm.ErrNotFound) {
			t.Errorf("Get(2) of the open orders error = %v, want orm.ErrNotFound", err)
		}

		if o, err := repo.First(ctx, pscopes.Order{}.PlacedAtDesc()); err != nil || o.ID != 3 {
			t.Errorf("First, latest first = %v, %v; want 3", o.ID, err)
		}
		if o, err := repo.First(ctx, pscopes.Order{}.StatusEq("open")); err != nil || o.ID != 1 {
			t.Errorf("First of no order = %v, %v; want 1, the first by the primary key", o.ID, err)
		}
		if _, err := repo.Get(ctx, 1, pscopes.Order{}.Offset(1)); err == nil ||
			err.Error() != "orm: Get reads one row by its key, but its scope keeps only some of the rows" {
			t.Errorf("Get with an offset error = %v", err)
		}
		for _, tt := range []struct {
			name string
			list func() ([]model.Order, error)
			want []model.OrderID
		}{
			{"open", func() ([]model.Order, error) { return repo.List(ctx, pscopes.Order{}.StatusEq("open").IDAsc()) },
				[]model.OrderID{1, 3}},
			{"of a customer", func() ([]model.Order, error) {
				return repo.ListByCustomer(ctx, 10, pscopes.Order{}.IDDesc())
			}, []model.OrderID{2, 1}},
			{"none", func() ([]model.Order, error) { return repo.List(ctx, pscopes.Order{}.TotalGt(1000)) },
				[]model.OrderID{}},
		} {
			got, err := tt.list()
			if err != nil || !slices.Equal(ids(got), tt.want) {
				t.Errorf("List %s = %v, %v; want %v", tt.name, ids(got), err, tt.want)
			}
		}

		page := pscopes.Order{}.PlacedAtDesc().Limit(2)
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
		// Orders 1 and 2 are both customer 10's, so the first page ends between two rows equal in the order.
		tied := pscopes.Order{}.CustomerIDDesc().Limit(2)
		if first, err = repo.List(ctx, tied); err != nil || !slices.Equal(ids(first), []model.OrderID{3, 2}) {
			t.Fatalf("the first page ending among equal rows = %v, %v; want [3 2]", ids(first), err)
		}
		if next, err := repo.List(ctx, tied.After(first[1])); err != nil || !slices.Equal(ids(next), []model.OrderID{1}) {
			t.Errorf("the page after one among equal rows = %v, %v; want [1]", ids(next), err)
		}
		// The first page of a scope without an order comes as the database reads the rows, which no page can continue
		// from.
		unordered := pscopes.Order{}.Limit(2)
		if first, err = repo.List(ctx, unordered); err != nil || len(first) != 2 {
			t.Fatalf("the first page of no order = %v, %v", ids(first), err)
		}
		if _, err := repo.List(ctx, unordered.After(first[1])); err == nil || err.Error() != pageOfNoOrder {
			t.Errorf("the page after one of no order error = %v", err)
		}
		if _, err := repo.List(ctx, page.After(first[0]), pscopes.Order{}.Before(first[1])); err == nil ||
			err.Error() != pageFromTwoRows {
			t.Errorf("a page from two rows error = %v", err)
		}
		latest := pscopes.Order{}.PlacedAtDesc()
		if first, err = repo.List(ctx, latest); err != nil || !slices.Equal(ids(first), []model.OrderID{3, 2, 1}) {
			t.Fatalf("the orders, latest first = %v, %v", ids(first), err)
		}
		if o, err := repo.First(ctx, latest.After(first[0])); err != nil || o.ID != 2 {
			t.Errorf("First after 3 = %v, %v; want 2", o.ID, err)
		}
		if _, err := repo.First(ctx, latest.Before(first[2])); err == nil || err.Error() != firstBefore {
			t.Errorf("First before 1 error = %v", err)
		}

		if n, err := repo.Count(ctx, pscopes.Order{}.StatusEq("open")); err != nil || n != 2 {
			t.Errorf("Count of the open orders = %d, %v; want 2", n, err)
		}
		if _, err := repo.Count(ctx, pscopes.Order{}.IDAsc()); err == nil {
			t.Error("Count takes a scope that orders the rows")
		}
		if ok, err := repo.Exists(ctx, pscopes.Order{}.CustomerIDEq(20)); err != nil || !ok {
			t.Errorf("Exists of customer 20 = %v, %v; want true", ok, err)
		}
		if ok, err := repo.Exists(ctx, pscopes.Order{}.CustomerIDEq(99)); err != nil || ok {
			t.Errorf("Exists of customer 99 = %v, %v; want false", ok, err)
		}

		if line, err := repo.GetLine(ctx, 1, 2); err != nil || line.SKU != "B" {
			t.Errorf("GetLine(1, 2) = %+v, %v", line, err)
		}
		if l, err := repo.GetLabel(ctx, 1); err != nil || !slices.Equal(l.Names, []string{"a", "b"}) {
			t.Errorf("GetLabel(1) = %+v, %v; want [a b]", l, err)
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
		if n, err := repo.DeleteStale(ctx, pscopes.Order{}.StatusEq("closed")); err != nil || n != 1 {
			t.Errorf("DeleteStale of the closed orders = %d, %v; want 1", n, err)
		}
	})
}

func TestGeneratedRepository_PostgresInOfNone(t *testing.T) {
	t.Parallel()

	o := pscopes.Order{}
	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		for _, tt := range []struct {
			name  string
			scope pscopes.Order
			want  []model.OrderID
		}{
			{"In of none", o.IDIn(), nil},
			{"In of an empty slice", o.IDIn([]model.OrderID{}...), nil},
			{"Not of In of none", o.Not(o.IDIn()), []model.OrderID{1, 2, 3}},
			{"Not of In of an empty slice", o.Not(o.IDIn([]model.OrderID{}...)), []model.OrderID{1, 2, 3}},
			{"Not of In of none, of a column that may be NULL", o.Not(o.MemoIn()), []model.OrderID{1, 2, 3}},
			{"Or of In of none", o.Or(o.IDIn(), o.IDEq(3)), []model.OrderID{3}},
			{"Not of Or of In of none", o.Not(o.Or(o.IDIn(), o.IDEq(3))), []model.OrderID{1, 2}},
			{"NotIn of none", o.IDNotIn(), []model.OrderID{1, 2, 3}},
		} {
			got, err := repo.List(t.Context(), tt.scope.IDAsc())
			if err != nil || !slices.Equal(ids(got), tt.want) {
				t.Errorf("List, %s = %v, %v; want %v", tt.name, ids(got), err, tt.want)
			}
		}
	})
}

// A slice of no values, nil or not, is the empty set to a query or a scope written by hand: = ANY of it matches no row,
// and <> ALL of it every row.
func TestGeneratedRepository_PostgresSliceArgument(t *testing.T) {
	t.Parallel()

	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		ctx := t.Context()
		for _, tt := range []struct {
			ids       []model.OrderID
			of, notOf []int64
			others    []model.OrderID
		}{
			{[]model.OrderID{1, 3}, []int64{10, 20}, []int64{10}, []model.OrderID{2}},
			{[]model.OrderID{}, []int64{}, []int64{10, 10, 20}, []model.OrderID{1, 2, 3}},
			{nil, []int64{}, []int64{10, 10, 20}, []model.OrderID{1, 2, 3}},
		} {
			if got, err := repo.CustomersOf(ctx, tt.ids); err != nil || !slices.Equal(got, tt.of) {
				t.Errorf("CustomersOf(%#v) = %v, %v; want %v", tt.ids, got, err, tt.of)
			}
			if got, err := repo.CustomersNotOf(ctx, tt.ids); err != nil || !slices.Equal(got, tt.notOf) {
				t.Errorf("CustomersNotOf(%#v) = %v, %v; want %v", tt.ids, got, err, tt.notOf)
			}
			got, err := repo.ListOthers(ctx, tt.ids, pscopes.Order{}.IDAsc())
			if err != nil || !slices.Equal(ids(got), tt.others) {
				t.Errorf("ListOthers(%#v) = %v, %v; want %v", tt.ids, ids(got), err, tt.others)
			}
		}
		for _, tt := range []struct {
			name  string
			scope pscopes.Order
			want  []model.OrderID
		}{
			{"Except of some", pscopes.Order{}.Except(1, 3), []model.OrderID{2}},
			{"Except of none", pscopes.Order{}.Except(), []model.OrderID{1, 2, 3}},
			{"Except of an empty slice", pscopes.Order{}.Except([]int64{}...), []model.OrderID{1, 2, 3}},
		} {
			got, err := repo.List(ctx, tt.scope.IDAsc())
			if err != nil || !slices.Equal(ids(got), tt.want) {
				t.Errorf("List, %s = %v, %v; want %v", tt.name, ids(got), err, tt.want)
			}
		}
	})
}

func TestGeneratedRepository_PostgresPreloads(t *testing.T) {
	t.Parallel()

	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		ctx := t.Context()

		o, err := repo.Get(ctx, 1, pscopes.Order{}.
			WithCustomer().
			WithLines(pscopes.OrderLine{}.LineNoDesc().WithOrder()).
			WithTags(pscopes.Tag{}.NameAsc()))
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

		lines, err := repo.Get(ctx, 1, pscopes.Order{}.WithLines(pscopes.OrderLine{}.LineNoAsc().
			WithNotes(pscopes.LineNote{}.BodyAsc().WithLine()).
			WithTags(pscopes.LineTag{}.TagIDAsc().WithTag())))
		if err != nil {
			t.Fatal(err)
		}
		if got := compositeRelations(lines.Lines); got != "1: [fragile wrap it] [gift]; 2: [spare] [gift rush]" {
			t.Errorf("the notes and tags of the lines of order 1, by both columns of their key = %s", got)
		}

		all, err := repo.List(ctx, pscopes.Order{}.IDAsc().WithLines().WithTags())
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 3 || all[1].Lines == nil || len(all[1].Lines) != 0 || all[1].Tags == nil || len(all[2].Tags) != 1 {
			t.Errorf("List with lines and tags = %+v; want order 2 with no lines and no tags, and order 3 with one tag", all)
		}
		if plain, err := repo.Get(ctx, 1); err != nil || plain.Lines != nil || plain.Customer != nil {
			t.Errorf("Get without With = %+v, %v; want no relation loaded", plain, err)
		}
		paged, err := repo.List(ctx, pscopes.Order{}.IDAsc().WithLines(pscopes.OrderLine{}.Offset(1)))
		if err != nil {
			t.Fatal(err)
		}
		if len(paged) != 3 || len(paged[0].Lines) != 1 || paged[0].Lines[0].LineNo != 2 || paged[1].Lines == nil {
			t.Errorf("List with the lines of each order after the first, by their key = %+v", paged)
		}
	})
}

func TestGeneratedRepository_PostgresWrites(t *testing.T) {
	t.Parallel()

	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		ctx := t.Context()

		memo, contact := "fragile", must(model.NewEmail("bob@example.com"))
		placed := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
		o, err := repo.Insert(ctx, model.Order{
			CustomerID: 20, Status: "open", PlacedAt: placed, Memo: &memo, Total: 400, Contact: &contact,
			Receipt: []byte{1}, Position: 4,
			Lines: []model.OrderLine{{LineNo: 1, SKU: "ignored"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if o.ID != 4 || *o.Memo != "fragile" || o.Contact.String() != "bob@example.com" || !o.PlacedAt.Equal(placed) ||
			o.Lines != nil {
			t.Errorf("Insert = %+v; want the row as stored, with the id the database assigns and no relation", o)
		}

		o.Status, o.Memo = "closed", nil
		if err := repo.Update(ctx, o); err != nil {
			t.Fatal(err)
		}
		if got, err := repo.Get(ctx, 4); err != nil || got.Status != "closed" || got.Memo != nil ||
			got.Position != 4 {
			t.Errorf("Get after Update = %+v, %v", got, err)
		}
		o.ID = 9
		if err := repo.Update(ctx, o); !errors.Is(err, orm.ErrNotFound) || err.Error() !=
			"orders with id 9: orm: not found" {
			t.Errorf("Update of no row error = %v, want orm.ErrNotFound", err)
		}

		if l, err := repo.UpsertLabel(ctx, model.Label{OrderID: 1, Names: []string{"c"}}); err != nil ||
			!slices.Equal(l.Names, []string{"c"}) {
			t.Errorf("UpsertLabel over the labels of order 1 = %+v, %v; want [c]", l, err)
		}
		if l, err := repo.UpsertLabel(ctx, model.Label{OrderID: 4}); err != nil || l.OrderID != 4 || l.Names != nil {
			t.Errorf("UpsertLabel of no labels = %+v, %v; want NULL", l, err)
		}

		moved, err := repo.UpsertByPosition(ctx, model.Order{
			CustomerID: 10, Status: "given", PlacedAt: placed.AddDate(1, 0, 0), Total: 900, Position: 4,
		}, pscopes.OrderChanges().SetStatus("moved").SetTotal(500))
		if err != nil {
			t.Fatal(err)
		}
		if moved.ID != 4 || moved.Status != "moved" || moved.Total != 500 || !moved.PlacedAt.Equal(placed) {
			t.Errorf("UpsertByPosition over order 4 = %+v; want its status and total alone changed", moved)
		}
		if added, err := repo.UpsertByPosition(ctx, model.Order{CustomerID: 10, Status: "new", Position: 5},
			pscopes.OrderChanges().SetStatus("moved")); err != nil || added.ID != 6 || added.Status != "new" {
			t.Errorf("UpsertByPosition of a new position = %+v, %v; want order 6 as given", added, err)
		}
		if kept, err := repo.UpsertByPosition(ctx, model.Order{Status: "given", Position: 4},
			pscopes.OrderChanges()); err != nil || kept.ID != 4 || kept.Status != "moved" {
			t.Errorf("UpsertByPosition of no changes over order 4 = %+v, %v; want it as it was", kept, err)
		}
		if _, err := repo.UpsertByPosition(ctx, model.Order{Position: 4},
			pscopes.OrderChanges().SetPosition(9)); err == nil || !strings.Contains(err.Error(), "meets its row by order") {
			t.Errorf("UpsertByPosition changing its key error = %v", err)
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
		got, err := repo.Get(ctx, 4, pscopes.Order{}.WithLines(pscopes.OrderLine{}.LineNoAsc()))
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
	})
}

func TestGeneratedRepository_PostgresQueries(t *testing.T) {
	t.Parallel()

	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		ctx := t.Context()

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
		if n, err := repo.Count(ctx, pscopes.Order{}.StatusEq("closed")); err != nil || n != 1 {
			t.Errorf("the closed orders = %d, %v; want order 1 alone", n, err)
		}
	})
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// compositeRelations writes the notes and the tags of each of lines, which reach them by both columns of their key,
// as "line_no: [bodies] [tags]", checking each note's line is its own.
func compositeRelations(lines []model.OrderLine) string {
	described := make([]string, 0, len(lines))
	for _, l := range lines {
		bodies := make([]string, 0, len(l.Notes))
		for _, n := range l.Notes {
			if n.Line == nil || n.Line.LineNo != l.LineNo {
				return fmt.Sprintf("the line of a note of line %d is %+v", l.LineNo, n.Line)
			}
			bodies = append(bodies, n.Body)
		}
		tags := make([]string, 0, len(l.Tags))
		for _, t := range l.Tags {
			if t.Tag == nil || t.Tag.Name != t.TagID {
				return fmt.Sprintf("the tag of a line tag of line %d is %+v", l.LineNo, t.Tag)
			}
			tags = append(tags, t.TagID)
		}
		described = append(described, fmt.Sprintf("%d: %v %v", l.LineNo, bodies, tags))
	}
	return strings.Join(described, "; ")
}

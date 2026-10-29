package gen_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
	"github.com/go-kanna/orm/internal/gen/testdata/mysql"
	"github.com/go-kanna/orm/internal/gen/testdata/mysqlrepo"
)

// mysqlDB creates a database of the test's own on the server KANNA_ORM_TEST_MYSQL names, dropped when the test ends,
// runs mysqlOrdersSchema there, and returns a connection to it. Without the variable, the test is skipped.
func mysqlDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("KANNA_ORM_TEST_MYSQL")
	if dsn == "" {
		t.Skip("KANNA_ORM_TEST_MYSQL is not set; make db-up test-db sets it")
	}
	ctx := t.Context()
	server, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "test_" + strings.ToLower(rand.Text())
	t.Cleanup(func() {
		if _, err := server.ExecContext(context.WithoutCancel(ctx), "DROP DATABASE IF EXISTS "+name); err != nil {
			t.Error(err)
		}
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := server.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}

	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName, cfg.ParseTime, cfg.MultiStatements = name, true, true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := db.ExecContext(ctx, mysqlOrdersSchema); err != nil {
		t.Fatal(err)
	}
	return db
}

// mysqlOrdersSchema is ordersSchema on MySQL, but for labels, an array, which MySQL has none of.
const mysqlOrdersSchema = "CREATE TABLE orders (" + `
	id bigint AUTO_INCREMENT PRIMARY KEY,
	customer_id bigint NOT NULL,
	status varchar(32) NOT NULL,
	placed_at datetime(6) NOT NULL,
	note text,
	total bigint NOT NULL,
	paid boolean NOT NULL,
	contact varchar(255),
	receipt blob,
	` + "`order`" + ` int NOT NULL UNIQUE
);
CREATE TABLE order_lines (
	order_id bigint NOT NULL REFERENCES orders (id),
	line_no int NOT NULL,
	sku varchar(64) NOT NULL,
	PRIMARY KEY (order_id, line_no)
);
CREATE TABLE customers (id bigint PRIMARY KEY, name varchar(255) NOT NULL);
CREATE TABLE tags (name varchar(64) PRIMARY KEY);
CREATE TABLE order_tags (
	order_id bigint NOT NULL,
	tag varchar(64) NOT NULL,
	PRIMARY KEY (order_id, tag),
	FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE CASCADE,
	FOREIGN KEY (tag) REFERENCES tags (name)
);
INSERT INTO orders (customer_id, status, placed_at, note, total, paid, contact, receipt, ` + "`order`" + `) VALUES
	(10, 'open', '2026-10-01 00:00:00', 'gift', 100, false, 'ann@example.com', X'00ff', 1),
	(10, 'closed', '2026-10-02 00:00:00', NULL, 200, true, NULL, NULL, 2),
	(20, 'open', '2026-10-03 00:00:00', NULL, 300, false, NULL, NULL, 3);
INSERT INTO order_lines VALUES (1, 1, 'A'), (1, 2, 'B');
INSERT INTO customers VALUES (10, 'Ann'), (20, 'Bob');
INSERT INTO tags VALUES ('gift'), ('rush');
INSERT INTO order_tags VALUES (1, 'rush'), (1, 'gift'), (3, 'rush');
`

func TestGeneratedRepository_MySQL(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := mysqlrepo.NewOrderRepository(mysqlDB(t))

	o, err := repo.Get(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "open" || o.Memo == nil || *o.Memo != "gift" || o.Contact == nil || o.Contact.String() !=
		"ann@example.com" || !slices.Equal(o.Receipt, []byte{0, 0xff}) || o.Position != 1 || o.PlacedAt.Day() != 1 ||
		o.Paid {
		t.Errorf("Get(1) = %+v", o)
	}
	if _, err := repo.Get(ctx, 9); !errors.Is(err, orm.ErrNotFound) || err.Error() != "orders with id 9: orm: not found" {
		t.Errorf("Get(9) error = %v, want orm.ErrNotFound", err)
	}
	if o, err := repo.First(ctx, mysql.Orders().PlacedAtDesc()); err != nil || o.ID != 3 {
		t.Errorf("First, latest first = %v, %v; want 3", o.ID, err)
	}
	if got, err := repo.ListByCustomer(ctx, 10, mysql.Orders().IDDesc()); err != nil ||
		!slices.Equal(ids(got), []model.OrderID{2, 1}) {
		t.Errorf("ListByCustomer(10) = %v, %v; want [2 1]", ids(got), err)
	}

	page := mysql.Orders().PlacedAtDesc().Limit(2)
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

	if n, err := repo.Count(ctx, mysql.Orders().StatusEq("open")); err != nil || n != 2 {
		t.Errorf("Count of the open orders = %d, %v; want 2", n, err)
	}
	if ok, err := repo.Exists(ctx, mysql.Orders().CustomerIDEq(99)); err != nil || ok {
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
	if n, err := repo.DeleteStale(ctx, mysql.Orders().StatusEq("closed")); err != nil || n != 1 {
		t.Errorf("DeleteStale of the closed orders = %d, %v; want 1", n, err)
	}
}

func TestGeneratedRepository_MySQLPreloads(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := mysqlDB(t)
	repo := mysqlrepo.NewOrderRepository(db)

	// More orders than go in one IN (...), each with two lines and two tags.
	if _, err := db.ExecContext(ctx, `
SET SESSION cte_max_recursion_depth = 2000;
INSERT INTO orders (customer_id, status, placed_at, total, paid, `+"`order`"+`)
WITH RECURSIVE n (i) AS (SELECT 4 UNION ALL SELECT i + 1 FROM n WHERE i < 1503)
SELECT 10 + i % 2 * 10, 'bulk', '2026-11-01 00:00:00', i, false, i FROM n;
INSERT INTO order_lines SELECT id, 1, 'X' FROM orders WHERE status = 'bulk';
INSERT INTO order_lines SELECT id, 2, 'Y' FROM orders WHERE status = 'bulk';
INSERT INTO order_tags SELECT id, 'rush' FROM orders WHERE status = 'bulk';
INSERT INTO order_tags SELECT id, 'gift' FROM orders WHERE status = 'bulk';
`); err != nil {
		t.Fatal(err)
	}

	all, err := repo.List(ctx, mysql.Orders().IDAsc().
		WithCustomer().
		WithLines(mysql.OrderLines().LineNoDesc().WithOrder()).
		WithTags(mysql.Tags().NameAsc()))
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1503 {
		t.Fatalf("List = %d orders, want 1503", len(all))
	}
	for _, o := range all {
		skus, tags := make([]string, 0, len(o.Lines)), make([]string, 0, len(o.Tags))
		for _, l := range o.Lines {
			if l.Order == nil || l.Order.ID != o.ID {
				t.Fatalf("the order of a line of order %d = %+v", o.ID, l.Order)
			}
			skus = append(skus, l.SKU)
		}
		for _, tag := range o.Tags {
			tags = append(tags, tag.Name)
		}
		want := struct{ skus, tags []string }{[]string{"Y", "X"}, []string{"gift", "rush"}}
		switch o.ID {
		case 1:
			want.skus = []string{"B", "A"}
		case 2:
			want.skus, want.tags = nil, nil
		case 3:
			want.skus, want.tags = nil, []string{"rush"}
		}
		if o.Customer == nil || o.Customer.ID != o.CustomerID || !slices.Equal(skus, want.skus) ||
			!slices.Equal(tags, want.tags) {
			t.Fatalf("order %d: customer %+v, lines %v, tags %v; want %v, by line_no descending and name", o.ID,
				o.Customer, skus, tags, want)
		}
	}
}

func TestGeneratedRepository_MySQLQueries(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := mysqlrepo.NewOrderRepository(mysqlDB(t))

	counts, err := repo.MonthCounts(ctx, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
	if err != nil || len(counts) != 1 || counts[0].Count != 2 || counts[0].Month.Month() != time.October {
		t.Errorf("MonthCounts since October 2 = %+v, %v; want 2 in October", counts, err)
	}
	if total, err := repo.TotalOf(ctx, 10); err != nil || total.Name != "Ann" || total.Total != 300 {
		t.Errorf("TotalOf(10) = %+v, %v; want Ann, 300", total, err)
	}
	if _, err := repo.TotalOf(ctx, 99); !errors.Is(err, orm.ErrNotFound) {
		t.Errorf("TotalOf(99) error = %v, want orm.ErrNotFound", err)
	}
	if n, err := repo.CloseOld(ctx, time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)); err != nil || n != 1 {
		t.Errorf("CloseOld before October 3 = %d, %v; want 1", n, err)
	}
	if err := repo.Reopen(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.Count(ctx, mysql.Orders().StatusEq("closed")); err != nil || n != 1 {
		t.Errorf("the closed orders = %d, %v; want order 1 alone", n, err)
	}
}

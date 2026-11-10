package gen_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-kanna/internal/cli"
	"github.com/go-sql-driver/mysql"
)

// qualifiedScopeModels are the models of the tests of scopes naming their table, given to the relations a With loads:
// a has many, a many to many, and a has many of a table to itself.
const qualifiedScopeModels = `
-- model/model.go --
package model

type Customer struct {
	ID     int64
	Name   string
	Orders []Order
}

type Order struct {
	ID         int64
	CustomerID int64
	Total      int64
	Tags       []Tag
}

type Tag struct {
	ID     string
	Weight int64
}

type Employee struct {
	ID        int64
	ManagerID *int64
	Level     int64
	Reports   []Employee ` + "`orm:\"fk=manager_id\"`" + `
}

-- scopes/scopes.go --
package scopes

//kanna:scopes model-package=../model
-- scopes/scopes.sql --
-- scope: Order.Over(v int64)
orders.total > :v

-- scope: Order.HasLines()
EXISTS (SELECT 1 FROM order_lines l WHERE l.order_id = orders.id)

-- scope: Tag.Heavy(v int64)
tags.weight > :v

-- scope: Tag.UsedTwice()
(SELECT count(*) FROM order_tags t WHERE t.tag_id = tags.id) > 1

-- scope: Employee.Senior(v int64)
employees.level >= :v

-- scope: Employee.Managing()
EXISTS (SELECT 1 FROM employees r WHERE r.manager_id = employees.id)
-- store/store.go --
package store

import (
	"context"

	"example.com/shop/model"
	"example.com/shop/scopes"
)

//kanna:repository
type CustomerRepository interface {
	List(ctx context.Context, s ...scopes.Customer) ([]model.Customer, error)
}

//kanna:repository
type EmployeeRepository interface {
	List(ctx context.Context, s ...scopes.Employee) ([]model.Employee, error)
}
-- store/store_test.go --
package store

import (
	"database/sql"
	"fmt"
	"os"
	"slices"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"example.com/shop/scopes"
)

func TestQualifiedScopes(t *testing.T) {
	db, err := sql.Open(os.Getenv("KANNA_QUALIFIED_DRIVER"), os.Getenv("KANNA_QUALIFIED_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()

	for _, tt := range []struct {
		name   string
		orders scopes.Order
		want   []string
	}{
		{"a column of the table", scopes.Orders().IDAsc().Over(150), []string{"1:[2]", "2:[3]"}},
		{"a correlated subquery", scopes.Orders().IDAsc().HasLines(), []string{"1:[1]", "2:[3]"}},
		{"a page of each", scopes.Orders().IDDesc().Over(50).Limit(1), []string{"1:[2]", "2:[3]"}},
		{"a column of the table through a table between", scopes.Orders().IDAsc().WithTags(scopes.Tags().IDAsc().Heavy(1)),
			[]string{"1:[1[a] 2[]]", "2:[3[]]"}},
		{"a correlated subquery through a table between", scopes.Orders().IDAsc().WithTags(scopes.Tags().IDAsc().UsedTwice()),
			[]string{"1:[1[b] 2[b]]", "2:[3[b]]"}},
	} {
		customers, err := NewCustomerRepository(db).List(ctx, scopes.Customers().IDAsc().WithOrders(tt.orders))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		var got []string
		for _, c := range customers {
			var orders []string
			for _, o := range c.Orders {
				if o.Tags == nil {
					orders = append(orders, fmt.Sprint(o.ID))
					continue
				}
				var tags []string
				for _, tag := range o.Tags {
					tags = append(tags, tag.ID)
				}
				orders = append(orders, fmt.Sprintf("%d%v", o.ID, tags))
			}
			got = append(got, fmt.Sprintf("%d:%v", c.ID, orders))
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: customers = %v, want %v", tt.name, got, tt.want)
		}
	}

	for _, tt := range []struct {
		name      string
		employees scopes.Employee
		want      []string
	}{
		{"reports by a column of the table", scopes.Employees().IDAsc().WithReports(scopes.Employees().IDAsc().Senior(2)),
			[]string{"1:[3]", "2:[]", "3:[]", "4:[]"}},
		{"reports by a correlated subquery", scopes.Employees().IDAsc().WithReports(scopes.Employees().IDAsc().Managing()),
			[]string{"1:[2]", "2:[]", "3:[]", "4:[]"}},
		{"a page of reports", scopes.Employees().IDAsc().WithReports(scopes.Employees().IDDesc().Managing().Limit(1)),
			[]string{"1:[2]", "2:[]", "3:[]", "4:[]"}},
	} {
		employees, err := NewEmployeeRepository(db).List(ctx, tt.employees)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		var got []string
		for _, e := range employees {
			ids := []int64{}
			for _, r := range e.Reports {
				ids = append(ids, r.ID)
			}
			got = append(got, fmt.Sprintf("%d:%v", e.ID, ids))
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: employees = %v, want %v", tt.name, got, tt.want)
		}
	}
}
`

// qualifiedScopeSchema creates the tables of qualifiedScopeModels, and rows in them, in SQL both databases read, with
// text the type of a key of text.
func qualifiedScopeSchema(text string) string {
	return fmt.Sprintf(`
CREATE TABLE customers (id bigint PRIMARY KEY, name %[1]s NOT NULL);
CREATE TABLE orders (
	id bigint PRIMARY KEY,
	customer_id bigint NOT NULL,
	total bigint NOT NULL,
	FOREIGN KEY (customer_id) REFERENCES customers (id)
);
CREATE TABLE order_lines (
	id bigint PRIMARY KEY,
	order_id bigint NOT NULL,
	FOREIGN KEY (order_id) REFERENCES orders (id)
);
CREATE TABLE tags (id %[1]s PRIMARY KEY, weight bigint NOT NULL);
CREATE TABLE order_tags (
	order_id bigint NOT NULL,
	tag_id %[1]s NOT NULL,
	PRIMARY KEY (order_id, tag_id),
	FOREIGN KEY (order_id) REFERENCES orders (id),
	FOREIGN KEY (tag_id) REFERENCES tags (id)
);
CREATE TABLE employees (
	id bigint PRIMARY KEY,
	manager_id bigint,
	level bigint NOT NULL,
	FOREIGN KEY (manager_id) REFERENCES employees (id)
);
INSERT INTO customers VALUES (1, 'Ann'), (2, 'Bob');
INSERT INTO orders VALUES (1, 1, 100), (2, 1, 200), (3, 2, 300);
INSERT INTO order_lines VALUES (1, 1), (2, 3);
INSERT INTO tags VALUES ('a', 2), ('b', 1);
INSERT INTO order_tags VALUES (1, 'a'), (1, 'b'), (2, 'b'), (3, 'b');
INSERT INTO employees VALUES (1, NULL, 3), (2, 1, 1), (3, 1, 2), (4, 2, 1);
`, text)
}

// TestCommand_WithQualifiedScopesPostgres loads relations by scopes naming their table, on PostgreSQL.
func TestCommand_WithQualifiedScopesPostgres(t *testing.T) {
	t.Parallel()
	url := os.Getenv("KANNA_ORM_TEST_POSTGRES")
	if url == "" {
		t.Skip("integration database is not configured")
	}
	schema := fmt.Sprintf("kanna_qualified_%d", time.Now().UnixNano())
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.WithoutCancel(t.Context()), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		_ = db.Close()
	})
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(t.Context(), "CREATE SCHEMA "+schema+"; SET search_path TO "+schema+";"+
		qualifiedScopeSchema("text")); err != nil {
		t.Fatal(err)
	}

	url = schemaURL(url, schema)
	dir := writeModule(t, qualifiedScopeModels)
	describeRelationKeys(t, dir, url)
	generateQualifiedScopes(t, dir, "-driver", "database/sql")
	moduleCommandEnv(t, dir, []string{"KANNA_QUALIFIED_DRIVER=pgx", "KANNA_QUALIFIED_DSN=" + url},
		"test", "-mod=mod", "./...")
}

// TestCommand_WithQualifiedScopesMySQL loads relations by scopes naming their table, on MySQL.
func TestCommand_WithQualifiedScopesMySQL(t *testing.T) {
	t.Parallel()
	dsn := os.Getenv("KANNA_ORM_TEST_MYSQL")
	if dsn == "" {
		t.Skip("integration database is not configured")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	database := fmt.Sprintf("kanna_qualified_%d", time.Now().UnixNano())
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.WithoutCancel(t.Context()), "DROP DATABASE IF EXISTS "+database)
		_ = db.Close()
	})
	if _, err := db.ExecContext(t.Context(), "CREATE DATABASE "+database+"; USE "+database+";"+
		qualifiedScopeSchema("varchar(10)")); err != nil {
		t.Fatal(err)
	}

	cfg.DBName, cfg.MultiStatements = database, false
	dir := writeModule(t, qualifiedScopeModels)
	describeRelationKeys(t, dir, fmt.Sprintf("mysql://%s:%s@%s/%s", cfg.User, cfg.Passwd, cfg.Addr, database))
	generateQualifiedScopes(t, dir)
	moduleCommandEnv(t, dir, []string{"KANNA_QUALIFIED_DRIVER=mysql", "KANNA_QUALIFIED_DSN=" + cfg.FormatDSN()},
		"test", "-mod=mod", "./...")
}

// generateQualifiedScopes generates the scope types and the repositories of qualifiedScopeModels in the module at dir,
// the repositories with args.
func generateQualifiedScopes(t *testing.T, dir string, args ...string) {
	t.Helper()
	if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
		t.Fatalf("kanna-orm on the scopes = %d\n%s", code, errOut)
	}
	code, _, errOut := run(t, filepath.Join(dir, "store"), append([]string{"-destination", "."}, args...)...)
	if code != cli.ExitOK {
		t.Fatalf("kanna-orm on the repositories = %d\n%s", code, errOut)
	}
}

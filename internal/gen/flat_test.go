package gen_test

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
	"github.com/go-kanna/orm/internal/sqlfile"
)

// flatShop holds its models, their scope types, its repository, and the repository's implementation in one package,
// with a scope and a query written by hand in one .sql file.
const flatShop = `
-- store/store.go --
package store

import "context"

//kanna:scopes model-package=.

type Customer struct {
	ID   int64
	Name string
}

type Order struct {
	ID         int64
	CustomerID int64
	Status     string
	Customer   *Customer
}

//kanna:repository
type OrderRepository interface {
	Get(ctx context.Context, id int64, s ...OrderScope) (Order, error)
	List(ctx context.Context, s ...OrderScope) ([]Order, error)
	Insert(ctx context.Context, o Order) (Order, error)
	UpdateStatus(ctx context.Context, id int64, c OrderChangeSet) error
	ListStatuses(ctx context.Context) ([]string, error)
}
-- store/store.sql --
-- scope: OrderScope.Open()
status = 'open';

-- query: ListStatuses
SELECT status FROM orders ORDER BY status;
-- store/store_test.go --
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestStore(t *testing.T) {
	dsn := os.Getenv("KANNA_ORM_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("integration database is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	namespace := fmt.Sprintf("kanna_flat_%d", time.Now().UnixNano())
	for _, stmt := range []string{
		"CREATE SCHEMA " + namespace, "SET search_path TO " + namespace,
		"CREATE TABLE customers (id bigint PRIMARY KEY, name text NOT NULL)",
		"CREATE TABLE orders (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, " +
			"customer_id bigint NOT NULL REFERENCES customers, status text NOT NULL)",
		"INSERT INTO customers VALUES (1, 'Ann')",
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+namespace+" CASCADE") })

	orders := NewOrderRepository(conn)
	placed, err := orders.Insert(ctx, Order{CustomerID: 1, Status: "open"})
	if err != nil || placed.ID == 0 {
		t.Fatalf("Insert = %+v, %v", placed, err)
	}
	got, err := orders.Get(ctx, placed.ID, Orders().WithCustomer())
	if err != nil || got.Customer == nil || got.Customer.Name != "Ann" {
		t.Fatalf("Get with the customer = %+v, %v", got, err)
	}
	open, err := orders.List(ctx, Orders().Open())
	if err != nil || len(open) != 1 {
		t.Fatalf("List of the open = %+v, %v", open, err)
	}
	if err := orders.UpdateStatus(ctx, placed.ID, OrderChanges().SetStatus("closed")); err != nil {
		t.Fatal(err)
	}
	statuses, err := orders.ListStatuses(ctx)
	if err != nil || !slices.Equal(statuses, []string{"closed"}) {
		t.Fatalf("ListStatuses = %v, %v", statuses, err)
	}
}
`

// describeFlatShop writes the snapshot of the tables and the statements flatShop reads into the module at dir.
func describeFlatShop(t *testing.T, dir string) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, "store", "store.sql")) //nolint:gosec // the test's module
	if err != nil {
		t.Fatal(err)
	}
	stmts, ds := sqlfile.Parse("store.sql", data)
	if len(ds) > 0 || len(stmts) != 2 {
		t.Fatalf("parse store.sql: %v", ds)
	}
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"customers": {Columns: []snapshot.Column{
			{Name: "id", Type: "int8", NotNull: true}, {Name: "name", Type: "text", NotNull: true},
		}, PrimaryKey: []string{"id"}},
		"orders": {Columns: []snapshot.Column{
			{Name: "id", Type: "int8", NotNull: true, Assigned: true},
			{Name: "customer_id", Type: "int8", NotNull: true}, {Name: "status", Type: "text", NotNull: true},
		}, PrimaryKey: []string{"id"}, ForeignKeys: []snapshot.ForeignKey{fk([]string{"customer_id"}, "customers", "id")}},
	}, Statements: map[string]snapshot.Statement{
		"example.com/shop/store.OrderScope.Open": {Digest: snapshot.Digest(stmts[0].Text)},
		"example.com/shop/store.ListStatuses": {Digest: snapshot.Digest(stmts[1].Text),
			Columns: []snapshot.OutputColumn{{Name: "status", Type: "text", NotNull: true}}},
	}}
	if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCommand_Flat(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, flatShop)
	describeFlatShop(t, dir)
	store := filepath.Join(dir, "store")
	args := []string{"-destination", ".", "-driver", "database/sql"}

	if code, _, errOut := run(t, store, args...); code != cli.ExitOK {
		t.Fatalf("the first run = %d\n%s", code, errOut)
	}
	outputs := map[string][]byte{}
	for _, name := range []string{"scopes_gen.go", "repository_gen.go"} {
		data, err := os.ReadFile(filepath.Join(store, name)) //nolint:gosec // the test's module
		if err != nil {
			t.Fatalf("the first run wrote no %s: %v", name, err)
		}
		outputs[name] = data
	}
	if code, _, errOut := run(t, store, append([]string{"-check"}, args...)...); code != cli.ExitOK {
		t.Fatalf("-check after the first run = %d\n%s", code, errOut)
	}
	if code, _, errOut := run(t, store, args...); code != cli.ExitOK {
		t.Fatalf("the second run = %d\n%s", code, errOut)
	}
	for name, first := range outputs {
		again, err := os.ReadFile(filepath.Join(store, name)) //nolint:gosec // the test's module
		if err != nil || string(again) != string(first) {
			t.Errorf("the second run changed %s: %v", name, err)
		}
	}
	code, out, errOut := run(t, store, append([]string{"-deps"}, args...)...)
	if code != cli.ExitOK || !strings.Contains(out, "scopes_gen.go") || !strings.Contains(out, "repository_gen.go") {
		t.Errorf("-deps = %d, want both outputs among its writes\n%s%s", code, out, errOut)
	}
	moduleCommand(t, dir, "test", "-mod=mod", "./...")
}

// flatShopWithout is flatShop without its test, which names the repository and its constructor, and with each of
// replacements made.
func flatShopWithout(replacements ...string) string {
	archive, _, _ := strings.Cut(flatShop, "-- store/store_test.go --")
	return strings.NewReplacer(replacements...).Replace(archive)
}

func TestCommand_FlatNames(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, archive string
		tables        map[string]snapshot.Table
		want          string
	}{
		{name: "a repository named as an import", archive: flatShopWithout("type OrderRepository interface",
			"type Scope interface")},
		{name: "a table named as a generated type", archive: flatShopWithout(), tables: map[string]snapshot.Table{
			"order_scopes": {Columns: []snapshot.Column{{Name: "id", Type: "int8", NotNull: true}},
				PrimaryKey: []string{"id"}},
		}},
		{name: "a constructor written by hand", archive: flatShopWithout() + "-- store/new.go --\npackage store\n\n" +
			"func NewOrderRepository(db DBTX) OrderRepository { return newOrderRepository(db) }\n"},
		{name: "a type of the constructor's name", archive: flatShopWithout() + "-- store/new.go --\npackage store\n\n" +
			"type NewOrderRepository struct{}\n",
			want: "NewOrderRepository is declared already, and kanna-orm writes it for OrderRepository"},
		{name: "a function of the constructor's name returning something else", archive: flatShopWithout() +
			"-- store/new.go --\npackage store\n\nfunc NewOrderRepository(s string) int { return len(s) }\n",
			want: "NewOrderRepository is declared already, and kanna-orm writes it for OrderRepository"},
		{name: "a constructor written by hand returning a pointer and an error", archive: flatShopWithout() +
			"-- store/new.go --\npackage store\n\n" +
			"func NewOrderRepository(db DBTX) (*OrderRepository, error) {\n\tvar r OrderRepository = newOrderRepository(db)\n" +
			"\treturn &r, nil\n}\n"},
		{name: "a constructor written in a test", archive: flatShopWithout() + "-- store/new_test.go --\npackage store\n\n" +
			"func NewOrderRepository(db DBTX) OrderRepository { return newOrderRepository(db) }\n",
			want: "NewOrderRepository is declared already, and kanna-orm writes it for OrderRepository"},
		{name: "DBTX written by hand", archive: flatShopWithout() + "-- store/db.go --\npackage store\n\n" +
			"type DBTX interface{}\n", want: "DBTX is declared already"},
		{name: "the implementation's constructor written by hand", archive: flatShopWithout() +
			"-- store/new.go --\npackage store\n\nfunc newOrderRepository() {}\n",
			want: "newOrderRepository is declared already, and kanna-orm writes it for OrderRepository"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeModule(t, tc.archive)
			describeFlatShop(t, dir)
			if tc.tables != nil {
				path := filepath.Join(dir, snapshot.Name)
				data, err := os.ReadFile(path) //nolint:gosec // the test's module
				if err != nil {
					t.Fatal(err)
				}
				s, err := snapshot.Unmarshal(data)
				if err != nil {
					t.Fatal(err)
				}
				maps.Copy(s.Tables, tc.tables)
				if err := os.WriteFile(path, snapshot.Marshal(s), 0o600); err != nil { //nolint:gosec // the test's module
					t.Fatal(err)
				}
			}
			store := filepath.Join(dir, "store")
			args := []string{"-destination", ".", "-driver", "database/sql"}
			code, _, errOut := run(t, store, args...)
			if tc.want != "" {
				if code == cli.ExitOK || !strings.Contains(errOut, tc.want) {
					t.Fatalf("generate = %d, want %q\n%s", code, tc.want, errOut)
				}
				return
			}
			if code != cli.ExitOK {
				t.Fatalf("the first run = %d\n%s", code, errOut)
			}
			if code, _, errOut := run(t, store, append([]string{"-check"}, args...)...); code != cli.ExitOK {
				t.Fatalf("-check after the first run = %d\n%s", code, errOut)
			}
			scopes, err := os.ReadFile(filepath.Join(store, "scopes_gen.go")) //nolint:gosec // the test's module
			if err != nil || strings.Contains(string(scopes), "OrderScopeScope") {
				t.Errorf("a generated type is read as a model: %v\n%s", err, scopes)
			}
			moduleCommand(t, dir, "vet", "-mod=mod", "./...")
		})
	}
}

func TestCommand_ChangeSetNames(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, scopes, want string }{
		{name: "a scope type ending in Scope", scopes: "//kanna:scope table=orders\n" +
			"type OrderScope struct{ q scope.Of[model.Order] }\n", want: "type OrderChangeSet struct"},
		{name: "two scope types of one change set", scopes: "//kanna:scope table=orders\n" +
			"type Order struct{ q scope.Of[model.Order] }\n\n//kanna:scope table=archived_orders\n" +
			"type OrderScope struct{ q scope.Of[model.Order] }\n",
			want: "OrderScope: its change set would be OrderChangeSet, as that of Order is"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeModule(t, `
-- model/model.go --
package model

type Order struct {
	ID     int64
	Status string
}
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

`+tc.scopes)
			table := snapshot.Table{Columns: []snapshot.Column{
				{Name: "id", Type: "int8", NotNull: true}, {Name: "status", Type: "text", NotNull: true},
			}, PrimaryKey: []string{"id"}}
			s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
				"orders": table, "archived_orders": table,
			}}
			if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
				t.Fatal(err)
			}
			code, _, errOut := run(t, filepath.Join(dir, "scopes"))
			if strings.HasPrefix(tc.want, "type ") {
				generated, err := os.ReadFile(filepath.Join(dir, "scopes", "scopes_gen.go")) //nolint:gosec // the test's
				if code != cli.ExitOK || err != nil || !strings.Contains(string(generated), tc.want) ||
					!strings.Contains(string(generated), "func OrderChanges() OrderChangeSet") {
					t.Fatalf("generate = %d, %v, want %q\n%s%s", code, err, tc.want, errOut, generated)
				}
				return
			}
			if code == cli.ExitOK || !strings.Contains(errOut, tc.want) {
				t.Fatalf("generate = %d, want %q\n%s", code, tc.want, errOut)
			}
		})
	}
}

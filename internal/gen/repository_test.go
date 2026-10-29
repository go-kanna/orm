package gen_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/internal/packages"

	"github.com/go-kanna/orm/internal/gen"
	"github.com/go-kanna/orm/internal/snapshot"
)

// repositoryModule declares the models, the scope types, and a repository package of the tests, whose interface the
// test declares.
const repositoryModule = `
-- model/model.go --
package model

import "time"

type (
	OrderID     int64
	OrderStatus string
)

type Order struct {
	ID         OrderID
	CustomerID int64
	Number     string
	Status     OrderStatus
	PlacedAt   time.Time
}

type OrderLine struct {
	OrderID OrderID
	LineNo  int
}

type User struct {
	ID        int64
	TenantID  int64
	Email     string
	Login     string
	Name      string
	CreatedAt time.Time
}

type Log struct{ Message string }
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm"
)

//kanna:scope table=orders
type Order struct{ q orm.Of[model.Order] }

//kanna:scope table=order_lines
type OrderLine struct{ q orm.Of[model.OrderLine] }

//kanna:scope table=users
type User struct{ q orm.Of[model.User] }

//kanna:scope table=logs
type Log struct{ q orm.Of[model.Log] }
`

// repositoryTables are the tables of repositoryModule.
func repositoryTables(d snapshot.Dialect) snapshot.Snapshot {
	assigned := snapshot.Column{Name: "id", Type: "int8", NotNull: true, Assigned: true}
	return snapshot.Snapshot{Dialect: d, Tables: map[string]snapshot.Table{
		"orders": {
			Columns:    append([]snapshot.Column{assigned}, columns("customer_id", "number", "status", "placed_at")...),
			PrimaryKey: []string{"id"}, UniqueKeys: [][]string{{"number"}},
		},
		"order_lines": {
			Columns: columns("order_id", "line_no"), PrimaryKey: []string{"order_id", "line_no"},
			ForeignKeys: []snapshot.ForeignKey{fk([]string{"order_id"}, "orders", "id")},
		},
		"users": {
			Columns:    append([]snapshot.Column{assigned}, columns("tenant_id", "email", "login", "name", "created_at")...),
			PrimaryKey: []string{"id"}, UniqueKeys: [][]string{{"tenant_id", "email"}, {"tenant_id", "login"}},
		},
		"logs": {Columns: columns("message")},
	}}
}

// repository loads the module of repositoryModule with the interface of the repository package declaring methods,
// and reads its repositories.
func repository(t *testing.T, d snapshot.Dialect, methods string, queries ...string) ([]string, string) {
	t.Helper()

	dir := writeModule(t, repositoryModule+`
-- repository/repository.go --
package repository

import (
	"context"

	"example.com/shop/model"
	"example.com/shop/scopes"
)

var (
	_ context.Context
	_ model.Order
	_ scopes.Order
)

//kanna:repository
type OrderRepository interface {
`+methods+`}
`)
	set, err := packages.Load([]string{"./repository"}, packages.Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Diags) > 0 || len(set.Packages) != 1 {
		t.Fatalf("load: %d packages, %v", len(set.Packages), set.Diags)
	}
	got, ds, err := gen.Repositories(set.Packages[0], repositoryTables(d), queries...)
	if err != nil {
		t.Fatal(err)
	}
	return got, report(ds)
}

func TestRepositories(t *testing.T) {
	t.Parallel()

	got, ds := repository(t, snapshot.PostgreSQL, `
	Get(ctx context.Context, id model.OrderID, s ...scopes.Order) (model.Order, error)
	GetByNumber(ctx context.Context, number string) (model.Order, error)
	GetLine(ctx context.Context, orderID model.OrderID, lineNo int) (model.OrderLine, error)
	First(ctx context.Context, s ...scopes.Order) (model.Order, error)
	List(ctx context.Context, s ...scopes.Order) ([]model.Order, error)
	ListByCustomer(ctx context.Context, customerID int64, s ...scopes.Order) ([]model.Order, error)
	Count(ctx context.Context, s ...scopes.Order) (int64, error)
	Exists(ctx context.Context, s ...scopes.Order) (bool, error)
	Insert(ctx context.Context, o model.Order) (model.Order, error)
	InsertLines(ctx context.Context, lines []model.OrderLine) error
	Update(ctx context.Context, o model.Order) error
	Upsert(ctx context.Context, o model.Order) error
	// UpsertUser meets the user of a tenant by their email address.
	//kanna:conflict tenant_id,email keep=created_at
	UpsertUser(ctx context.Context, u model.User) (model.User, error)
	Delete(ctx context.Context, id model.OrderID) error
	DeleteStale(ctx context.Context, s ...scopes.Order) (int64, error)
	MonthCounts(ctx context.Context) error
`, "MonthCounts")
	if ds != "" {
		t.Fatalf("Repositories:\n%s", ds)
	}
	want := []string{
		"OrderRepository.Get Get orders keys=id=id scoped row",
		"OrderRepository.GetByNumber Get orders keys=number=number row",
		"OrderRepository.GetLine Get order_lines keys=orderID=order_id,lineNo=line_no row",
		"OrderRepository.First First orders scoped row",
		"OrderRepository.List List orders scoped rows",
		"OrderRepository.ListByCustomer List orders keys=customerID=customer_id scoped rows",
		"OrderRepository.Count Count orders scoped count",
		"OrderRepository.Exists Exists orders scoped bool",
		"OrderRepository.Insert Insert orders row=o row",
		"OrderRepository.InsertLines Insert order_lines row=lines many error",
		"OrderRepository.Update Update orders row=o error",
		"OrderRepository.Upsert Upsert orders row=o error",
		"OrderRepository.UpsertUser Upsert users row=u row conflict=tenant_id,email keep=created_at",
		"OrderRepository.Delete Delete orders keys=id=id error",
		"OrderRepository.DeleteStale Delete orders scoped count",
		"OrderRepository.MonthCounts query",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Repositories =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRepositories_Mistakes(t *testing.T) {
	t.Parallel()

	const at = "repository.go:18:2: "
	tests := []struct {
		name    string
		dialect snapshot.Dialect
		methods string
		want    string
	}{
		{"no verb", snapshot.PostgreSQL, "\tFetch(ctx context.Context) (model.Order, error)\n",
			at + "error: Fetch: its name starts with no verb kanna-orm knows, and no -- query: Fetch is written for it"},
		{"a verb run into another word", snapshot.PostgreSQL, "\tGetaway(ctx context.Context) (model.Order, error)\n",
			at + "error: Getaway: its name starts with no verb kanna-orm knows, and no -- query: Getaway is written " +
				"for it"},
		{"no context", snapshot.PostgreSQL, "\tGet(id model.OrderID) (model.Order, error)\n",
			at + "error: Get: its first parameter is no context.Context"},
		{"a result the verb does not take", snapshot.PostgreSQL,
			"\tList(ctx context.Context, s ...scopes.Order) (model.Order, error)\n",
			at + "error: List: kanna-orm cannot implement it as declared\n" +
				"  declare it as List(ctx context.Context, s ...scopes.Order) ([]model.Order, error)"},
		{"a variadic parameter of no scope type", snapshot.PostgreSQL,
			"\tList(ctx context.Context, ids ...int64) ([]model.Order, error)\n",
			at + "error: List: its last parameter, ids, takes no scope type"},
		{"a column misspelled", snapshot.PostgreSQL,
			"\tListByCustomer(ctx context.Context, custmerID int64, s ...scopes.Order) ([]model.Order, error)\n",
			at + "error: ListByCustomer: orders has no column for the parameter custmerID\n" +
				"  did you mean customerID (customer_id)?"},
		{"a parameter of another type than its field", snapshot.PostgreSQL,
			"\tListByCustomer(ctx context.Context, customerID string) ([]model.Order, error)\n",
			at + "error: ListByCustomer: customerID is a string, but Order.CustomerID, which holds customer_id, is an " +
				"int64\n  declare it as int64"},
		{"two models", snapshot.PostgreSQL,
			"\tList(ctx context.Context, s ...scopes.OrderLine) ([]model.Order, error)\n",
			at + "error: List: it reads or writes both model.OrderLine and model.Order"},
		{"a Get of no unique key", snapshot.PostgreSQL,
			"\tGetByStatus(ctx context.Context, status model.OrderStatus) (model.Order, error)\n",
			at + "error: GetByStatus: status is not a unique key of orders, so Get cannot return one row\n" +
				"  use List, or add a unique index on orders (status)"},
		{"a Delete of no key", snapshot.PostgreSQL,
			"\tDeleteByStatus(ctx context.Context, status model.OrderStatus) error\n",
			at + "error: DeleteByStatus: (status) is no key of orders, so Delete would delete more than the one row\n" +
				"  return (int64, error) for the number of rows deleted, or take a key of the table"},
		{"nothing telling the table", snapshot.PostgreSQL, "\tDeleteByID(ctx context.Context, id int64) error\n",
			at + "error: DeleteByID: nothing tells the table it reads or writes\n" +
				"  give it the scopes of the table, as in Delete(ctx context.Context, id model.OrderID) error, or " +
				"DeleteStale(ctx context.Context, s ...scopes.Order) (int64, error)"},
		{"an Update of a table without a primary key", snapshot.PostgreSQL,
			"\tUpdateLog(ctx context.Context, l model.Log) error\n",
			at + "error: UpdateLog: logs has no primary key to tell its rows by"},
		{"a conflict on no unique key", snapshot.PostgreSQL,
			"\t//kanna:conflict email\n\tUpsertUser(ctx context.Context, u model.User) error\n",
			"repository.go:18:2: error: UpsertUser: (email) is neither the primary key of users nor one of its unique " +
				"keys"},
		{"a conflict on what is no upsert", snapshot.PostgreSQL,
			"\t//kanna:conflict id\n\tInsertUser(ctx context.Context, u model.User) error\n",
			"repository.go:18:2: error: InsertUser: //kanna:conflict is for an Upsert"},
		{"a conflict MySQL meets on another key too", snapshot.MySQL,
			"\t//kanna:conflict tenant_id,email\n\tUpsertUser(ctx context.Context, u model.User) error\n",
			"repository.go:19:2: warning: UpsertUser: on MySQL, a conflict on (tenant_id, login) also updates the row\n" +
				"  users has unique keys other than (tenant_id, email)"},
		{"rows read back on MySQL from AUTO_INCREMENT", snapshot.MySQL,
			"\tInsertUsers(ctx context.Context, us []model.User) ([]model.User, error)\n",
			at + "error: InsertUsers: MySQL does not promise consecutive AUTO_INCREMENT values to the rows of one " +
				"statement, so the rows cannot be read back\n  return error alone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, ds := repository(t, tt.dialect, tt.methods); ds != tt.want {
				t.Errorf("Repositories reports\n%s\nwant\n%s", ds, tt.want)
			}
		})
	}
}

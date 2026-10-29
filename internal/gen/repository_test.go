package gen_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/internal/packages"

	"github.com/go-kanna/orm/internal/gen"
	"github.com/go-kanna/orm/internal/snapshot"
	"github.com/go-kanna/orm/internal/sqlfile"
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

type Note struct {
	Body string
	Tags []string
}

type Ticket struct{ ID int64 }

type Session struct{ UserID int64 }
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

//kanna:scope table=notes
type Note struct{ q orm.Of[model.Note] }

//kanna:scope table=tickets
type Ticket struct{ q orm.Of[model.Ticket] }

//kanna:scope table=sessions
type Session struct{ q orm.Of[model.Session] }
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
		"logs": {Columns: columns("message", "level")},
		"notes": {
			Columns:    []snapshot.Column{assigned, {Name: "body", Type: "text", NotNull: true}, {Name: "tags", Type: "text[]"}},
			PrimaryKey: []string{"id"},
		},
		"tickets": {Columns: []snapshot.Column{assigned}, PrimaryKey: []string{"id"}},
		"sessions": {
			Columns: []snapshot.Column{
				{Name: "token", Type: "varchar(64)", NotNull: true, Default: true},
				{Name: "user_id", Type: "bigint", NotNull: true},
			},
			PrimaryKey: []string{"token"},
		},
	}}
}

// repositories loads the module of repositoryModule, with the interface of the repository package declaring methods,
// and queries.sql there holding sql, whose queries describe records as described has them but for their digests, and
// reads its repositories.
func repositories(t *testing.T, d snapshot.Dialect, methods, sql string, described map[string]snapshot.Statement) (
	[]string, string,
) {
	t.Helper()

	dir := writeModule(t, repositoryModule+`
-- repository/repository.go --
package repository

import (
	"context"
	"time"

	"example.com/shop/model"
	"example.com/shop/scopes"
)

var (
	_ context.Context
	_ model.Order
	_ scopes.Order
)

type MonthCount struct {
	Month time.Time
	Count int64
}

//kanna:repository
type OrderRepository interface {
`+methods+`}
-- repository/queries.sql --
`+sql)
	set, err := packages.Load([]string{"./repository"}, packages.Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Diags) > 0 || len(set.Packages) != 1 {
		t.Fatalf("load: %d packages, %v", len(set.Packages), set.Diags)
	}
	s := repositoryTables(d)
	s.Statements = map[string]snapshot.Statement{}
	stmts, _ := sqlfile.Parse("queries.sql", []byte(sql))
	for _, st := range stmts {
		if desc, ok := described[st.Name]; ok {
			desc.Digest = snapshot.Digest(st.Text)
			s.Statements["example.com/shop/repository."+st.Name] = desc
		}
	}
	got, ds, err := gen.Repositories(set.Packages[0], s)
	if err != nil {
		t.Fatal(err)
	}
	return got, report(ds)
}

// monthCounts is a query written by hand, and what describe records of it.
const monthCounts = `-- query: MonthCounts
SELECT date_trunc('month', placed_at) AS month, count(*) AS count
FROM orders
WHERE placed_at >= :since
GROUP BY 1
ORDER BY 1;
`

var monthCountsDescribed = snapshot.Statement{
	Params:  []snapshot.Param{{Name: "since", Type: "timestamptz"}},
	Columns: []snapshot.Result{{Name: "month", Type: "timestamptz"}, {Name: "count", Type: "int8", NotNull: true}},
}

func TestRepositories(t *testing.T) {
	t.Parallel()

	got, ds := repositories(t, snapshot.PostgreSQL, `
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
	UpsertLine(ctx context.Context, line model.OrderLine) error
	// UpsertUser meets the user of a tenant by their email address.
	//kanna:conflict tenant_id,email keep=created_at
	UpsertUser(ctx context.Context, u model.User) (model.User, error)
	Delete(ctx context.Context, id model.OrderID) error
	DeleteStale(ctx context.Context, s ...scopes.Order) (int64, error)
	MonthCounts(ctx context.Context, since time.Time) ([]MonthCount, error)
	CloseOld(ctx context.Context, before time.Time) (int64, error)
`, monthCounts+`
-- query: CloseOld
UPDATE orders SET status = 'closed' WHERE placed_at < :before
`, map[string]snapshot.Statement{
		"MonthCounts": monthCountsDescribed,
		"CloseOld":    {Params: []snapshot.Param{{Name: "before", Type: "timestamptz"}}},
	})
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
		"OrderRepository.UpsertLine Upsert order_lines row=line error",
		"OrderRepository.UpsertUser Upsert users row=u row conflict=tenant_id,email keep=created_at",
		"OrderRepository.Delete Delete orders keys=id=id error",
		"OrderRepository.DeleteStale Delete orders scoped count",
		"OrderRepository.MonthCounts query \"SELECT date_trunc('month', placed_at) AS month, count(*) AS count\\nFROM " +
			"orders\\nWHERE placed_at >= $1\\nGROUP BY 1\\nORDER BY 1\" args=since rows repository.MonthCount{Month, Count}",
		"OrderRepository.CloseOld query \"UPDATE orders SET status = 'closed' WHERE placed_at < $1\" args=before count",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Repositories =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRepositories_Mistakes(t *testing.T) {
	t.Parallel()

	const at = "repository.go:24:2: "
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
		{"an Upsert of a table without a key", snapshot.PostgreSQL,
			"\tUpsertLog(ctx context.Context, l model.Log) error\n",
			at + "error: UpsertLog: logs has no primary key to tell its rows by\n" +
				"  name a unique key of logs with //kanna:conflict"},
		{"an Upsert by a key the database assigns", snapshot.PostgreSQL,
			"\tUpsert(ctx context.Context, o model.Order) error\n",
			at + "error: Upsert: the database assigns id, so an insert never meets a row by it\n" +
				"  name another unique key of orders with //kanna:conflict"},
		{"an Update of a model without its key", snapshot.PostgreSQL,
			"\tUpdateNote(ctx context.Context, n model.Note) error\n",
			at + "error: UpdateNote: model.Note has no field for id, which it meets its row by"},
		{"an Update of nothing but the key", snapshot.PostgreSQL,
			"\tUpdateLine(ctx context.Context, line model.OrderLine) error\n",
			at + "error: UpdateLine: model.OrderLine holds no column of order_lines to update but its key"},
		{"a column the table requires and the model has no field for", snapshot.PostgreSQL,
			"\tInsertLog(ctx context.Context, l model.Log) error\n",
			at + "error: InsertLog: logs requires a value for level, which model.Log has no field for\n" +
				"  give model.Log a field for it, or the column a default"},
		{"an Insert of no column but those the database assigns", snapshot.PostgreSQL,
			"\tInsertTicket(ctx context.Context, t model.Ticket) (model.Ticket, error)\n",
			at + "error: InsertTicket: model.Ticket holds no column of tickets but those the database assigns"},
		{"many rows of an array on PostgreSQL", snapshot.PostgreSQL,
			"\tInsertNotes(ctx context.Context, ns []model.Note) error\n",
			at + "error: InsertNotes: unnest, which writes the rows in one statement, cannot write tags, an array\n" +
				"  write the rows one by one"},
		{"a conflict on no unique key", snapshot.PostgreSQL,
			"\t//kanna:conflict email\n\tUpsertUser(ctx context.Context, u model.User) error\n",
			"repository.go:24:2: error: UpsertUser: (email) is neither the primary key of users nor one of its unique " +
				"keys"},
		{"a conflict on what is no upsert", snapshot.PostgreSQL,
			"\t//kanna:conflict id\n\tInsertUser(ctx context.Context, u model.User) error\n",
			"repository.go:24:2: error: InsertUser: //kanna:conflict is for an Upsert"},
		{"a conflict MySQL meets on another key too", snapshot.MySQL,
			"\t//kanna:conflict tenant_id,email\n\tUpsertUser(ctx context.Context, u model.User) error\n",
			"repository.go:25:2: warning: UpsertUser: on MySQL, a conflict on (tenant_id, login) also updates the row\n" +
				"  users has unique keys other than (tenant_id, email)"},
		{"rows read back on MySQL", snapshot.MySQL,
			"\tUpsertLines(ctx context.Context, lines []model.OrderLine) ([]model.OrderLine, error)\n",
			at + "error: UpsertLines: MySQL has no RETURNING, so the rows of one statement cannot be read back\n" +
				"  return error alone"},
		{"a row read back on MySQL from a table without a primary key", snapshot.MySQL,
			"\tInsertLog(ctx context.Context, l model.Log) (model.Log, error)\n",
			at + "error: InsertLog: logs requires a value for level, which model.Log has no field for\n" +
				"  give model.Log a field for it, or the column a default\n" +
				at + "error: InsertLog: MySQL reads the row written back by its primary key, which logs has none\n" +
				"  return error alone"},
		{"a row read back on MySQL by a key the model has no field for", snapshot.MySQL,
			"\tInsertSession(ctx context.Context, s model.Session) (model.Session, error)\n",
			at + "error: InsertSession: MySQL reads the row written back by its primary key, but model.Session has " +
				"no field for token\n  return error alone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, ds := repositories(t, tt.dialect, tt.methods, "", nil); ds != tt.want {
				t.Errorf("Repositories reports\n%s\nwant\n%s", ds, tt.want)
			}
		})
	}
}

func TestRepositories_QueryMistakes(t *testing.T) {
	t.Parallel()

	const (
		at     = "repository.go:24:2: "
		header = "queries.sql:1:1: "
	)
	described := map[string]snapshot.Statement{"MonthCounts": monthCountsDescribed}
	tests := []struct {
		name, methods, sql string
		described          map[string]snapshot.Statement
		want               string
	}{
		{"a query not described",
			"\tMonthCounts(ctx context.Context, since time.Time) ([]MonthCount, error)\n", monthCounts, nil,
			header + "error: -- query: MonthCounts: the snapshot has not described it\n" +
				"  run go tool kanna-orm describe to describe it"},
		{"a parameter not declared", "\tMonthCounts(ctx context.Context) ([]MonthCount, error)\n", monthCounts,
			described, header + "error: -- query: MonthCounts: :since is none of the parameters of the method\n" +
				"  declare it: MonthCounts(ctx context.Context, since ...)"},
		{"a parameter not used",
			"\tMonthCounts(ctx context.Context, since time.Time, n int) ([]MonthCount, error)\n", monthCounts,
			described, at + "warning: MonthCounts: the query does not use n"},
		{"columns returned to error alone", "\tMonthCounts(ctx context.Context, since time.Time) error\n",
			monthCounts, described, at + "error: MonthCounts: the query returns columns, but the method returns " +
				"error alone\n  return ([]T, error) or (T, error), T having a field for each column"},
		{"a result of no struct", "\tMonthCounts(ctx context.Context, since time.Time) ([]int, error)\n",
			monthCounts, described, at + "error: MonthCounts: a query written by hand returns error alone, (int64, " +
				"error), (T, error), or ([]T, error)\n  T being a struct with a field for each column of its result"},
		{"a column of no field", "\tMonthCounts(ctx context.Context, since time.Time) ([]struct{ Month time.Time }, " +
			"error)\n", monthCounts, described,
			header + "error: -- query: MonthCounts: struct{Month time.Time} has no field for the column count\n" +
				"  give struct{Month time.Time} a field for it, or name the column after one with AS"},
		{"a field of no column", "\tMonthCounts(ctx context.Context, since time.Time) ([]struct{ Month time.Time; " +
			"Count int64; Total int64 }, error)\n", monthCounts, described,
			header + "error: -- query: MonthCounts: the query returns no column total for struct{Month time.Time; " +
				"Count int64; Total int64}.Total\n  select it, or leave the field out with orm:\"-\""},
		{"a column that may be NULL in a field that cannot be nil",
			"\tMonthCounts(ctx context.Context, since time.Time) ([]MonthCount, error)\n", monthCounts,
			map[string]snapshot.Statement{"MonthCounts": {
				Params:  monthCountsDescribed.Params,
				Columns: []snapshot.Result{{Name: "month", Type: "timestamptz", Nullable: true}, {Name: "count", Type: "int8"}},
			}},
			header + "error: -- query: MonthCounts: the column month may be NULL, which repository.MonthCount.Month, " +
				"a time.Time, cannot hold\n  declare it as *time.Time"},
		{"a query of no method", "\tList(ctx context.Context, s ...scopes.Order) ([]model.Order, error)\n",
			"-- query: Lists\nSELECT 1\n", nil,
			header + "error: -- query: Lists is no method of a //kanna:repository interface of this package\n" +
				"  did you mean List?"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, ds := repositories(t, snapshot.PostgreSQL, tt.methods, tt.sql, tt.described); ds != tt.want {
				t.Errorf("Repositories reports\n%s\nwant\n%s", ds, tt.want)
			}
		})
	}
}

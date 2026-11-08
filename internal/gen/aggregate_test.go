package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

// aggregateShop declares sales, with columns of numbers, of strings, and of times, some of which may be NULL; a
// repository reading aggregates into values, one reading them into pointers, one by a key and no scopes; and
// projections of aggregates, grouped by the other fields.
const aggregateShop = `
-- model/model.go --
package model

import "time"

type Status string

type Cents int64

type Sale struct {
	ID       int64
	Region   string
	Status   Status
	Units    int32
	Amount   Cents
	Discount *int64
	Rate     float64
	Price    float64
	Note     *string
	SoldAt   time.Time
}
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=sales
type Sale struct{ q scope.Of[model.Sale] }
-- repository/repository.go --
package repository

import (
	"context"
	"time"

	"example.com/shop/model"
	"example.com/shop/scopes"
)

//kanna:repository
type Sales interface {
	SumUnits(ctx context.Context, s ...scopes.Sale) (int32, error)
	SumAmount(ctx context.Context, s ...scopes.Sale) (model.Cents, error)
	SumDiscount(ctx context.Context, s ...scopes.Sale) (int64, error)
	SumRate(ctx context.Context, s ...scopes.Sale) (float64, error)
	MinSoldAt(ctx context.Context, s ...scopes.Sale) (time.Time, error)
	MaxSoldAt(ctx context.Context, s ...scopes.Sale) (time.Time, error)
	MinNote(ctx context.Context, s ...scopes.Sale) (string, error)
	MaxStatus(ctx context.Context, s ...scopes.Sale) (model.Status, error)
	AvgUnits(ctx context.Context, s ...scopes.Sale) (float64, error)
	AvgAmount(ctx context.Context, s ...scopes.Sale) (float32, error)
	AvgPrice(ctx context.Context, s ...scopes.Sale) (float64, error)
	ListStatusCounts(ctx context.Context, s ...scopes.Sale) ([]StatusCount, error)
	ListRegionTotals(ctx context.Context, s ...scopes.Sale) ([]RegionTotal, error)
	ListNoteCounts(ctx context.Context, s ...scopes.Sale) ([]NoteCount, error)
}

//kanna:repository table=sales
type Optional interface {
	SumAmount(ctx context.Context, s ...scopes.Sale) (*model.Cents, error)
	SumDiscount(ctx context.Context, s ...scopes.Sale) (*int64, error)
	MinSoldAt(ctx context.Context, s ...scopes.Sale) (*time.Time, error)
	MaxDiscount(ctx context.Context, s ...scopes.Sale) (*int64, error)
	AvgRate(ctx context.Context, s ...scopes.Sale) (*float64, error)
}

//kanna:repository table=sales
type Regional interface {
	SumAmount(ctx context.Context, region string) (model.Cents, error)
	MinDiscount(ctx context.Context, region string) (int64, error)
	ListStatusCounts(ctx context.Context, region string, s ...scopes.Sale) ([]StatusCount, error)
}

type StatusCount struct {
	Status model.Status
	Count  int64
}

// RegionTotal is what the sales of a region amount to: of a column that may be NULL, a sum is zero and a maximum nil
// for a region whose sales hold only NULLs there.
type RegionTotal struct {
	Region      string
	Count       int64
	SumAmount   model.Cents
	SumDiscount int64
	MinSoldAt   time.Time
	MaxDiscount *int64
	AvgRate     float64
}

type NoteCount struct {
	Note  *string
	Count int64
}
`

// aggregateTables describes the table of aggregateShop on the database of d.
func aggregateTables(d snapshot.Dialect) map[string]snapshot.Table {
	types := map[string]string{"int": "int4", "bigint": "int8", "float": "float8", "decimal": "numeric(10,2)",
		"text": "text", "time": "timestamptz"}
	if d == snapshot.MySQL {
		types = map[string]string{"int": "int", "bigint": "bigint", "float": "double", "decimal": "decimal(10,2)",
			"text": "varchar(64)", "time": "datetime(6)"}
	}
	return map[string]snapshot.Table{
		"sales": {Columns: []snapshot.Column{
			{Name: "id", Type: types["bigint"], NotNull: true, Assigned: true},
			{Name: "region", Type: types["text"], NotNull: true},
			{Name: "status", Type: types["text"], NotNull: true},
			{Name: "units", Type: types["int"], NotNull: true},
			{Name: "amount", Type: types["bigint"], NotNull: true},
			{Name: "discount", Type: types["bigint"]},
			{Name: "rate", Type: types["float"], NotNull: true},
			{Name: "price", Type: types["decimal"], NotNull: true},
			{Name: "note", Type: types["text"]},
			{Name: "sold_at", Type: types["time"], NotNull: true},
		}, PrimaryKey: []string{"id"}},
	}
}

// generateAggregateShop writes archive, aggregateShop and more, into a module with the snapshot of d, its tables
// changed by change if any, generates its scopes and its repositories into impl through driver, and returns the
// module's directory and what the run of the repositories reported.
func generateAggregateShop(t *testing.T, archive string, d snapshot.Dialect, driver string,
	change ...func(map[string]snapshot.Table),
) (string, int, string) {
	t.Helper()

	dir := writeModule(t, archive)
	tables := aggregateTables(d)
	for _, c := range change {
		c(tables)
	}
	s := snapshot.Snapshot{Dialect: d, Tables: tables}
	if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
		t.Fatalf("generate scopes = %d\n%s", code, errOut)
	}
	code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", "../impl", "-driver", driver)
	return dir, code, errOut
}

// TestCommand_AggregateSQL checks the SQL each aggregate is read by on each database: a Sum into a value coalesced to
// zero, the sum of integers a bigint and an average a double precision number on PostgreSQL, an average a DOUBLE on
// MySQL, and a projection of aggregates grouped by its other fields.
func TestCommand_AggregateSQL(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		d    snapshot.Dialect
		want []string
	}{
		{snapshot.PostgreSQL, []string{
			"`SELECT coalesce(sum(units)::bigint, 0) FROM sales`+rd.Where()",
			"`SELECT coalesce(sum(rate), 0) FROM sales`+rd.Where()",
			"`SELECT min(sold_at) FROM sales`+rd.Where()",
			"`SELECT avg(units)::double precision FROM sales`+rd.Where()",
			"`SELECT sum(amount)::bigint FROM sales`+rd.Where()",
			"const q = `SELECT coalesce(sum(amount)::bigint, 0) FROM sales WHERE region = $1`",
			"const q = `SELECT min(discount) FROM sales WHERE region = $1`",
			"`SELECT status, count(*) FROM sales`+rd.Where()+` GROUP BY status`",
			"`SELECT region, count(*), sum(amount)::bigint, coalesce(sum(discount)::bigint, 0), min(sold_at), " +
				"max(discount), avg(rate)::double precision FROM sales`+rd.Where()+` GROUP BY region`",
		}},
		{snapshot.MySQL, []string{
			"`SELECT coalesce(sum(units), 0) FROM sales`+rd.Where()",
			"`SELECT CAST(avg(units) AS DOUBLE) FROM sales`+rd.Where()",
			"`SELECT sum(amount) FROM sales`+rd.Where()",
			"const q = `SELECT coalesce(sum(amount), 0) FROM sales WHERE region = ?`",
			"`SELECT region, count(*), sum(amount), coalesce(sum(discount), 0), min(sold_at), max(discount), " +
				"CAST(avg(rate) AS DOUBLE) FROM sales`+rd.Where()+` GROUP BY region`",
		}},
	} {
		t.Run(string(tt.d), func(t *testing.T) {
			t.Parallel()
			dir, code, errOut := generateAggregateShop(t, aggregateShop, tt.d, "database/sql")
			if code != cli.ExitOK {
				t.Fatalf("generate = %d\n%s", code, errOut)
			}
			src, err := os.ReadFile(filepath.Join(dir, "impl", "repository_gen.go")) //nolint:gosec // written above
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(string(src), want) {
					t.Errorf("repository_gen.go has no %s\n%s", want, src)
				}
			}
		})
	}
}

// aggregateMistakes adds to aggregateShop fields of sales that kanna-orm reads no aggregate of: a number stored as
// text, and others the test declares; customers, which a projection can join; and projections, to which the test adds
// the method reading one.
const aggregateMistakes = `
-- model/more.go --
package model

import (
	"database/sql/driver"
	"strconv"
)

// Code is a number stored as text.
type Code int

func (c Code) Value() (driver.Value, error) { return strconv.Itoa(int(c)), nil }

func (c *Code) Scan(src any) error {
	n, err := strconv.Atoi(src.(string))
	*c = Code(n)
	return err
}

type Customer struct {
	ID   int64
	Name string
}
-- scopes/customer.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=customers
type Customer struct{ q scope.Of[model.Customer] }
-- repository/projections.go --
package repository

import "example.com/shop/model"

type Totals struct {
	Count     int64
	SumAmount model.Cents
}

type Tagged struct {
	Region    string
	SumAmount model.Cents ` + "`orm:\"amount\"`" + `
}

type NarrowCount struct {
	Region string
	Count  int32
}

type FirstNotes struct {
	Region  string
	MinNote string
}

type NoteSums struct {
	Region  string
	SumNote string
}

type CustomerCount struct {
	Customer struct{ Name string }
	Count    int64
}

type RegionCount struct {
	Region string
	Count  int64
}
`

// aggregateMistakeTables adds to the tables of aggregateShop the columns of the fields a test adds to sales, and
// customers, which sales refers to.
func aggregateMistakeTables(tables map[string]snapshot.Table) {
	sales := tables["sales"]
	sales.Columns = append(sales.Columns, snapshot.Column{Name: "paid", Type: "bool", NotNull: true},
		snapshot.Column{Name: "ref", Type: "uuid", NotNull: true},
		snapshot.Column{Name: "code", Type: "text", NotNull: true},
		snapshot.Column{Name: "customer_id", Type: "int8", NotNull: true},
		snapshot.Column{Name: "count", Type: "int8", NotNull: true})
	sales.ForeignKeys = []snapshot.ForeignKey{fk([]string{"customer_id"}, "customers", "id")}
	tables["sales"] = sales
	tables["customers"] = snapshot.Table{Columns: []snapshot.Column{{Name: "id", Type: "int8", NotNull: true},
		{Name: "name", Type: "text", NotNull: true}}, PrimaryKey: []string{"id"}}
}

func TestCommand_AggregateMistakes(t *testing.T) {
	t.Parallel()
	const scoped = "(ctx context.Context, s ...scopes.Sale) "
	for _, tt := range []struct {
		method string
		// count gives sales a field Count, whose column a projection's Count could read too.
		count bool
		want  string
	}{
		{method: "SumAmountByStatus" + scoped + "(model.Cents, error)",
			want: "SumAmountByStatus: it reads the sum of a column, but model.Sale has no field named AmountByStatus\n" +
				"  name it after a field, as in SumTotal(ctx context.Context, s ...scopes.Order) (int64, error), whose " +
				"keys and scopes tell the rows"},
		{method: "Sum" + scoped + "(int64, error)",
			want: "Sum: it names no field after Sum, whose column it reads the sum of"},
		{method: "SumStatus" + scoped + "(model.Status, error)",
			want: "SumStatus: it reads the sum of Sale.Status, a model.Status, which is no number"},
		{method: "AvgCode" + scoped + "(float64, error)",
			want: "AvgCode: it reads the average of Sale.Code, whose column code is text, which holds no number"},
		{method: "MinPaid" + scoped + "(bool, error)",
			want: "MinPaid: it reads the minimum of Sale.Paid, a bool, which has no order"},
		{method: "MaxRef" + scoped + "(string, error)",
			want: "MaxRef: it reads the maximum of Sale.Ref, but the database has no max of a uuid, as its column ref is"},
		{method: "SumAmount" + scoped + "(int64, error)",
			want: "SumAmount: it reads the sum of Sale.Amount, a model.Cents, as an int64\n" +
				"  declare it as model.Cents, or *model.Cents, nil when there is no value"},
		{method: "AvgUnits" + scoped + "(int32, error)",
			want: "AvgUnits: it reads the average of Sale.Units as an int32, which is no floating-point number"},
		{method: "SumAmount" + scoped + "error", want: "SumAmount: kanna-orm cannot implement it as declared"},
		{method: "//kanna:lock update\n\tMaxAmount" + scoped + "(model.Cents, error)",
			want: "MaxAmount: //kanna:lock is for a Get, a Find, a First, a List, or an Update"},
		{method: "ListRegionCounts" + scoped + "([]RegionCount, error)", count: true,
			want: "ListRegionCounts: Count reads the number of the rows, and model.Sale has a field Count too, whose " +
				"column count it could read"},
		{method: "ListTagged" + scoped + "([]Tagged, error)",
			want: `ListTagged: SumAmount reads the sum of Sale.Amount, which orm:"amount" tells nothing of`},
		{method: "ListNarrowCounts" + scoped + "([]NarrowCount, error)",
			want: "ListNarrowCounts: Count reads the number of the rows as an int64, not an int32"},
		{method: "ListFirstNotes" + scoped + "([]FirstNotes, error)",
			want: "ListFirstNotes: MinNote reads the minimum of Sale.Note, which may be NULL in every row of a group, " +
				"as a string\n  declare it as *string, nil for such a group"},
		{method: "ListNoteSums" + scoped + "([]NoteSums, error)",
			want: "ListNoteSums: SumNote reads the sum of Sale.Note, a string, which is no number"},
		{method: "FirstStatusCount" + scoped + "(StatusCount, error)",
			want: "FirstStatusCount: a projection of aggregates is read by a List, which returns those of each group"},
		{method: "ListTotals" + scoped + "([]Totals, error)",
			want: "ListTotals: a projection of aggregates groups the rows by its other fields, and repository.Totals " +
				"has none"},
		{method: "ListCustomerCounts" + scoped + "([]CustomerCount, error)",
			want: "ListCustomerCounts: Customer joins a table, and a projection of aggregates joins none"},
		{method: "//kanna:lock update\n\tListStatusCounts" + scoped + "([]StatusCount, error)",
			want: "ListStatusCounts: a projection of aggregates is read of groups of rows, which hold no lock"},
	} {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()
			archive := strings.Replace(aggregateShop, "\tListStatusCounts(ctx context.Context, s ...scopes.Sale) "+
				"([]StatusCount, error)\n", "", 1)
			archive = strings.Replace(archive+aggregateMistakes, "type Sales interface {",
				"type Sales interface {\n\t"+tt.method, 1)
			fields := "\tPaid       bool\n\tRef        string\n\tCode       Code\n\tCustomerID int64\n"
			if tt.count {
				fields += "\tCount      int64\n"
			}
			archive = strings.Replace(archive, "\tSoldAt   time.Time\n", "\tSoldAt   time.Time\n"+fields, 1)
			_, code, errOut := generateAggregateShop(t, archive, snapshot.PostgreSQL, "pgx", aggregateMistakeTables)
			if code != cli.ExitFailure || !strings.Contains(errOut, tt.want) {
				t.Fatalf("generate = %d\n%s\nwant\n%s", code, errOut, tt.want)
			}
		})
	}
}

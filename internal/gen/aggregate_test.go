package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

// aggregateShop declares sales, with columns of numbers, of strings, and of times, some of which may be NULL; and a
// repository reading aggregates into values, one reading them into pointers, and one by a key and no scopes.
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
// zero, the sum of integers a bigint and an average a double precision number on PostgreSQL, and an average a DOUBLE on
// MySQL.
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
		}},
		{snapshot.MySQL, []string{
			"`SELECT coalesce(sum(units), 0) FROM sales`+rd.Where()",
			"`SELECT CAST(avg(units) AS DOUBLE) FROM sales`+rd.Where()",
			"`SELECT sum(amount) FROM sales`+rd.Where()",
			"const q = `SELECT coalesce(sum(amount), 0) FROM sales WHERE region = ?`",
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

// aggregateMistakes adds to aggregateShop a number stored as text, a field of sales kanna-orm reads no aggregate of,
// and others the test declares.
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
`

// aggregateMistakeTables adds to the tables of aggregateShop the columns of the fields a test adds to sales.
func aggregateMistakeTables(tables map[string]snapshot.Table) {
	sales := tables["sales"]
	sales.Columns = append(sales.Columns, snapshot.Column{Name: "paid", Type: "bool", NotNull: true},
		snapshot.Column{Name: "ref", Type: "uuid", NotNull: true},
		snapshot.Column{Name: "code", Type: "text", NotNull: true})
	tables["sales"] = sales
}

func TestCommand_AggregateMistakes(t *testing.T) {
	t.Parallel()
	const scoped = "(ctx context.Context, s ...scopes.Sale) "
	for _, tt := range []struct {
		method string
		want   string
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
	} {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()
			archive := strings.Replace(aggregateShop+aggregateMistakes, "type Sales interface {",
				"type Sales interface {\n\t"+tt.method, 1)
			archive = strings.Replace(archive, "\tSoldAt   time.Time\n",
				"\tSoldAt   time.Time\n\tPaid     bool\n\tRef      string\n\tCode     Code\n", 1)
			_, code, errOut := generateAggregateShop(t, archive, snapshot.PostgreSQL, "pgx", aggregateMistakeTables)
			if code != cli.ExitFailure || !strings.Contains(errOut, tt.want) {
				t.Fatalf("generate = %d\n%s\nwant\n%s", code, errOut, tt.want)
			}
		})
	}
}

package gen_test

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-kanna/orm"
	mrepository "github.com/go-kanna/orm/internal/gen/testdata/mysql/repository"
	mscopes "github.com/go-kanna/orm/internal/gen/testdata/mysql/scopes"
	mstdlib "github.com/go-kanna/orm/internal/gen/testdata/mysql/stdlib"
	prepository "github.com/go-kanna/orm/internal/gen/testdata/postgres/repository"
	pscopes "github.com/go-kanna/orm/internal/gen/testdata/postgres/scopes"
	"github.com/go-kanna/orm/internal/snapshot"
)

// aggregateRuntime reads the aggregates of the sales of aggregateShop, which open writes into a namespace of their own.
const aggregateRuntime = `package impl

import (
	"cmp"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"example.com/shop/repository"
	"example.com/shop/scopes"
	"github.com/go-kanna/orm"
)

func TestAggregates(t *testing.T) {
	db, exec := open(t)
	ctx := t.Context()
	sales, optional, regional := NewSales(db), NewOptional(db), NewRegional(db)
	all, west, north := scopes.Sale{}, scopes.Sale{}.RegionEq("west"), scopes.Sale{}.RegionEq("north")
	day := func(d int) time.Time { return time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC) }
	check := func(what string, ok bool, got any, err error) {
		t.Helper()
		if err != nil || !ok {
			t.Errorf("%s = %v, %v", what, got, err)
		}
	}
	notFound := func(what string, got any, err error) {
		t.Helper()
		if !errors.Is(err, orm.ErrNotFound) {
			t.Errorf("%s = %v, %v; want orm.ErrNotFound", what, got, err)
		}
	}

	units, err := sales.SumUnits(ctx)
	check("SumUnits", units == 11, units, err)
	amount, err := sales.SumAmount(ctx, west)
	check("SumAmount of the west", amount == 350, amount, err)
	amount, err = sales.SumAmount(ctx, north)
	check("SumAmount of no sale", amount == 0, amount, err)
	discount, err := sales.SumDiscount(ctx)
	check("SumDiscount", discount == 10, discount, err)
	discount, err = sales.SumDiscount(ctx, west)
	check("SumDiscount of sales of no discount", discount == 0, discount, err)
	rate, err := sales.SumRate(ctx)
	check("SumRate", rate == 7.5, rate, err)
	at, err := sales.MinSoldAt(ctx)
	check("MinSoldAt", at.Equal(day(1)), at, err)
	at, err = sales.MaxSoldAt(ctx, west)
	check("MaxSoldAt of the west", at.Equal(day(4)), at, err)
	at, err = sales.MaxSoldAt(ctx, north)
	notFound("MaxSoldAt of no sale", at, err)
	note, err := sales.MinNote(ctx)
	check("MinNote", note == "a", note, err)
	note, err = sales.MinNote(ctx, scopes.Sale{}.IDEq(2))
	notFound("MinNote of a sale of no note", note, err)
	status, err := sales.MaxStatus(ctx)
	check("MaxStatus", status == "open", status, err)
	average, err := sales.AvgUnits(ctx, scopes.Sale{}.IDLte(3))
	check("AvgUnits, unrounded", math.Abs(average-10.0/3) < 1e-9, average, err)
	small, err := sales.AvgAmount(ctx)
	check("AvgAmount", small == 162.5, small, err)
	average, err = sales.AvgPrice(ctx)
	check("AvgPrice", math.Abs(average-1.9) < 1e-9, average, err)
	average, err = sales.AvgUnits(ctx, north)
	notFound("AvgUnits of no sale", average, err)
	for _, tt := range []struct {
		scope scopes.Sale
		want  string
	}{
		{all.IDAsc(), "orm: SumAmount takes conditions alone, but its scope orders the rows"},
		{all.Limit(1), "orm: SumAmount takes conditions alone, but its scope keeps only some of the rows"},
	} {
		if _, err := sales.SumAmount(ctx, tt.scope); err == nil || err.Error() != tt.want {
			t.Errorf("SumAmount of a scope = %v, want %s", err, tt.want)
		}
	}

	sum, err := optional.SumAmount(ctx, north)
	check("SumAmount of no sale, into a pointer", sum == nil, sum, err)
	sum, err = optional.SumAmount(ctx)
	check("SumAmount into a pointer", sum != nil && *sum == 650, sum, err)
	some, err := optional.SumDiscount(ctx, west)
	check("SumDiscount of sales of no discount, into a pointer", some == nil, some, err)
	first, err := optional.MinSoldAt(ctx, north)
	check("MinSoldAt of no sale, into a pointer", first == nil, first, err)
	some, err = optional.MaxDiscount(ctx, west)
	check("MaxDiscount of sales of no discount", some == nil, some, err)
	some, err = optional.MaxDiscount(ctx)
	check("MaxDiscount", some != nil && *some == 10, some, err)
	rates, err := optional.AvgRate(ctx)
	check("AvgRate", rates != nil && *rates == 1.875, rates, err)
	rates, err = optional.AvgRate(ctx, north)
	check("AvgRate of no sale", rates == nil, rates, err)

	amount, err = regional.SumAmount(ctx, "east")
	check("SumAmount of the east, by a key", amount == 300, amount, err)
	amount, err = regional.SumAmount(ctx, "north")
	check("SumAmount of no sale, by a key", amount == 0, amount, err)
	least, err := regional.MinDiscount(ctx, "west")
	if !errors.Is(err, orm.ErrNotFound) || err.Error() != "min sales.discount with region west: orm: not found" {
		t.Errorf("MinDiscount of sales of no discount, by a key = %v, %v", least, err)
	}

	byStatus := func(counts []repository.StatusCount) []repository.StatusCount {
		slices.SortFunc(counts, func(a, b repository.StatusCount) int { return cmp.Compare(a.Status, b.Status) })
		return counts
	}
	counts, err := sales.ListStatusCounts(ctx)
	check("ListStatusCounts", slices.Equal(byStatus(counts), []repository.StatusCount{{"closed", 1}, {"open", 3}}),
		counts, err)
	counts, err = sales.ListStatusCounts(ctx, west)
	check("ListStatusCounts of the west", slices.Equal(counts, []repository.StatusCount{{"open", 2}}), counts, err)
	counts, err = regional.ListStatusCounts(ctx, "east", scopes.Sale{}.StatusEq("open"))
	check("ListStatusCounts by a key", slices.Equal(counts, []repository.StatusCount{{"open", 1}}), counts, err)
	counts, err = sales.ListStatusCounts(ctx, north)
	check("ListStatusCounts of no sale", counts != nil && len(counts) == 0, counts, err)
	if _, err := sales.ListStatusCounts(ctx, all.StatusAsc()); err == nil ||
		err.Error() != "orm: ListStatusCounts takes conditions alone, but its scope orders the rows" {
		t.Errorf("ListStatusCounts in order = %v", err)
	}

	totals, err := sales.ListRegionTotals(ctx)
	slices.SortFunc(totals, func(a, b repository.RegionTotal) int { return cmp.Compare(a.Region, b.Region) })
	if err != nil || len(totals) != 2 {
		t.Fatalf("ListRegionTotals = %+v, %v", totals, err)
	}
	east, westTotal := totals[0], totals[1]
	if east.Region != "east" || east.Count != 2 || east.SumAmount != 300 || east.SumDiscount != 10 ||
		!east.MinSoldAt.Equal(day(1)) || east.MaxDiscount == nil || *east.MaxDiscount != 10 || east.AvgRate != 2 {
		t.Errorf("the totals of the east = %+v", east)
	}
	if westTotal.Region != "west" || westTotal.Count != 2 || westTotal.SumAmount != 350 || westTotal.SumDiscount != 0 ||
		!westTotal.MinSoldAt.Equal(day(3)) || westTotal.MaxDiscount != nil || westTotal.AvgRate != 1.75 {
		t.Errorf("the totals of the west, of no discount = %+v", westTotal)
	}

	notes, err := sales.ListNoteCounts(ctx)
	described := map[string]int64{}
	for _, n := range notes {
		key := "NULL"
		if n.Note != nil {
			key = *n.Note
		}
		described[key] = n.Count
	}
	if err != nil || len(notes) != 3 || described["a"] != 1 || described["b"] != 1 || described["NULL"] != 2 {
		t.Errorf("ListNoteCounts = %v, %v", described, err)
	}

	// A sum too large for the type it is read into is an error, not a value cut short.
	exec(overflowing)
	if units, err := sales.SumUnits(ctx, scopes.Sale{}.RegionEq("big")); err == nil {
		t.Errorf("SumUnits past int32 = %d", units)
	}
	if amount, err := sales.SumAmount(ctx, scopes.Sale{}.RegionEq("huge")); err == nil {
		t.Errorf("SumAmount past int64 = %d", amount)
	}
}
`

// aggregateSales are the rows of aggregateShop, the times as d writes them; and overflowing, rows whose sums no Go
// type of theirs holds.
func aggregateSales(d snapshot.Dialect) string {
	zone := "+00"
	if d == snapshot.MySQL {
		zone = ""
	}
	return `
const sales = "INSERT INTO sales (region, status, units, amount, discount, rate, price, note, sold_at) VALUES " +
	"('east', 'open', 2, 100, 10, 1.5, 1.25, 'a', '2026-01-01 00:00:00` + zone + `'), " +
	"('east', 'closed', 3, 200, NULL, 2.5, 2.50, NULL, '2026-01-02 00:00:00` + zone + `'), " +
	"('west', 'open', 5, 300, NULL, 3.0, 3.75, 'b', '2026-01-03 00:00:00` + zone + `'), " +
	"('west', 'open', 1, 50, NULL, 0.5, 0.10, NULL, '2026-01-04 00:00:00` + zone + `')"

const overflowing = "INSERT INTO sales (region, status, units, amount, rate, price, sold_at) VALUES " +
	"('big', 'open', 2000000000, 0, 0, 0, '2026-01-05 00:00:00` + zone + `'), " +
	"('big', 'open', 2000000000, 0, 0, 0, '2026-01-05 00:00:00` + zone + `'), " +
	"('huge', 'open', 0, 9000000000000000000, 0, 0, '2026-01-05 00:00:00` + zone + `'), " +
	"('huge', 'open', 0, 9000000000000000000, 0, 0, '2026-01-05 00:00:00` + zone + `')"
`
}

// aggregateOpen opens the database of each driver, creates sales in a namespace of the test's own, dropped when it
// ends, and writes its rows.
var aggregateOpen = map[string]string{
	"pgx": `package impl

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func open(t *testing.T) (DBTX, func(string)) {
	url := os.Getenv("KANNA_ORM_TEST_POSTGRES")
	if url == "" {
		t.Skip("KANNA_ORM_TEST_POSTGRES is not set")
	}
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.WithoutCancel(ctx)) })
	exec := func(statement string) {
		t.Helper()
		if _, err := conn.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	schema := fmt.Sprintf("kanna_aggregate_%d", time.Now().UnixNano())
	exec("CREATE SCHEMA " + schema + "; SET search_path TO " + schema)
	t.Cleanup(func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "DROP SCHEMA "+schema+" CASCADE") })
	exec(postgresSales)
	exec(sales)
	return conn, exec
}
`,
	"database/sql postgres": `package impl

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func open(t *testing.T) (DBTX, func(string)) {
	url := os.Getenv("KANNA_ORM_TEST_POSTGRES")
	if url == "" {
		t.Skip("KANNA_ORM_TEST_POSTGRES is not set")
	}
	conn := connect(t, "pgx", url)
	exec := execOn(t, conn)
	schema := fmt.Sprintf("kanna_aggregate_%d", time.Now().UnixNano())
	exec("CREATE SCHEMA " + schema)
	t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	exec("SET search_path TO " + schema)
	exec(postgresSales)
	exec(sales)
	return conn, exec
}
` + aggregateConnect,
	"database/sql mysql": `package impl

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func open(t *testing.T) (DBTX, func(string)) {
	dsn := os.Getenv("KANNA_ORM_TEST_MYSQL")
	if dsn == "" {
		t.Skip("KANNA_ORM_TEST_MYSQL is not set")
	}
	conn := connect(t, "mysql", dsn+"&parseTime=true")
	exec := execOn(t, conn)
	name := fmt.Sprintf("kanna_aggregate_%d", time.Now().UnixNano())
	exec("CREATE DATABASE " + name)
	t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), "DROP DATABASE "+name) })
	exec("USE " + name)
	exec("CREATE TABLE sales (id bigint AUTO_INCREMENT PRIMARY KEY, region varchar(64) NOT NULL, " +
		"status varchar(64) NOT NULL, units int NOT NULL, amount bigint NOT NULL, discount bigint, " +
		"rate double NOT NULL, price decimal(10,2) NOT NULL, note varchar(64), sold_at datetime(6) NOT NULL)")
	exec(sales)
	return conn, exec
}
` + aggregateConnect,
}

// aggregateConnect connects to a database through database/sql, by one connection, which a namespace chosen holds.
const aggregateConnect = `
func connect(t *testing.T, driver, dsn string) *sql.Conn {
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func execOn(t *testing.T, conn *sql.Conn) func(string) {
	return func(statement string) {
		t.Helper()
		if _, err := conn.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
}
`

const aggregatePostgresSchema = `
const postgresSales = "CREATE TABLE sales (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, region text NOT NULL, " +
	"status text NOT NULL, units integer NOT NULL, amount bigint NOT NULL, discount bigint, " +
	"rate double precision NOT NULL, price numeric(10,2) NOT NULL, note text, sold_at timestamptz NOT NULL)"
`

// aggregatesOfOrders checks the aggregates of the orders of ordersSchema that repo reads.
func aggregatesOfOrders(t *testing.T, repo interface {
	SumTotal(ctx context.Context, s ...pscopes.Order) (int64, error)
	MaxPlacedAt(ctx context.Context, s ...pscopes.Order) (*time.Time, error)
	AvgTotal(ctx context.Context, customerID int64) (float64, error)
	ListStatusCounts(ctx context.Context, s ...pscopes.Order) ([]prepository.StatusCount, error)
},
) {
	t.Helper()

	ctx, last := t.Context(), time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	none := pscopes.Order{}.StatusEq("none")
	if sum, err := repo.SumTotal(ctx); err != nil || sum != 600 {
		t.Errorf("SumTotal = %d, %v; want 600", sum, err)
	}
	if sum, err := repo.SumTotal(ctx, none); err != nil || sum != 0 {
		t.Errorf("SumTotal of no order = %d, %v; want 0", sum, err)
	}
	if at, err := repo.MaxPlacedAt(ctx); err != nil || at == nil || !at.Equal(last) {
		t.Errorf("MaxPlacedAt = %v, %v", at, err)
	}
	if at, err := repo.MaxPlacedAt(ctx, none); err != nil || at != nil {
		t.Errorf("MaxPlacedAt of no order = %v, %v; want nil", at, err)
	}
	if avg, err := repo.AvgTotal(ctx, 10); err != nil || avg != 150 {
		t.Errorf("AvgTotal of customer 10 = %v, %v; want 150", avg, err)
	}
	if avg, err := repo.AvgTotal(ctx, 99); !errors.Is(err, orm.ErrNotFound) {
		t.Errorf("AvgTotal of no order = %v, %v; want orm.ErrNotFound", avg, err)
	}
	counts, err := repo.ListStatusCounts(ctx)
	slices.SortFunc(counts, func(a, b prepository.StatusCount) int { return cmp.Compare(a.Status, b.Status) })
	if err != nil || !slices.Equal(counts, []prepository.StatusCount{{Status: "closed", Count: 1},
		{Status: "open", Count: 2}}) {
		t.Errorf("ListStatusCounts = %v, %v", counts, err)
	}
	if _, err := repo.ListStatusCounts(ctx, pscopes.Order{}.WithLines()); err == nil {
		t.Error("ListStatusCounts loaded a relation")
	}
}

func TestAggregates_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) { aggregatesOfOrders(t, repo) })
}

func TestAggregates_MySQL(t *testing.T) {
	t.Parallel()
	repo := mstdlib.NewOrderRepository(mysqlDB(t))
	ctx, last := t.Context(), time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	none := mscopes.Order{}.StatusEq("none")
	if sum, err := repo.SumTotal(ctx); err != nil || sum != 600 {
		t.Errorf("SumTotal = %d, %v; want 600", sum, err)
	}
	if sum, err := repo.SumTotal(ctx, none); err != nil || sum != 0 {
		t.Errorf("SumTotal of no order = %d, %v; want 0", sum, err)
	}
	if at, err := repo.MaxPlacedAt(ctx); err != nil || at == nil || !at.Equal(last) {
		t.Errorf("MaxPlacedAt = %v, %v", at, err)
	}
	if avg, err := repo.AvgTotal(ctx, 99); !errors.Is(err, orm.ErrNotFound) {
		t.Errorf("AvgTotal of no order = %v, %v; want orm.ErrNotFound", avg, err)
	}
	counts, err := repo.ListStatusCounts(ctx, mscopes.Order{}.CustomerIDEq(10))
	slices.SortFunc(counts, func(a, b mrepository.StatusCount) int { return cmp.Compare(a.Status, b.Status) })
	if err != nil || !slices.Equal(counts, []mrepository.StatusCount{{Status: "closed", Count: 1},
		{Status: "open", Count: 1}}) {
		t.Errorf("ListStatusCounts of customer 10 = %v, %v", counts, err)
	}
	if _, err := repo.ListStatusCounts(ctx, mscopes.Order{}.IDAsc()); err == nil {
		t.Error("ListStatusCounts took an order")
	}
}

func TestCommand_Aggregates(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, driver string
		d            snapshot.Dialect
	}{
		{"pgx", "pgx", snapshot.PostgreSQL},
		{"database/sql postgres", "database/sql", snapshot.PostgreSQL},
		{"database/sql mysql", "database/sql", snapshot.MySQL},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rows := aggregateSales(tt.d)
			if tt.d == snapshot.PostgreSQL {
				rows += aggregatePostgresSchema
			}
			archive := aggregateShop + "\n-- impl/aggregate_test.go --\n" + aggregateRuntime +
				"\n-- impl/open_test.go --\n" + aggregateOpen[tt.name] +
				"\n-- impl/rows_test.go --\npackage impl\n" + strings.TrimPrefix(rows, "\n")
			dir, code, errOut := generateAggregateShop(t, archive, tt.d, tt.driver)
			if code != 0 {
				t.Fatalf("generate = %d\n%s", code, errOut)
			}
			moduleCommand(t, dir, "test", "-mod=mod", "./...")
		})
	}
}

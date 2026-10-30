package bench_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/go-kanna/orm/bench/postgres/bun"
	"github.com/go-kanna/orm/bench/postgres/ent"
	entdb "github.com/go-kanna/orm/bench/postgres/ent/db"
	"github.com/go-kanna/orm/bench/postgres/gorm"
	"github.com/go-kanna/orm/bench/postgres/kanna"
	"github.com/go-kanna/orm/bench/postgres/kanna/model"
	"github.com/go-kanna/orm/bench/postgres/kanna/pgxstore"
	"github.com/go-kanna/orm/bench/postgres/kanna/sqlstore"
	"github.com/go-kanna/orm/bench/postgres/pgx"
	"github.com/go-kanna/orm/bench/postgres/sqlc"
	sqlcdb "github.com/go-kanna/orm/bench/postgres/sqlc/db"
)

// What the benchmarks read and write, among the rows migrate fills the tables with.
const (
	orders        = 10000
	getOrder      = 4 // an order with a note, which TestSameReads gets
	canceledOrder = 2 // an order the reads do not see, which the writes add lines to
	lines         = 100
)

var (
	// since is ten days before the last order: the latest open orders placed since then are many more than a page.
	since   = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	created = time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
)

func BenchmarkGet(b *testing.B) {
	for _, s := range setUp(b).subjects {
		b.Run(s.name, func(b *testing.B) {
			var id int64
			for b.Loop() {
				id = id%orders + 1
				if err := s.get(b.Context(), id); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkList(b *testing.B) {
	for _, s := range setUp(b).subjects {
		b.Run(s.name, func(b *testing.B) {
			for b.Loop() {
				if err := s.list(b.Context(), since); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkListWithLines(b *testing.B) {
	for _, s := range setUp(b).subjects {
		b.Run(s.name, func(b *testing.B) {
			for b.Loop() {
				if err := s.listWithLines(b.Context(), since); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkKeysetPage(b *testing.B) {
	env := setUp(b)
	for _, s := range env.subjects {
		b.Run(s.name, func(b *testing.B) {
			for b.Loop() {
				if err := s.page(b.Context(), env.after.PlacedAt, env.after.ID); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkInsert(b *testing.B) {
	env := setUp(b)
	for _, s := range env.subjects {
		b.Run(s.name, func(b *testing.B) {
			b.Cleanup(func() { env.clean(b, "customers", "DELETE FROM customers WHERE email LIKE 'bench%'") })
			n := 0
			for b.Loop() {
				n++
				if _, err := s.insert(b.Context(), "bench"+strconv.Itoa(n)+"@example.com", "Bench", created); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkInsertMany(b *testing.B) {
	env := setUp(b)
	for _, s := range env.subjects {
		b.Run(s.name, func(b *testing.B) {
			b.Cleanup(func() { env.clean(b, "order_lines", "DELETE FROM order_lines WHERE sku = 'BENCH'") })
			for b.Loop() {
				if _, err := s.insertLines(b.Context(), canceledOrder, lines); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkUpsert(b *testing.B) {
	env := setUp(b)
	for _, s := range env.subjects {
		b.Run(s.name, func(b *testing.B) {
			b.Cleanup(func() { env.clean(b, "customers", "") })
			for b.Loop() {
				if _, err := s.upsert(b.Context(), "customer0001@example.com", "Customer 1", created); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestSameReads checks that every subject reads the same rows, in the same order, as the benchmarks have them read.
func TestSameReads(t *testing.T) {
	t.Parallel()
	env := setUp(t)
	want, err := env.subjects[0].read(t.Context(), env.after)
	if err != nil {
		t.Fatal(err)
	}
	if len(want.list) != 20 || len(want.page) != 20 || len(want.withLines) != 20 || len(want.withLines[0].Lines) != 5 {
		t.Fatalf("%s reads %d orders, a page of %d, and %d with their lines: is the database migrate filled?",
			env.subjects[0].name, len(want.list), len(want.page), len(want.withLines))
	}
	for _, s := range env.subjects[1:] {
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			got, err := s.read(t.Context(), env.after)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("reads\n%+v\nwhile %s reads\n%+v", got, env.subjects[0].name, want)
			}
		})
	}
}

// TestSameWrites checks that every subject writes what the benchmarks have it write, and reads back the IDs.
func TestSameWrites(t *testing.T) {
	t.Parallel()
	env := setUp(t)
	for i, s := range env.subjects {
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			email, orderID := "same-"+s.name+"@example.com", int64(canceledOrder+3*i)
			t.Cleanup(func() {
				ctx := context.WithoutCancel(ctx)
				_, err := env.pool.Exec(ctx, "DELETE FROM customers WHERE email = $1", email)
				if err == nil {
					_, err = env.pool.Exec(ctx, "DELETE FROM order_lines WHERE order_id = $1 AND sku = 'BENCH'", orderID)
				}
				if err != nil {
					t.Error(err)
				}
			})

			id, err := s.insert(ctx, email, "Before", created)
			if err != nil {
				t.Fatal(err)
			}
			upserted, err := s.upsert(ctx, email, "After", created.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			var name string
			var at time.Time
			err = env.pool.QueryRow(ctx, "SELECT name, created_at FROM customers WHERE id = $1", id).Scan(&name, &at)
			if err != nil {
				t.Fatal(err)
			}
			if upserted != id || name != "After" || !at.Equal(created) {
				t.Errorf("upserts %d to %q joined %v; want %d to %q joined %v", upserted, name, at, id, "After", created)
			}

			n, err := s.insertLines(ctx, orderID, 3)
			if err != nil {
				t.Fatal(err)
			}
			var count int
			err = env.pool.QueryRow(ctx, "SELECT count(*) FROM order_lines WHERE order_id = $1 AND sku = 'BENCH'", orderID).
				Scan(&count)
			if err != nil {
				t.Fatal(err)
			}
			if n != 3 || count != 3 {
				t.Errorf("inserts %d lines, and reads back %d; want 3 of both", count, n)
			}
		})
	}
}

// subject is one way of reading and writing the tables, run by the benchmarks without the results, which differ in
// type from one to another.
type subject struct {
	name          string
	get           func(ctx context.Context, id int64) error
	list          func(ctx context.Context, since time.Time) error
	listWithLines func(ctx context.Context, since time.Time) error
	page          func(ctx context.Context, placedAt time.Time, id int64) error
	insert        func(ctx context.Context, email, name string, createdAt time.Time) (int64, error)
	insertLines   func(ctx context.Context, orderID int64, n int) (int, error)
	upsert        func(ctx context.Context, email, name string, createdAt time.Time) (int64, error)
	read          func(ctx context.Context, after order) (reads, error)
}

// store is what each subject implements, in types of its own: O an order, and W an order with its lines.
type store[O, W any] interface {
	Get(ctx context.Context, id int64) (O, error)
	List(ctx context.Context, since time.Time) ([]O, error)
	ListWithLines(ctx context.Context, since time.Time) ([]W, error)
	Page(ctx context.Context, placedAt time.Time, id int64) ([]O, error)
	Insert(ctx context.Context, email, name string, createdAt time.Time) (int64, error)
	InsertLines(ctx context.Context, orderID int64, n int) (int, error)
	Upsert(ctx context.Context, email, name string, createdAt time.Time) (int64, error)
}

// newSubject runs s, turning what it reads into an order by toOrder and toOrderWithLines for the tests alone.
func newSubject[O, W any](name string, s store[O, W], toOrder func(O) order, toOrderWithLines func(W) order) subject {
	return subject{
		name: name,
		get: func(ctx context.Context, id int64) error {
			_, err := s.Get(ctx, id)
			return wrap(name, err)
		},
		list: func(ctx context.Context, since time.Time) error {
			_, err := s.List(ctx, since)
			return wrap(name, err)
		},
		listWithLines: func(ctx context.Context, since time.Time) error {
			_, err := s.ListWithLines(ctx, since)
			return wrap(name, err)
		},
		page: func(ctx context.Context, placedAt time.Time, id int64) error {
			_, err := s.Page(ctx, placedAt, id)
			return wrap(name, err)
		},
		insert:      s.Insert,
		insertLines: s.InsertLines,
		upsert:      s.Upsert,
		read: func(ctx context.Context, after order) (reads, error) {
			var r reads
			o, err := s.Get(ctx, getOrder)
			if err != nil {
				return reads{}, wrap(name, err)
			}
			r.get = toOrder(o)
			list, err := s.List(ctx, since)
			if err != nil {
				return reads{}, wrap(name, err)
			}
			r.list = mapSlice(list, toOrder)
			withLines, err := s.ListWithLines(ctx, since)
			if err != nil {
				return reads{}, wrap(name, err)
			}
			r.withLines = mapSlice(withLines, toOrderWithLines)
			page, err := s.Page(ctx, after.PlacedAt, after.ID)
			if err != nil {
				return reads{}, wrap(name, err)
			}
			r.page = mapSlice(page, toOrder)
			return r, nil
		},
	}
}

// wrap names the subject err came from, if any.
func wrap(name string, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// reads is what a subject reads, as the benchmarks have it read.
type reads struct {
	get       order
	list      []order
	withLines []order
	page      []order
}

// order is an order of any subject, with its lines when they were loaded.
type order struct {
	ID         int64
	CustomerID int64
	Status     string
	PlacedAt   time.Time
	Note       *string
	Lines      []line
}

type line struct {
	ID       int64
	OrderID  int64
	SKU      string
	Quantity int32
}

// mapSlice maps ts by f, keeping nil as nil.
func mapSlice[T, U any](ts []T, f func(T) U) []U {
	if ts == nil {
		return nil
	}
	us := make([]U, len(ts))
	for i, t := range ts {
		us[i] = f(t)
	}
	return us
}

func pgxOrder(o pgx.Order) order {
	return order{o.ID, o.CustomerID, o.Status, o.PlacedAt.UTC(), o.Note,
		mapSlice(o.Lines, func(l pgx.OrderLine) line { return line(l) })}
}

func sqlcOrder(o sqlcdb.Order) order {
	return order{o.ID, o.CustomerID, o.Status, o.PlacedAt.UTC(), o.Note, nil}
}

func sqlcOrderWithLines(o sqlc.OrderWithLines) order {
	r := sqlcOrder(o.Order)
	r.Lines = mapSlice(o.Lines, func(l sqlcdb.OrderLine) line { return line{l.ID, l.OrderID, l.Sku, l.Quantity} })
	return r
}

func kannaOrder(o model.Order) order {
	return order{o.ID, o.CustomerID, o.Status, o.PlacedAt.UTC(), o.Note,
		mapSlice(o.Lines, func(l model.OrderLine) line { return line(l) })}
}

func gormOrder(o gorm.Order) order {
	return order{o.ID, o.CustomerID, o.Status, o.PlacedAt.UTC(), o.Note,
		mapSlice(o.Lines, func(l gorm.OrderLine) line { return line(l) })}
}

func bunOrder(o bun.Order) order {
	return order{o.ID, o.CustomerID, o.Status, o.PlacedAt.UTC(), o.Note,
		mapSlice(o.Lines, func(l bun.OrderLine) line { return line{l.ID, l.OrderID, l.SKU, l.Quantity} })}
}

func entOrder(o *entdb.Order) order {
	return order{o.ID, o.CustomerID, o.Status, o.PlacedAt.UTC(), o.Note,
		mapSlice(o.Edges.Lines, func(l *entdb.OrderLine) line { return line{l.ID, l.OrderID, l.Sku, l.Quantity} })}
}

// env is what the benchmarks and the tests run on: the subjects, the pool they check the tables through, and the
// order a page continues after.
type env struct {
	subjects []subject
	pool     *pgxpool.Pool
	after    order
}

var connect = sync.OnceValues(func() (env, error) {
	ctx := context.Background()
	url := os.Getenv("KANNA_ORM_BENCH_POSTGRES")
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return env{}, fmt.Errorf("connect: %w", err)
	}
	config := *pool.Config().ConnConfig
	db := stdlib.OpenDB(config)
	g, err := gorm.New(db)
	if err != nil {
		return env{}, fmt.Errorf("gorm: %w", err)
	}

	subjects := []subject{
		newSubject("pgx", pgx.New(pool), pgxOrder, pgxOrder),
		newSubject("sqlc", sqlc.New(pool), sqlcOrder, sqlcOrderWithLines),
		newSubject("kanna",
			kanna.New(pgxstore.NewCustomerRepository(pool), pgxstore.NewOrderRepository(pool)), kannaOrder, kannaOrder),
		newSubject("kanna-sql",
			kanna.New(sqlstore.NewCustomerRepository(db), sqlstore.NewOrderRepository(db)), kannaOrder, kannaOrder),
		newSubject("gorm", g, gormOrder, gormOrder),
		newSubject("bun", bun.New(config), bunOrder, bunOrder),
		newSubject("ent", ent.New(db), entOrder, entOrder),
	}
	first, err := pgx.New(pool).List(ctx, since)
	if err != nil {
		return env{}, fmt.Errorf("the first page: %w", err)
	}
	if len(first) == 0 {
		return env{}, errors.New("no open orders since " + since.String() + ": is the database migrate filled?")
	}
	return env{subjects: subjects, pool: pool, after: pgxOrder(first[len(first)-1])}, nil
})

// setUp connects to the database $KANNA_ORM_BENCH_POSTGRES names, which migrate has filled, or skips tb.
func setUp(tb testing.TB) env {
	tb.Helper()
	if os.Getenv("KANNA_ORM_BENCH_POSTGRES") == "" {
		tb.Skip("KANNA_ORM_BENCH_POSTGRES is not set")
	}
	env, err := connect()
	if err != nil {
		tb.Fatal(err)
	}
	return env
}

// clean deletes the rows a benchmark wrote by del, if any, and vacuums table, so that the next subject starts from the
// table as migrate filled it.
func (e env) clean(tb testing.TB, table, del string) {
	tb.Helper()
	ctx := context.WithoutCancel(tb.Context())
	if del != "" {
		if _, err := e.pool.Exec(ctx, del); err != nil {
			tb.Error(err)
		}
	}
	if _, err := e.pool.Exec(ctx, "VACUUM ANALYZE "+table); err != nil {
		tb.Error(err)
	}
}

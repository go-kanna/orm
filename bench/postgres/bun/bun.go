// Package bun reads and writes the tables of the benchmarks through bun, on database/sql.
package bun

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

type Customer struct {
	bun.BaseModel `bun:"table:customers"`

	ID        int64 `bun:",pk,autoincrement"`
	Email     string
	Name      string
	CreatedAt time.Time
}

type Order struct {
	bun.BaseModel `bun:"table:orders"`

	ID         int64 `bun:",pk,autoincrement"`
	CustomerID int64
	Status     string
	PlacedAt   time.Time
	Note       *string
	Lines      []OrderLine `bun:"rel:has-many,join:id=order_id"`
}

type OrderLine struct {
	bun.BaseModel `bun:"table:order_lines"`

	ID       int64 `bun:",pk,autoincrement"`
	OrderID  int64
	SKU      string
	Quantity int32
}

type Store struct{ db *bun.DB }

// New returns a Store connecting by config through pgx/stdlib in the simple protocol, as bun's docs run pgx: bun puts
// the arguments into the query itself, and has nothing to prepare.
func New(config pgx.ConnConfig) Store {
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return Store{db: bun.NewDB(stdlib.OpenDB(config), pgdialect.New())}
}

func (s Store) Get(ctx context.Context, id int64) (Order, error) {
	var o Order
	if err := s.db.NewSelect().Model(&o).Where("id = ?", id).Scan(ctx); err != nil {
		return Order{}, fmt.Errorf("get order %d: %w", id, err)
	}
	return o, nil
}

func (s Store) List(ctx context.Context, since time.Time) ([]Order, error) {
	var orders []Order
	return list(ctx, s.recent(&orders, since), &orders)
}

func (s Store) ListWithLines(ctx context.Context, since time.Time) ([]Order, error) {
	var orders []Order
	q := s.recent(&orders, since).Relation("Lines", func(q *bun.SelectQuery) *bun.SelectQuery { return q.Order("id") })
	return list(ctx, q, &orders)
}

func (s Store) Page(ctx context.Context, placedAt time.Time, id int64) ([]Order, error) {
	var orders []Order
	return list(ctx, s.open(&orders).Where("(placed_at, id) < (?, ?)", placedAt, id).Limit(20), &orders)
}

func (s Store) Insert(ctx context.Context, email, name string, createdAt time.Time) (int64, error) {
	c := Customer{Email: email, Name: name, CreatedAt: createdAt}
	if _, err := s.db.NewInsert().Model(&c).Exec(ctx); err != nil {
		return 0, fmt.Errorf("insert customer: %w", err)
	}
	return c.ID, nil
}

func (s Store) InsertLines(ctx context.Context, orderID int64, n int) (int, error) {
	lines := make([]OrderLine, n)
	for i := range lines {
		lines[i] = OrderLine{OrderID: orderID, SKU: "BENCH", Quantity: int32(i + 1)}
	}
	if _, err := s.db.NewInsert().Model(&lines).Exec(ctx); err != nil {
		return 0, fmt.Errorf("insert order lines: %w", err)
	}
	return len(lines), nil
}

func (s Store) Upsert(ctx context.Context, email, name string, createdAt time.Time) (int64, error) {
	c := Customer{Email: email, Name: name, CreatedAt: createdAt}
	_, err := s.db.NewInsert().Model(&c).On("CONFLICT (email) DO UPDATE").Set("name = EXCLUDED.name").Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("upsert customer: %w", err)
	}
	return c.ID, nil
}

// open narrows to the open orders, latest first.
func (s Store) open(orders *[]Order) *bun.SelectQuery {
	return s.db.NewSelect().Model(orders).Where("status = ?", "open").Order("placed_at DESC", "id DESC")
}

// recent narrows to the latest 20 open orders placed since.
func (s Store) recent(orders *[]Order, since time.Time) *bun.SelectQuery {
	return s.open(orders).Where("placed_at >= ?", since).Limit(20)
}

func list(ctx context.Context, q *bun.SelectQuery, orders *[]Order) ([]Order, error) {
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	return *orders, nil
}

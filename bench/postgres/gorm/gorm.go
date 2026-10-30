// Package gorm reads and writes the tables of the benchmarks through GORM, on database/sql. It writes without the
// transaction GORM would otherwise begin for each write, as none of the others begins one.
package gorm

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Customer struct {
	ID        int64
	Email     string
	Name      string
	CreatedAt time.Time
}

type Order struct {
	ID         int64
	CustomerID int64
	Status     string
	PlacedAt   time.Time
	Note       *string
	Lines      []OrderLine
}

type OrderLine struct {
	ID       int64
	OrderID  int64
	SKU      string
	Quantity int32
}

type Store struct{ db *gorm.DB }

func New(db *sql.DB) (Store, error) {
	g, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		return Store{}, fmt.Errorf("open gorm: %w", err)
	}
	return Store{db: g}, nil
}

func (s Store) Get(ctx context.Context, id int64) (Order, error) {
	var o Order
	if err := s.db.WithContext(ctx).Take(&o, id).Error; err != nil {
		return Order{}, fmt.Errorf("get order %d: %w", id, err)
	}
	return o, nil
}

func (s Store) List(ctx context.Context, since time.Time) ([]Order, error) {
	return s.list(s.recent(ctx, since))
}

func (s Store) ListWithLines(ctx context.Context, since time.Time) ([]Order, error) {
	return s.list(s.recent(ctx, since).Preload("Lines", func(db *gorm.DB) *gorm.DB { return db.Order("id") }))
}

func (s Store) Page(ctx context.Context, placedAt time.Time, id int64) ([]Order, error) {
	return s.list(s.open(ctx).Where("(placed_at, id) < (?, ?)", placedAt, id).Limit(20))
}

func (s Store) Insert(ctx context.Context, email, name string, createdAt time.Time) (int64, error) {
	c := Customer{Email: email, Name: name, CreatedAt: createdAt}
	if err := s.db.WithContext(ctx).Create(&c).Error; err != nil {
		return 0, fmt.Errorf("insert customer: %w", err)
	}
	return c.ID, nil
}

func (s Store) InsertLines(ctx context.Context, orderID int64, n int) (int, error) {
	lines := make([]OrderLine, n)
	for i := range lines {
		lines[i] = OrderLine{OrderID: orderID, SKU: "BENCH", Quantity: int32(i + 1)}
	}
	if err := s.db.WithContext(ctx).Create(&lines).Error; err != nil {
		return 0, fmt.Errorf("insert order lines: %w", err)
	}
	return len(lines), nil
}

func (s Store) Upsert(ctx context.Context, email, name string, createdAt time.Time) (int64, error) {
	c := Customer{Email: email, Name: name, CreatedAt: createdAt}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "email"}},
		DoUpdates: clause.AssignmentColumns([]string{"name"}),
	}).Create(&c).Error
	if err != nil {
		return 0, fmt.Errorf("upsert customer: %w", err)
	}
	return c.ID, nil
}

// open narrows to the open orders, latest first.
func (s Store) open(ctx context.Context) *gorm.DB {
	return s.db.WithContext(ctx).Where("status = ?", "open").Order("placed_at DESC, id DESC")
}

// recent narrows to the latest 20 open orders placed since.
func (s Store) recent(ctx context.Context, since time.Time) *gorm.DB {
	return s.open(ctx).Where("placed_at >= ?", since).Limit(20)
}

func (Store) list(q *gorm.DB) ([]Order, error) {
	var orders []Order
	if err := q.Find(&orders).Error; err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	return orders, nil
}

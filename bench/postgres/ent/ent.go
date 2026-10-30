// Package ent reads and writes the tables of the benchmarks through the client ent generates in db, on database/sql.
package ent

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/go-kanna/orm/bench/postgres/ent/db"
	"github.com/go-kanna/orm/bench/postgres/ent/db/customer"
	"github.com/go-kanna/orm/bench/postgres/ent/db/order"
	"github.com/go-kanna/orm/bench/postgres/ent/db/orderline"
)

type Store struct{ c *db.Client }

func New(sqlDB *sql.DB) Store {
	return Store{c: db.NewClient(db.Driver(entsql.OpenDB(dialect.Postgres, sqlDB)))}
}

func (s Store) Get(ctx context.Context, id int64) (*db.Order, error) {
	o, err := s.c.Order.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get order %d: %w", id, err)
	}
	return o, nil
}

func (s Store) List(ctx context.Context, since time.Time) ([]*db.Order, error) {
	return list(ctx, s.recent(since))
}

func (s Store) ListWithLines(ctx context.Context, since time.Time) ([]*db.Order, error) {
	return list(ctx, s.recent(since).WithLines(func(q *db.OrderLineQuery) { q.Order(orderline.ByID()) }))
}

func (s Store) Page(ctx context.Context, placedAt time.Time, id int64) ([]*db.Order, error) {
	after := func(sel *entsql.Selector) {
		sel.Where(entsql.CompositeLT([]string{sel.C(order.FieldPlacedAt), sel.C(order.FieldID)}, placedAt, id))
	}
	return list(ctx, s.open().Where(after).Limit(20))
}

func (s Store) Insert(ctx context.Context, email, name string, createdAt time.Time) (int64, error) {
	c, err := s.c.Customer.Create().SetEmail(email).SetName(name).SetCreatedAt(createdAt).Save(ctx)
	if err != nil {
		return 0, fmt.Errorf("insert customer: %w", err)
	}
	return c.ID, nil
}

func (s Store) InsertLines(ctx context.Context, orderID int64, n int) (int, error) {
	builders := make([]*db.OrderLineCreate, n)
	for i := range builders {
		builders[i] = s.c.OrderLine.Create().SetOrderID(orderID).SetSku("BENCH").SetQuantity(int32(i + 1))
	}
	lines, err := s.c.OrderLine.CreateBulk(builders...).Save(ctx)
	if err != nil {
		return 0, fmt.Errorf("insert order lines: %w", err)
	}
	return len(lines), nil
}

func (s Store) Upsert(ctx context.Context, email, name string, createdAt time.Time) (int64, error) {
	id, err := s.c.Customer.Create().SetEmail(email).SetName(name).SetCreatedAt(createdAt).
		OnConflictColumns(customer.FieldEmail).UpdateName().ID(ctx)
	if err != nil {
		return 0, fmt.Errorf("upsert customer: %w", err)
	}
	return id, nil
}

// open narrows to the open orders, latest first.
func (s Store) open() *db.OrderQuery {
	return s.c.Order.Query().
		Where(order.Status("open")).
		Order(order.ByPlacedAt(entsql.OrderDesc()), order.ByID(entsql.OrderDesc()))
}

// recent narrows to the latest 20 open orders placed since.
func (s Store) recent(since time.Time) *db.OrderQuery {
	return s.open().Where(order.PlacedAtGTE(since)).Limit(20)
}

func list(ctx context.Context, q *db.OrderQuery) ([]*db.Order, error) {
	orders, err := q.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	return orders, nil
}

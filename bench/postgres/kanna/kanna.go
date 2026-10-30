// Package kanna reads and writes the tables of the benchmarks through the repositories kanna-orm implements, narrowed
// by the scopes it generates.
package kanna

import (
	"context"
	"fmt"
	"time"

	"github.com/go-kanna/orm/bench/postgres/kanna/model"
	"github.com/go-kanna/orm/bench/postgres/kanna/repository"
	"github.com/go-kanna/orm/bench/postgres/kanna/scopes"
)

type Store struct {
	customers repository.CustomerRepository
	orders    repository.OrderRepository
}

func New(customers repository.CustomerRepository, orders repository.OrderRepository) Store {
	return Store{customers: customers, orders: orders}
}

func (s Store) Get(ctx context.Context, id int64) (model.Order, error) {
	o, err := s.orders.Get(ctx, id)
	if err != nil {
		return model.Order{}, fmt.Errorf("get order %d: %w", id, err)
	}
	return o, nil
}

func (s Store) List(ctx context.Context, since time.Time) ([]model.Order, error) {
	return s.list(ctx, recent(since))
}

func (s Store) ListWithLines(ctx context.Context, since time.Time) ([]model.Order, error) {
	return s.list(ctx, recent(since).WithLines(scopes.OrderLines().IDAsc()))
}

func (s Store) Page(ctx context.Context, placedAt time.Time, id int64) ([]model.Order, error) {
	return s.list(ctx, open().After(model.Order{ID: id, PlacedAt: placedAt}).Limit(20))
}

func (s Store) Insert(ctx context.Context, email, name string, createdAt time.Time) (int64, error) {
	c, err := s.customers.Insert(ctx, model.Customer{Email: email, Name: name, CreatedAt: createdAt})
	if err != nil {
		return 0, fmt.Errorf("insert customer: %w", err)
	}
	return c.ID, nil
}

func (s Store) InsertLines(ctx context.Context, orderID int64, n int) (int, error) {
	lines := make([]model.OrderLine, n)
	for i := range lines {
		lines[i] = model.OrderLine{OrderID: orderID, SKU: "BENCH", Quantity: int32(i + 1)}
	}
	rows, err := s.orders.InsertLines(ctx, lines)
	if err != nil {
		return 0, fmt.Errorf("insert order lines: %w", err)
	}
	return len(rows), nil
}

func (s Store) Upsert(ctx context.Context, email, name string, createdAt time.Time) (int64, error) {
	c, err := s.customers.UpsertByEmail(ctx, model.Customer{Email: email, Name: name, CreatedAt: createdAt})
	if err != nil {
		return 0, fmt.Errorf("upsert customer: %w", err)
	}
	return c.ID, nil
}

func (s Store) list(ctx context.Context, scope scopes.Order) ([]model.Order, error) {
	orders, err := s.orders.List(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	return orders, nil
}

// open narrows to the open orders, latest first.
func open() scopes.Order {
	return scopes.Orders().StatusEq("open").PlacedAtDesc().IDDesc()
}

// recent narrows to the latest 20 open orders placed since.
func recent(since time.Time) scopes.Order {
	return open().PlacedAtGte(since).Limit(20)
}

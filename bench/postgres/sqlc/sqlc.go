// Package sqlc reads and writes the tables of the benchmarks through the queries sqlc generates from query.sql, on
// pgx.
package sqlc

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/go-kanna/orm/bench/postgres/sqlc/db"
)

//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

// OrderWithLines is an order with its lines, which sqlc leaves to its caller to put together.
type OrderWithLines struct {
	db.Order

	Lines []db.OrderLine
}

type Store struct{ q *db.Queries }

func New(pool *pgxpool.Pool) Store { return Store{q: db.New(pool)} }

func (s Store) Get(ctx context.Context, id int64) (db.Order, error) {
	o, err := s.q.GetOrder(ctx, id)
	if err != nil {
		return db.Order{}, fmt.Errorf("get order %d: %w", id, err)
	}
	return o, nil
}

func (s Store) List(ctx context.Context, since time.Time) ([]db.Order, error) {
	orders, err := s.q.ListOrders(ctx, db.ListOrdersParams{Status: "open", PlacedAt: since})
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	return orders, nil
}

func (s Store) ListWithLines(ctx context.Context, since time.Time) ([]OrderWithLines, error) {
	orders, err := s.List(ctx, since)
	if err != nil {
		return nil, err
	}
	out := make([]OrderWithLines, len(orders))
	ids := make([]int64, len(orders))
	byID := make(map[int64]int, len(orders))
	for i, o := range orders {
		out[i] = OrderWithLines{Order: o, Lines: []db.OrderLine{}}
		ids[i] = o.ID
		byID[o.ID] = i
	}
	lines, err := s.q.ListOrderLines(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list order lines: %w", err)
	}
	for _, l := range lines {
		o := &out[byID[l.OrderID]]
		o.Lines = append(o.Lines, l)
	}
	return out, nil
}

func (s Store) Page(ctx context.Context, placedAt time.Time, id int64) ([]db.Order, error) {
	orders, err := s.q.PageOrders(ctx, db.PageOrdersParams{Status: "open", PlacedAt: placedAt, ID: id})
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	return orders, nil
}

func (s Store) Insert(ctx context.Context, email, name string, createdAt time.Time) (int64, error) {
	id, err := s.q.InsertCustomer(ctx, db.InsertCustomerParams{Email: email, Name: name, CreatedAt: createdAt})
	if err != nil {
		return 0, fmt.Errorf("insert customer: %w", err)
	}
	return id, nil
}

func (s Store) InsertLines(ctx context.Context, orderID int64, n int) (int, error) {
	arg := db.InsertOrderLinesParams{
		OrderIds:   make([]int64, n),
		Skus:       make([]string, n),
		Quantities: make([]int32, n),
	}
	for i := range n {
		arg.OrderIds[i], arg.Skus[i], arg.Quantities[i] = orderID, "BENCH", int32(i+1)
	}
	ids, err := s.q.InsertOrderLines(ctx, arg)
	if err != nil {
		return 0, fmt.Errorf("insert order lines: %w", err)
	}
	return len(ids), nil
}

func (s Store) Upsert(ctx context.Context, email, name string, createdAt time.Time) (int64, error) {
	id, err := s.q.UpsertCustomer(ctx, db.UpsertCustomerParams{Email: email, Name: name, CreatedAt: createdAt})
	if err != nil {
		return 0, fmt.Errorf("upsert customer: %w", err)
	}
	return id, nil
}

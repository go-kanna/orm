// Package pgx reads and writes the tables of the benchmarks through pgx alone, by SQL written by hand: the baseline
// the others are measured against.
package pgx

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

type Store struct{ db *pgxpool.Pool }

func New(db *pgxpool.Pool) Store { return Store{db: db} }

func (s Store) Get(ctx context.Context, id int64) (Order, error) {
	const q = `SELECT id, customer_id, status, placed_at, note FROM orders WHERE id = $1`
	var o Order
	if err := s.db.QueryRow(ctx, q, id).Scan(&o.ID, &o.CustomerID, &o.Status, &o.PlacedAt, &o.Note); err != nil {
		return Order{}, fmt.Errorf("get order %d: %w", id, err)
	}
	return o, nil
}

func (s Store) List(ctx context.Context, since time.Time) ([]Order, error) {
	const q = `SELECT id, customer_id, status, placed_at, note FROM orders
WHERE status = $1 AND placed_at >= $2 ORDER BY placed_at DESC, id DESC LIMIT 20`
	return s.orders(ctx, q, "open", since)
}

func (s Store) ListWithLines(ctx context.Context, since time.Time) ([]Order, error) {
	orders, err := s.List(ctx, since)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(orders))
	byID := make(map[int64]int, len(orders))
	for i, o := range orders {
		ids[i] = o.ID
		byID[o.ID] = i
		orders[i].Lines = []OrderLine{}
	}
	const q = `SELECT id, order_id, sku, quantity FROM order_lines WHERE order_id = ANY($1) ORDER BY id`
	rows, err := s.db.Query(ctx, q, ids)
	if err != nil {
		return nil, fmt.Errorf("list order lines: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var l OrderLine
		if err := rows.Scan(&l.ID, &l.OrderID, &l.SKU, &l.Quantity); err != nil {
			return nil, fmt.Errorf("list order lines: %w", err)
		}
		o := &orders[byID[l.OrderID]]
		o.Lines = append(o.Lines, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list order lines: %w", err)
	}
	return orders, nil
}

func (s Store) Page(ctx context.Context, placedAt time.Time, id int64) ([]Order, error) {
	const q = `SELECT id, customer_id, status, placed_at, note FROM orders
WHERE status = $1 AND (placed_at, id) < ($2, $3) ORDER BY placed_at DESC, id DESC LIMIT 20`
	return s.orders(ctx, q, "open", placedAt, id)
}

func (s Store) Insert(ctx context.Context, email, name string, createdAt time.Time) (int64, error) {
	const q = `INSERT INTO customers (email, name, created_at) VALUES ($1, $2, $3) RETURNING id`
	var id int64
	if err := s.db.QueryRow(ctx, q, email, name, createdAt).Scan(&id); err != nil {
		return 0, fmt.Errorf("insert customer: %w", err)
	}
	return id, nil
}

func (s Store) InsertLines(ctx context.Context, orderID int64, n int) (int, error) {
	orderIDs := make([]int64, n)
	skus := make([]string, n)
	quantities := make([]int32, n)
	for i := range n {
		orderIDs[i], skus[i], quantities[i] = orderID, "BENCH", int32(i+1)
	}
	const q = `INSERT INTO order_lines (order_id, sku, quantity)
SELECT u.order_id, u.sku, u.quantity
FROM unnest($1::bigint[], $2::text[], $3::integer[]) AS u (order_id, sku, quantity)
RETURNING id`
	rows, err := s.db.Query(ctx, q, orderIDs, skus, quantities)
	if err != nil {
		return 0, fmt.Errorf("insert order lines: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return 0, fmt.Errorf("insert order lines: %w", err)
	}
	return len(ids), nil
}

func (s Store) Upsert(ctx context.Context, email, name string, createdAt time.Time) (int64, error) {
	const q = `INSERT INTO customers (email, name, created_at) VALUES ($1, $2, $3)
ON CONFLICT (email) DO UPDATE SET name = EXCLUDED.name RETURNING id`
	var id int64
	if err := s.db.QueryRow(ctx, q, email, name, createdAt).Scan(&id); err != nil {
		return 0, fmt.Errorf("upsert customer: %w", err)
	}
	return id, nil
}

func (s Store) orders(ctx context.Context, q string, args ...any) ([]Order, error) {
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()
	orders := []Order{}
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.CustomerID, &o.Status, &o.PlacedAt, &o.Note); err != nil {
			return nil, fmt.Errorf("list orders: %w", err)
		}
		orders = append(orders, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	return orders, nil
}

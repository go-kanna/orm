// Command migrate creates the tables of the benchmarks afresh in the database its argument names, and fills them:
// customers, ten orders of each, and five lines of each order.
package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

//go:embed schema.sql
var schema string

const (
	customers     = 1000
	orders        = 10 * customers
	linesPerOrder = 5
)

// start is when the first customer joined and the first orders were placed; six orders are placed each hour after.
var start = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func main() {
	if err := migrate(context.Background(), os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func migrate(ctx context.Context, url string) error {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, schema); err != nil {
		return fmt.Errorf("create the tables: %w", err)
	}
	if err := fill(ctx, conn); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, "ANALYZE customers, orders, order_lines"); err != nil {
		return fmt.Errorf("analyze: %w", err)
	}
	return nil
}

// fill copies the rows in, leaving the IDs to the database, which numbers each table's rows from 1 in the order given.
func fill(ctx context.Context, conn *pgx.Conn) error {
	statuses := []string{"open", "shipped", "canceled"}
	for _, t := range []struct {
		table   string
		columns []string
		rows    int
		row     func(n int) []any
	}{
		{"customers", []string{"email", "name", "created_at"}, customers, func(n int) []any {
			return []any{fmt.Sprintf("customer%04d@example.com", n), fmt.Sprintf("Customer %d", n), start}
		}},
		{"orders", []string{"customer_id", "status", "placed_at", "note"}, orders, func(n int) []any {
			var note *string
			if n%2 == 0 {
				note = new("gift wrap")
			}
			return []any{(n-1)%customers + 1, statuses[n%len(statuses)], start.Add(time.Duration(n/6) * time.Hour), note}
		}},
		{"order_lines", []string{"order_id", "sku", "quantity"}, orders * linesPerOrder, func(n int) []any {
			return []any{(n-1)/linesPerOrder + 1, fmt.Sprintf("SKU-%03d", n%100), (n-1)%linesPerOrder + 1}
		}},
	} {
		rows := pgx.CopyFromSlice(t.rows, func(i int) ([]any, error) { return t.row(i + 1), nil })
		if _, err := conn.CopyFrom(ctx, pgx.Identifier{t.table}, t.columns, rows); err != nil {
			return fmt.Errorf("fill %s: %w", t.table, err)
		}
	}
	return nil
}

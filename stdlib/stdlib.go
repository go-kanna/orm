// Package stdlib holds DBTX, what the repositories kanna-orm generates for database/sql run on. Each package of them
// declares its DBTX as this one, so that one value serves them all, as a dependency injector provides it once.
package stdlib

import (
	"context"
	"database/sql"
)

// DBTX is what the repositories run on: *sql.DB, *sql.Conn, or *sql.Tx. A DBTX of your own wrapping a transaction tells
// a method locking the rows it reads so by an Unwrap() DBTX returning it.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

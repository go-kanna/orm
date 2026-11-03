// Package pgx holds DBTX, what the repositories kanna-orm generates for pgx run on. Each package of them declares its
// DBTX as this one, so that one value serves them all, as a dependency injector provides it once.
package pgx

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBTX is what the repositories run on: *pgxpool.Pool, *pgx.Conn, or pgx.Tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

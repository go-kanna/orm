// Package pgx holds DBTX, what the repositories kanna-orm generates for pgx run on. Each package of them declares its
// DBTX as this one, or as CopyDBTX when kanna-orm writes a Copy for it, so that one value serves them all, as a
// dependency injector provides it once.
package pgx

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBTX is what the repositories run on: *pgxpool.Pool, *pgx.Conn, or pgx.Tx. A DBTX of your own wrapping a transaction
// tells a method locking the rows it reads so by an Unwrap() DBTX returning it.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// CopyDBTX is DBTX that copies rows, what the repositories of a package kanna-orm writes a Copy for run on:
// *pgxpool.Pool, *pgx.Conn, or pgx.Tx, as DBTX.
type CopyDBTX interface {
	DBTX
	CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64,
		error)
}

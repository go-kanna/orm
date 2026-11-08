// Package pgx holds DBTX, what the repositories kanna-orm generates for pgx run on. Each package of them declares its
// DBTX as this one, or as CopyDBTX when kanna-orm writes a Copy for it, so that one value serves them all, as a
// dependency injector provides it once.
package pgx

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBTX is what the repositories run on: *pgxpool.Pool, *pgx.Conn, or pgx.Tx.
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

// CopyFromSQL is the statement CopyFrom runs to copy rows into the columns columnNames of tableName, as the hooks of a
// repository see it: COPY "orders" ("customer_id", "status") FROM STDIN.
func CopyFromSQL(tableName pgx.Identifier, columnNames []string) string {
	columns := make([]string, len(columnNames))
	for i, name := range columnNames {
		columns[i] = pgx.Identifier{name}.Sanitize()
	}
	return "COPY " + tableName.Sanitize() + " (" + strings.Join(columns, ", ") + ") FROM STDIN"
}

// InTransaction reports whether db is a transaction, which has a Commit, or wraps one, as an Unwrap returning the DBTX
// it wraps tells, however many wrappers deep: what a lock needs to last beyond its statement.
func InTransaction(db DBTX) bool {
	for {
		if _, ok := db.(interface {
			Commit(ctx context.Context) error
		}); ok {
			return true
		}
		u, ok := db.(interface{ Unwrap() DBTX })
		if !ok {
			return false
		}
		db = u.Unwrap()
	}
}

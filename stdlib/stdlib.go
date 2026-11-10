// Package stdlib holds DBTX, what the repositories kanna-orm generates for database/sql run on. Each package of them
// declares its DBTX as this one, so that one value serves them all, as a dependency injector provides it once.
package stdlib

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// DBTX is what the repositories run on: *sql.DB, *sql.Conn, or *sql.Tx.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// InTransaction reports whether db is a transaction, which has a Commit, or wraps one, as an Unwrap returning the DBTX
// it wraps tells, however many wrappers deep: what a lock needs to last beyond its statement.
func InTransaction(db DBTX) bool {
	for {
		if _, ok := db.(interface{ Commit() error }); ok {
			return true
		}
		u, ok := db.(interface{ Unwrap() DBTX })
		if !ok {
			return false
		}
		db = u.Unwrap()
	}
}

// MySQLScanError returns err, an error of a Scan of a row of MySQL, told that the DSN of go-sql-driver/mysql needs
// parseTime=true when it is what the driver returns without it: a DATETIME or a TIMESTAMP, read as bytes, that a
// time.Time cannot hold. database/sql tells it by its message alone.
func MySQLScanError(err error) error {
	if err == nil || !strings.Contains(err.Error(), "storing driver.Value type []uint8 into type *time.Time") {
		return err
	}
	return fmt.Errorf("%w (a time.Time reads a DATETIME or a TIMESTAMP when the DSN of go-sql-driver/mysql has "+
		"parseTime=true)", err)
}

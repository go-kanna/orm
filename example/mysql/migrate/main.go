// Command migrate creates the tables of the example afresh in the database its argument, a DSN of
// go-sql-driver/mysql with multiStatements=true, names, as a migration tool would.
package main

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"

	_ "github.com/go-sql-driver/mysql"
)

//go:embed schema.sql
var schema string

func main() {
	if err := migrate(context.Background(), os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func migrate(ctx context.Context, dsn string) error {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create the tables: %w", err)
	}
	return nil
}

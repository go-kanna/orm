// Command migrate creates the tables of the example afresh in the database its argument names, as a migration tool
// would.
package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
)

//go:embed schema.sql
var schema string

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
	return nil
}

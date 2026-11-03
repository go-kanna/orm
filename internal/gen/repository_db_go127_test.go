//go:build go1.27

package gen_test

import (
	"database/sql"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	prepository "github.com/go-kanna/orm/internal/gen/testdata/postgres/repository"
	pstdlib "github.com/go-kanna/orm/internal/gen/testdata/postgres/stdlib"
)

// pgsqlRepository opens OrderRepository through database/sql on the database at url, closed when the test ends.
// database/sql reads an array into a slice through pgx/stdlib from Go 1.27 on, where the driver scans each column.
var pgsqlRepository = func(t *testing.T, url string) prepository.OrderRepository {
	t.Helper()

	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return pstdlib.NewOrderRepository(db)
}

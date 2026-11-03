package gen_test

import (
	"errors"
	"testing"

	"github.com/go-kanna/orm"
	mstdlib "github.com/go-kanna/orm/internal/gen/testdata/mysql/stdlib"
	ppgx "github.com/go-kanna/orm/internal/gen/testdata/postgres/pgx"
	prepository "github.com/go-kanna/orm/internal/gen/testdata/postgres/repository"
)

// TestMapError_Postgres checks that the pgx implementation, which has a mapError, returns its errors through it, and
// that the database/sql one, which has none, returns them as they are.
func TestMapError_Postgres(t *testing.T) {
	t.Parallel()
	onPostgres(t, func(t *testing.T, repo prepository.OrderRepository) {
		_, err := repo.Get(t.Context(), 99)
		_, maps := repo.(ppgx.OrderRepository)
		var mapped ppgx.MappedError
		if !errors.Is(err, orm.ErrNotFound) || errors.As(err, &mapped) != maps {
			t.Errorf("Get of no order = %v, want orm.ErrNotFound, through mapError %t", err, maps)
		}
	})
}

func TestMapError_MySQL(t *testing.T) {
	t.Parallel()
	repo := mstdlib.NewOrderRepository(mysqlDB(t))
	_, err := repo.Get(t.Context(), 99)
	var mapped mstdlib.MappedError
	if !errors.Is(err, orm.ErrNotFound) || !errors.As(err, &mapped) {
		t.Errorf("Get of no order = %v, want orm.ErrNotFound, through mapError", err)
	}
}

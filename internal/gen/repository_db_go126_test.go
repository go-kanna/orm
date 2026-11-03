//go:build !go1.27

package gen_test

import (
	"testing"

	pgrepository "github.com/go-kanna/orm/internal/gen/testdata/postgres/repository"
)

// pgsqlRepository is nil before Go 1.27, where database/sql cannot read an array into a slice through pgx/stdlib.
var pgsqlRepository func(t *testing.T, url string) pgrepository.OrderRepository

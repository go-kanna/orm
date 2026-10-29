//go:build !go1.27

package gen_test

import (
	"testing"

	"github.com/go-kanna/orm/internal/gen/testdata/repository"
)

// pgsqlRepository is nil before Go 1.27, where database/sql cannot read an array into a slice through pgx/stdlib.
var pgsqlRepository func(t *testing.T, url string) repository.OrderRepository

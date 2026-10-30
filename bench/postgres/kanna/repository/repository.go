// Package repository declares what the benchmarks read and write, which kanna-orm implements twice: through pgx in
// pgxstore, and through database/sql in sqlstore.
package repository

import (
	"context"

	"github.com/go-kanna/orm/bench/postgres/kanna/model"
	"github.com/go-kanna/orm/bench/postgres/kanna/scopes"
)

//go:generate go tool kanna-orm -destination ../pgxstore
//go:generate go tool kanna-orm -destination ../sqlstore -driver database/sql

//kanna:repository
type CustomerRepository interface {
	Insert(ctx context.Context, c model.Customer) (model.Customer, error)
	//kanna:conflict email keep=created_at
	UpsertByEmail(ctx context.Context, c model.Customer) (model.Customer, error)
}

//kanna:repository
type OrderRepository interface {
	Get(ctx context.Context, id int64) (model.Order, error)
	List(ctx context.Context, s ...scopes.Order) ([]model.Order, error)
	InsertLines(ctx context.Context, lines []model.OrderLine) ([]model.OrderLine, error)
}

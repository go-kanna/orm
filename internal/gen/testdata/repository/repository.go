// Package repository declares the repositories of the tests, which kanna-orm implements in pgxrepo.
package repository

import (
	"context"

	"github.com/go-kanna/orm/internal/gen/testdata/model"
	"github.com/go-kanna/orm/internal/gen/testdata/postgres"
)

//kanna:repository
type OrderRepository interface {
	Get(ctx context.Context, id model.OrderID, s ...postgres.Order) (model.Order, error)
	First(ctx context.Context, s ...postgres.Order) (model.Order, error)
	List(ctx context.Context, s ...postgres.Order) ([]model.Order, error)
	ListByCustomer(ctx context.Context, customerID int64, s ...postgres.Order) ([]model.Order, error)
	Count(ctx context.Context, s ...postgres.Order) (int64, error)
	Exists(ctx context.Context, s ...postgres.Order) (bool, error)
	Delete(ctx context.Context, id model.OrderID) error
	DeleteStale(ctx context.Context, s ...postgres.Order) (int64, error)
	GetLine(ctx context.Context, orderID model.OrderID, lineNo int) (model.OrderLine, error)
}

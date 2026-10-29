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

	Insert(ctx context.Context, o model.Order) (model.Order, error)
	InsertLine(ctx context.Context, line model.OrderLine) error
	InsertLines(ctx context.Context, lines []model.OrderLine) ([]model.OrderLine, error)
	InsertTags(ctx context.Context, tags []model.Tag) error
	Update(ctx context.Context, o model.Order) error
	UpsertCustomer(ctx context.Context, c model.Customer) (model.Customer, error)
	//kanna:conflict order keep=placed_at
	UpsertByPosition(ctx context.Context, o model.Order) (model.Order, error)
	UpsertLines(ctx context.Context, lines []model.OrderLine) error
	//kanna:conflict name
	UpsertTags(ctx context.Context, tags []model.Tag) ([]model.Tag, error)
}

// Package repository declares what the rest of the code reads and writes, which kanna-orm implements in store.
package repository

import (
	"context"
	"time"

	"github.com/go-kanna/orm/example/postgres/model"
	"github.com/go-kanna/orm/example/postgres/scopes"
)

//go:generate go tool kanna-orm -destination ../store

//kanna:repository
type CustomerRepository interface {
	// UpsertByEmail meets a customer by their email address, keeping when they joined.
	//kanna:conflict email keep=created_at
	UpsertByEmail(ctx context.Context, c model.Customer) (model.Customer, error)
	Get(ctx context.Context, id model.CustomerID) (model.Customer, error)
}

//kanna:repository
type ProductRepository interface {
	// UpsertAll writes the catalog in one statement, however many products it has.
	UpsertAll(ctx context.Context, products []model.Product) error
	List(ctx context.Context, s ...scopes.Product) ([]model.Product, error)
	InsertTags(ctx context.Context, tags []model.Tag) error
}

//kanna:repository
type OrderRepository interface {
	Get(ctx context.Context, id model.OrderID, s ...scopes.Order) (model.Order, error)
	List(ctx context.Context, s ...scopes.Order) ([]model.Order, error)
	Count(ctx context.Context, s ...scopes.Order) (int64, error)
	Insert(ctx context.Context, o model.Order) (model.Order, error)
	InsertLines(ctx context.Context, lines []model.OrderLine) error
	DeleteAll(ctx context.Context, s ...scopes.Order) (int64, error)

	Tag(ctx context.Context, orderID model.OrderID, tag string) error
	MonthlySales(ctx context.Context, since time.Time) ([]MonthSales, error)
}

// FulfillmentRepository takes an order from one status to the next.
//
//kanna:repository
type FulfillmentRepository interface {
	// GetForUpdate keeps the order as it is read until the transaction ends.
	//kanna:lock update
	GetForUpdate(ctx context.Context, id model.OrderID) (model.Order, error)
	// FirstToShip passes over the orders other workers hold, so that no two take the same one.
	//kanna:lock update skip-locked
	FirstToShip(ctx context.Context, s ...scopes.Order) (model.Order, error)
	InsertShipment(ctx context.Context, s model.Shipment) error
	Update(ctx context.Context, o model.Order) error
}

// MonthSales is what the orders of a month sold.
type MonthSales struct {
	Month   time.Time
	Orders  int64
	Revenue int64
}

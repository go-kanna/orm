// Package repository declares what the rest of the code reads and writes, which kanna-orm implements in store.
package repository

import (
	"context"
	"time"

	"github.com/go-kanna/orm/example/mysql/model"
	"github.com/go-kanna/orm/example/mysql/scopes"
)

//go:generate go tool kanna-orm -destination ../mysql

//kanna:repository
type CustomerRepository interface {
	// UpsertByEmail meets a customer by their email address, keeping when they joined.
	//kanna:conflict email keep=created_at coalesce=phone
	UpsertByEmail(ctx context.Context, c model.Customer) (model.Customer, error)
	Get(ctx context.Context, id model.CustomerID) (model.Customer, error)
	UpdatePhone(ctx context.Context, id model.CustomerID, c scopes.CustomerChangeSet, s ...scopes.Customer) error
}

//kanna:repository
type ProductRepository interface {
	// UpsertAll writes the catalog in one statement, however many products it has.
	//kanna:conflict sku
	UpsertAll(ctx context.Context, products []model.Product) error
	List(ctx context.Context, s ...scopes.Product) ([]model.Product, error)
	ListSummaries(ctx context.Context, s ...scopes.Product) ([]ProductSummary, error)
	//kanna:pluck name
	Names(ctx context.Context, s ...scopes.Product) ([]string, error)
	InsertTags(ctx context.Context, tags []model.Tag) error
}

//kanna:repository
type OrderRepository interface { //nolint:interfacebloat // demonstrates order reads and writes together
	Get(ctx context.Context, id model.OrderID, s ...scopes.Order) (model.Order, error)
	GetApprover(ctx context.Context, id model.OrderID) (OrderApprover, error)
	List(ctx context.Context, s ...scopes.Order) ([]model.Order, error)
	ListSummaries(ctx context.Context, s ...scopes.Order) ([]OrderSummary, error)
	Count(ctx context.Context, s ...scopes.Order) (int64, error)
	Insert(ctx context.Context, o model.Order) (model.Order, error)
	InsertLines(ctx context.Context, lines []model.OrderLine) error
	InsertApprovals(ctx context.Context, approvals []model.Approval) error
	Delete(ctx context.Context, s ...scopes.Order) (int64, error)

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

// OrderApprover is who approved the revision an order is at, nil until someone has.
type OrderApprover struct {
	OrderID  model.OrderID
	Approver *string
}

// MonthSales is what the orders of a month, as 2026-02, sold.
type MonthSales struct {
	Month   string
	Orders  int64
	Revenue int64
}

// ProductSummary is the part of a product shown in a price list.
type ProductSummary struct {
	Name  string
	Price int64
}

type OrderSummary struct {
	ID           model.OrderID
	CustomerName string `orm:"customers.name"`
	Status       model.OrderStatus
}

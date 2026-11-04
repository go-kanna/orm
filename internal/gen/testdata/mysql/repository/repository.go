// Package repository declares the repositories of the tests on MySQL, which kanna-orm implements in stdlib.
package repository

import (
	"context"
	"time"

	"github.com/go-kanna/orm/internal/gen/testdata/model"
	"github.com/go-kanna/orm/internal/gen/testdata/mysql/scopes"
)

//kanna:repository
type OrderRepository interface {
	Get(ctx context.Context, id model.OrderID, s ...scopes.Order) (model.Order, error)
	First(ctx context.Context, s ...scopes.Order) (model.Order, error)
	List(ctx context.Context, s ...scopes.Order) ([]model.Order, error)
	ListIDs(ctx context.Context, s ...scopes.Order) ([]model.OrderID, error)
	FindMemo(ctx context.Context, id model.OrderID) (**string, error)
	GetTagSummary(ctx context.Context, id model.OrderID, s ...scopes.Order) (model.OrderTagSummary, error)
	GetSummary(ctx context.Context, id model.OrderID, s ...scopes.Order) (model.OrderSummary, error)
	FindSummary(ctx context.Context, id model.OrderID, s ...scopes.Order) (*model.OrderSummary, error)
	ListSummaries(ctx context.Context, s ...scopes.Order) ([]model.OrderSummary, error)
	ListByCustomer(ctx context.Context, customerID int64, s ...scopes.Order) ([]model.Order, error)
	Count(ctx context.Context, s ...scopes.Order) (int64, error)
	Exists(ctx context.Context, s ...scopes.Order) (bool, error)
	Delete(ctx context.Context, id model.OrderID) error
	DeleteStale(ctx context.Context, s ...scopes.Order) (int64, error)
	DeleteLines(ctx context.Context, s ...scopes.OrderLine) (int64, error)
	DeleteTags(ctx context.Context, s ...scopes.Tag) (int64, error)
	GetLine(ctx context.Context, orderID model.OrderID, lineNo int) (model.OrderLine, error)

	//kanna:lock update
	GetForUpdate(ctx context.Context, id model.OrderID) (model.Order, error)
	//kanna:lock share
	GetForShare(ctx context.Context, id model.OrderID) (model.Order, error)
	//kanna:lock update nowait
	GetNoWait(ctx context.Context, id model.OrderID, s ...scopes.Order) (model.Order, error)
	//kanna:lock update skip-locked
	FirstUnlocked(ctx context.Context, s ...scopes.Order) (model.Order, error)
	//kanna:lock update skip-locked
	ListUnlocked(ctx context.Context, s ...scopes.Order) ([]model.Order, error)

	Insert(ctx context.Context, o model.Order) (model.Order, error)
	InsertOrders(ctx context.Context, orders []model.Order) error
	UpsertOrder(ctx context.Context, o model.Order) (model.Order, error)
	InsertLine(ctx context.Context, line model.OrderLine) (model.OrderLine, error)
	InsertLines(ctx context.Context, lines []model.OrderLine) error
	InsertTags(ctx context.Context, tags []model.Tag) error
	Update(ctx context.Context, o model.Order) error
	UpdateFieldsOnly(ctx context.Context, id model.OrderID, c scopes.OrderChangeSet, s ...scopes.Order) error
	UpdateMany(ctx context.Context, c scopes.OrderChangeSet, s ...scopes.Order) (int64, error)

	UpsertCustomer(ctx context.Context, c model.Customer) (model.Customer, error)
	//kanna:conflict order
	UpsertByPosition(ctx context.Context, o model.Order, c scopes.OrderChangeSet) (model.Order, error)
	UpsertLines(ctx context.Context, lines []model.OrderLine) error
	//kanna:conflict name
	UpsertTag(ctx context.Context, tag model.Tag) (model.Tag, error)

	MonthCounts(ctx context.Context, since time.Time) ([]MonthCount, error)
	ListWithCustomers(ctx context.Context, s ...scopes.Order) ([]OrderWithCustomer, error)
	FirstPlacedSince(ctx context.Context, since time.Time, s ...scopes.Order) (model.Order, error)
	TotalOf(ctx context.Context, customerID int64) (CustomerTotal, error)
	CloseOld(ctx context.Context, before time.Time) (int64, error)
	Reopen(ctx context.Context, id model.OrderID) error
	CustomersOf(ctx context.Context, ids []model.OrderID) ([]int64, error)
}

// OrderWithCustomer is an order with the name of its customer, which a query adds to it.
type OrderWithCustomer struct {
	model.Order
	CustomerName string
}

type MonthCount struct {
	Month time.Time
	Count int64
}

type CustomerTotal struct {
	Name  string `orm:"customer"`
	Total int64
}

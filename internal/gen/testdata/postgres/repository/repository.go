// Package repository declares the repositories of the tests on PostgreSQL, which kanna-orm implements in pgx and
// stdlib.
package repository

import (
	"context"
	"time"

	"github.com/go-kanna/orm/internal/gen/testdata/model"
	"github.com/go-kanna/orm/internal/gen/testdata/postgres/scopes"
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
	DeleteReturning(ctx context.Context, s ...scopes.Order) ([]model.Order, error)
	DeleteSummaries(ctx context.Context, s ...scopes.Order) ([]DeletedOrder, error)
	DeleteByCustomer(ctx context.Context, customerID int64, s ...scopes.Order) ([]DeletedOrder, error)
	DeleteLines(ctx context.Context, s ...scopes.OrderLine) (int64, error)
	DeleteTags(ctx context.Context, s ...scopes.Tag) (int64, error)
	GetLine(ctx context.Context, orderID model.OrderID, lineNo int) (model.OrderLine, error)

	//kanna:lock update
	GetForUpdate(ctx context.Context, id model.OrderID) (model.Order, error)
	//kanna:lock no-key-update
	GetForNoKeyUpdate(ctx context.Context, id model.OrderID) (model.Order, error)
	//kanna:lock share
	GetForShare(ctx context.Context, id model.OrderID) (model.Order, error)
	//kanna:lock key-share
	GetForKeyShare(ctx context.Context, id model.OrderID) (model.Order, error)
	//kanna:lock update nowait
	GetNoWait(ctx context.Context, id model.OrderID, s ...scopes.Order) (model.Order, error)
	//kanna:lock update skip-locked
	FirstUnlocked(ctx context.Context, s ...scopes.Order) (model.Order, error)
	//kanna:lock update skip-locked
	ListUnlocked(ctx context.Context, s ...scopes.Order) ([]model.Order, error)

	Insert(ctx context.Context, o model.Order) (model.Order, error)
	InsertLine(ctx context.Context, line model.OrderLine) error
	InsertLines(ctx context.Context, lines []model.OrderLine) ([]model.OrderLine, error)
	InsertTags(ctx context.Context, tags []model.Tag) error
	Update(ctx context.Context, o model.Order) error
	UpdateFieldsOnly(ctx context.Context, id model.OrderID, c scopes.OrderChangeSet, s ...scopes.Order) error
	UpdateMany(ctx context.Context, c scopes.OrderChangeSet, s ...scopes.Order) (int64, error)
	UpdateFields(ctx context.Context, id model.OrderID, c scopes.OrderChangeSet, s ...scopes.Order) (model.Order, error)
	UpdateManyReturning(ctx context.Context, c scopes.OrderChangeSet, s ...scopes.Order) ([]model.Order, error)
	UpdateScoped(ctx context.Context, o model.Order, s ...scopes.Order) (model.Order, error)
	UpdateLineFields(ctx context.Context, orderID model.OrderID, lineNo int, c scopes.OrderLineChangeSet) (model.OrderLine, error)

	UpsertCustomer(ctx context.Context, c model.Customer) (model.Customer, error)
	//kanna:conflict order keep=placed_at
	UpsertByPosition(ctx context.Context, o model.Order) (model.Order, error)
	UpsertLines(ctx context.Context, lines []model.OrderLine) error
	//kanna:conflict name
	UpsertTags(ctx context.Context, tags []model.Tag) ([]model.Tag, error)
	GetLabel(ctx context.Context, orderID model.OrderID) (model.Label, error)
	UpsertLabel(ctx context.Context, l model.Label) (model.Label, error)

	MonthCounts(ctx context.Context, since time.Time) ([]MonthCount, error)
	TotalOf(ctx context.Context, customerID int64) (CustomerTotal, error)
	CloseOld(ctx context.Context, before time.Time) (int64, error)
	Reopen(ctx context.Context, id model.OrderID) error
}

type DeletedOrder struct {
	ID   model.OrderID
	Memo *string
}

type MonthCount struct {
	Month time.Time
	Count int64
}

type CustomerTotal struct {
	Name  string `orm:"customer"`
	Total int64
}

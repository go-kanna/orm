// Package repository declares the repositories of the tests, which kanna-orm implements in pgxrepo and pgsqlrepo.
package repository

import (
	"context"
	"time"

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

	//kanna:lock update
	GetForUpdate(ctx context.Context, id model.OrderID) (model.Order, error)
	//kanna:lock no-key-update
	GetForNoKeyUpdate(ctx context.Context, id model.OrderID) (model.Order, error)
	//kanna:lock share
	GetForShare(ctx context.Context, id model.OrderID) (model.Order, error)
	//kanna:lock key-share
	GetForKeyShare(ctx context.Context, id model.OrderID) (model.Order, error)
	//kanna:lock update nowait
	GetNoWait(ctx context.Context, id model.OrderID, s ...postgres.Order) (model.Order, error)
	//kanna:lock update skip-locked
	FirstUnlocked(ctx context.Context, s ...postgres.Order) (model.Order, error)
	//kanna:lock update skip-locked
	ListUnlocked(ctx context.Context, s ...postgres.Order) ([]model.Order, error)

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
	GetLabel(ctx context.Context, orderID model.OrderID) (model.Label, error)
	UpsertLabel(ctx context.Context, l model.Label) (model.Label, error)

	MonthCounts(ctx context.Context, since time.Time) ([]MonthCount, error)
	TotalOf(ctx context.Context, customerID int64) (CustomerTotal, error)
	CloseOld(ctx context.Context, before time.Time) (int64, error)
	Reopen(ctx context.Context, id model.OrderID) error
}

type MonthCount struct {
	Month time.Time
	Count int64
}

type CustomerTotal struct {
	Name  string `orm:"customer"`
	Total int64
}

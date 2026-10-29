// Package mysqlrepository declares the repositories of the tests on MySQL, which kanna-orm implements in mysqlrepo.
package mysqlrepository

import (
	"context"
	"time"

	"github.com/go-kanna/orm/internal/gen/testdata/model"
	"github.com/go-kanna/orm/internal/gen/testdata/mysql"
)

//kanna:repository
type OrderRepository interface {
	Get(ctx context.Context, id model.OrderID, s ...mysql.Order) (model.Order, error)
	First(ctx context.Context, s ...mysql.Order) (model.Order, error)
	List(ctx context.Context, s ...mysql.Order) ([]model.Order, error)
	ListByCustomer(ctx context.Context, customerID int64, s ...mysql.Order) ([]model.Order, error)
	Count(ctx context.Context, s ...mysql.Order) (int64, error)
	Exists(ctx context.Context, s ...mysql.Order) (bool, error)
	Delete(ctx context.Context, id model.OrderID) error
	DeleteStale(ctx context.Context, s ...mysql.Order) (int64, error)
	GetLine(ctx context.Context, orderID model.OrderID, lineNo int) (model.OrderLine, error)

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

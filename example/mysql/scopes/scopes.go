// Package scopes holds the scope types the repositories read through, one for each table.
package scopes

import (
	"github.com/go-kanna/orm"

	"github.com/go-kanna/orm/example/mysql/model"
)

//go:generate go tool kanna-orm

//kanna:scope table=customers
type Customer struct{ q orm.Of[model.Customer] }

//kanna:scope table=products
type Product struct{ q orm.Of[model.Product] }

//kanna:scope table=orders
type Order struct{ q orm.Of[model.Order] }

//kanna:scope table=order_lines
type OrderLine struct{ q orm.Of[model.OrderLine] }

//kanna:scope table=shipments
type Shipment struct{ q orm.Of[model.Shipment] }

//kanna:scope table=tags
type Tag struct{ q orm.Of[model.Tag] }

// Open narrows to the orders neither shipped nor canceled.
func (s Order) Open() Order {
	return s.StatusEq(model.OrderOpen)
}

// Latest orders the orders latest first.
func (s Order) Latest() Order {
	return s.PlacedAtDesc().IDDesc()
}

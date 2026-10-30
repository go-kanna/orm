// Package model is the domain of the example: customers, the products they order, and their orders.
package model

import "time"

type (
	CustomerID  int64
	OrderID     int64
	SKU         string
	OrderStatus string
)

const (
	OrderOpen     OrderStatus = "open"
	OrderShipped  OrderStatus = "shipped"
	OrderCanceled OrderStatus = "canceled"
)

type Customer struct {
	ID        CustomerID
	Email     string
	Name      string
	CreatedAt time.Time
}

type Product struct {
	SKU   SKU
	Name  string
	Price int64
}

type Order struct {
	ID         OrderID
	CustomerID CustomerID
	Status     OrderStatus
	PlacedAt   time.Time
	Memo       *string `orm:"note"`

	Customer *Customer   // belongs to, by customer_id
	Lines    []OrderLine // has many, by order_lines.order_id
	Shipment *Shipment   // has one, by shipments.order_id, its primary key
	Tags     []Tag       // many to many, through order_tags
}

type OrderLine struct {
	OrderID  OrderID
	LineNo   int
	SKU      SKU
	Quantity int

	Product *Product `orm:"fk=sku"`
}

type Shipment struct {
	OrderID  OrderID
	Carrier  string
	Tracking string
}

type Tag struct{ Name string }

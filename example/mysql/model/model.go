// Package model is the domain of the example: customers, the products they order, and their orders.
package model

import (
	"errors"
	"time"
)

var ErrOrderNotFound = errors.New("order not found")

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
	Phone     *string
	CreatedAt time.Time
}

type Product struct {
	SKU      SKU
	Name     string
	Price    int64
	Currency string `orm:",default"`
}

type Order struct {
	Approver   *string `orm:"relation=approvals"`
	Approvals  []Approval
	ID         OrderID
	CustomerID CustomerID
	Status     OrderStatus
	PlacedAt   time.Time
	Memo       string `orm:"note,nullzero"`

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

// Approval records the reviewer of one revision of an order.
type Approval struct {
	ID         int64 `orm:",default"`
	OrderID    OrderID
	Revision   int32
	Approver   string
	ApprovedAt time.Time
	RevokedAt  *time.Time
}

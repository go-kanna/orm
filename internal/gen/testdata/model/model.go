// Package model declares the models of the scope types the tests generate for.
package model

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"time"
)

type (
	OrderID     int64
	OrderStatus string
)

type Order struct {
	ID         OrderID
	CustomerID int64
	Status     OrderStatus
	PlacedAt   time.Time
	Memo       *string `orm:"note"`
	Total      int64
	Paid       bool
	Contact    *Email
	Receipt    []byte
	Position   int `orm:"order"`
	Customer   *Customer
	Lines      []OrderLine
	Tags       []Tag
}

type Customer struct {
	ID       int64
	Name     string
	Nickname *string
}

// OrderSummary is an order with the name of its customer, which an INNER JOIN of customers reads.
type OrderSummary struct {
	ID       OrderID
	Total    int64
	Memo     *string
	Customer struct{ Name string }
}

// OrderContact is an order with its customer, if there is one, which a LEFT JOIN of customers reads.
type OrderContact struct {
	ID       OrderID
	Customer *struct {
		Name     string
		Nickname *string
	}
}

type OrderLine struct {
	OrderID OrderID
	LineNo  int
	SKU     string
	Order   *Order
	Notes   []LineNote `orm:"fk=order_id,line_no"`
	Tags    []LineTag  `orm:"fk=order_id,line_no"`
}

// LineNote is a note on an order line, which it refers to by both columns of its key.
type LineNote struct {
	OrderID OrderID
	LineNo  int
	Body    string
	Line    *OrderLine `orm:"fk=order_id,line_no"`
}

// LineTag tags an order line. It is the table between order_lines and tags, which a many to many does not go through,
// as it refers to an order line by both columns of its key.
type LineTag struct {
	OrderID OrderID
	LineNo  int
	TagID   string
	Tag     *Tag
}

type Tag struct{ Name string }

// Label holds the labels of an order in an array, which PostgreSQL alone of the databases has.
type Label struct {
	OrderID OrderID
	Names   []string
}

// Email is a value type, as kanna-value generates one.
type Email struct{ s string }

func NewEmail(s string) (Email, error) { return Email{s}, nil }

func (x Email) String() string { return x.s }

func (x Email) Value() (driver.Value, error) { return x.s, nil }

func (x *Email) Scan(src any) error {
	var v sql.Null[string]
	if err := v.Scan(src); err != nil {
		return err
	}
	if !v.Valid {
		return errors.New("model: cannot scan NULL into Email")
	}
	x.s = v.V
	return nil
}

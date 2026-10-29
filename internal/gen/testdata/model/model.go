// Package model declares the models of the scope types the tests generate for.
package model

import (
	"database/sql/driver"
	"fmt"
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
	Labels     []string
	Receipt    []byte
	Position   int `orm:"order"`
	Customer   *Customer
	Lines      []OrderLine
	Tags       []Tag
}

type Customer struct {
	ID   int64
	Name string
}

type OrderLine struct {
	OrderID OrderID
	LineNo  int
	SKU     string
	Order   *Order
}

type Tag struct{ Name string }

// Email is a value type, as kanna-value generates one.
type Email struct{ s string }

func NewEmail(s string) (Email, error) { return Email{s}, nil }

func (x Email) String() string { return x.s }

func (x Email) Value() (driver.Value, error) { return x.s, nil }

func (x *Email) Scan(src any) error {
	s, ok := src.(string)
	if !ok {
		return fmt.Errorf("scan %T into an Email", src)
	}
	x.s = s
	return nil
}

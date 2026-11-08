// Package model holds the rows the benchmarks read and write through kanna-orm.
package model

import "time"

type Customer struct {
	ID        int64
	Email     string
	Name      string
	CreatedAt time.Time
}

type Order struct {
	ID         int64
	CustomerID int64
	Status     string
	PlacedAt   time.Time
	Note       *string

	Lines []OrderLine
}

type OrderLine struct {
	ID       int64
	OrderID  int64
	SKU      string
	Quantity int32
}

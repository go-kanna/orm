// Package scopes holds the scope types the repositories read through, one for each table, which kanna-orm declares
// of the models.
package scopes

import "github.com/go-kanna/orm/example/mysql/model"

//go:generate go tool kanna-orm

//kanna:scopes github.com/go-kanna/orm/example/mysql/model

// Open narrows to the orders neither shipped nor canceled.
func (s Order) Open() Order {
	return s.StatusEq(model.OrderOpen)
}

// Latest orders the orders latest first.
func (s Order) Latest() Order {
	return s.PlacedAtDesc().IDDesc()
}

// Package scopes holds the scope types of the tests on PostgreSQL.
package scopes

import (
	"github.com/go-kanna/orm/internal/gen/testdata/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=orders
type Order struct{ q scope.Of[model.Order] }

//kanna:scope table=customers
type Customer struct{ q scope.Of[model.Customer] }

//kanna:scope table=order_lines
type OrderLine struct{ q scope.Of[model.OrderLine] }

//kanna:scope table=tags
type Tag struct{ q scope.Of[model.Tag] }

//kanna:scope table=line_notes
type LineNote struct{ q scope.Of[model.LineNote] }

//kanna:scope table=labels
type Label struct{ q scope.Of[model.Label] }

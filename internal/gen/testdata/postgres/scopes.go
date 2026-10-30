// Package postgres holds scope types, generated for PostgreSQL.
package postgres

import (
	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
)

//kanna:scope table=orders
type Order struct{ q orm.Of[model.Order] }

//kanna:scope table=customers
type Customer struct{ q orm.Of[model.Customer] }

//kanna:scope table=order_lines
type OrderLine struct{ q orm.Of[model.OrderLine] }

//kanna:scope table=tags
type Tag struct{ q orm.Of[model.Tag] }

//kanna:scope table=line_notes
type LineNote struct{ q orm.Of[model.LineNote] }

//kanna:scope table=labels
type Label struct{ q orm.Of[model.Label] }

// Package postgres holds a scope type, generated for PostgreSQL.
package postgres

import (
	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
)

//kanna:scope table=orders
type Order struct{ q orm.Of[model.Order] }

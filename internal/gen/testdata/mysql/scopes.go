// Package mysql holds a scope type, generated for MySQL.
package mysql

import (
	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
)

//kanna:scope table=orders
type Order struct{ q orm.Of[model.Order] }

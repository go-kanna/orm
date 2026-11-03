package mysql

import (
	"errors"

	"github.com/go-kanna/orm"

	"github.com/go-kanna/orm/example/mysql/model"
)

// mapError says in the words of the model that an order is not there.
func (Order) mapError(err error) error {
	if errors.Is(err, orm.ErrNotFound) {
		return model.ErrOrderNotFound
	}
	return err
}

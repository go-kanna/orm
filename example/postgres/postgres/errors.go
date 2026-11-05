package postgres

import (
	"errors"

	"github.com/go-kanna/orm"

	"github.com/go-kanna/orm/example/postgres/model"
)

// mapError says in the words of the model that the order Get reads is not there.
func (Order) mapError(op orderOperation, err error) error {
	if op == orderOperationGet && errors.Is(err, orm.ErrNotFound) {
		return model.ErrOrderNotFound
	}
	return err
}

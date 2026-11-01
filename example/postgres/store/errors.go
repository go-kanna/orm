package store

import (
	"errors"

	"github.com/go-kanna/orm"

	"github.com/go-kanna/orm/example/postgres/model"
)

func translateOrderError(op orderRepositoryOperation, err error) error {
	if op == orderRepositoryOperationGet && errors.Is(err, orm.ErrNotFound) {
		return model.ErrOrderNotFound
	}
	return err
}

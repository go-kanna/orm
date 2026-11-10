package pgx_test

import (
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	kpgx "github.com/go-kanna/orm/pgx"
)

// What pgx connects by copies rows.
var (
	_ kpgx.CopyDBTX = (*pgxpool.Pool)(nil)
	_ kpgx.CopyDBTX = (*pgx.Conn)(nil)
	_ kpgx.CopyDBTX = pgx.Tx(nil)
)

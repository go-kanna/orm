package pgx_test

import (
	"context"
	"testing"

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

func TestCopyFromSQL(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		table   pgx.Identifier
		columns []string
		want    string
	}{
		{pgx.Identifier{"orders"}, []string{"customer_id", "status"}, `COPY "orders" ("customer_id", "status") FROM STDIN`},
		{pgx.Identifier{"shop", "order"}, []string{`say "hi"`}, `COPY "shop"."order" ("say ""hi""") FROM STDIN`},
	} {
		if got := kpgx.CopyFromSQL(tt.table, tt.columns); got != tt.want {
			t.Errorf("CopyFromSQL(%v, %v) = %s, want %s", tt.table, tt.columns, got, tt.want)
		}
	}
}

type conn struct{ kpgx.DBTX }

type tx struct{ conn }

func (tx) Commit(context.Context) error { return nil }

// wrapper wraps a DBTX, which its embedding alone does not tell of a Commit.
type wrapper struct{ kpgx.DBTX }

func (w wrapper) Unwrap() kpgx.DBTX { return w.DBTX }

func TestInTransaction(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		db   kpgx.DBTX
		want bool
	}{
		"a transaction":                       {tx{}, true},
		"a connection":                        {conn{}, false},
		"a wrapper of a transaction":          {wrapper{tx{}}, true},
		"a wrapper of a wrapper of one":       {wrapper{wrapper{tx{}}}, true},
		"a wrapper of a connection":           {wrapper{conn{}}, false},
		"a transaction embedded with no word": {struct{ kpgx.DBTX }{tx{}}, false},
	} {
		if got := kpgx.InTransaction(tt.db); got != tt.want {
			t.Errorf("InTransaction(%s) = %t, want %t", name, got, tt.want)
		}
	}
}

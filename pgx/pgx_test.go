package pgx_test

import (
	"context"
	"testing"

	ormpgx "github.com/go-kanna/orm/pgx"
)

type conn struct{ ormpgx.DBTX }

type tx struct{ conn }

func (tx) Commit(context.Context) error { return nil }

// wrapper wraps a DBTX, which its embedding alone does not tell of a Commit.
type wrapper struct{ ormpgx.DBTX }

func (w wrapper) Unwrap() ormpgx.DBTX { return w.DBTX }

func TestInTransaction(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		db   ormpgx.DBTX
		want bool
	}{
		"a transaction":                       {tx{}, true},
		"a connection":                        {conn{}, false},
		"a wrapper of a transaction":          {wrapper{tx{}}, true},
		"a wrapper of a wrapper of one":       {wrapper{wrapper{tx{}}}, true},
		"a wrapper of a connection":           {wrapper{conn{}}, false},
		"a transaction embedded with no word": {struct{ ormpgx.DBTX }{tx{}}, false},
	} {
		if got := ormpgx.InTransaction(tt.db); got != tt.want {
			t.Errorf("InTransaction(%s) = %t, want %t", name, got, tt.want)
		}
	}
}

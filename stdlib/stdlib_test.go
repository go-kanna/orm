package stdlib_test

import (
	"testing"

	"github.com/go-kanna/orm/stdlib"
)

type conn struct{ stdlib.DBTX }

type tx struct{ conn }

func (tx) Commit() error { return nil }

// wrapper wraps a DBTX, which its embedding alone does not tell of a Commit.
type wrapper struct{ stdlib.DBTX }

func (w wrapper) Unwrap() stdlib.DBTX { return w.DBTX }

func TestInTransaction(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		db   stdlib.DBTX
		want bool
	}{
		"a transaction":                       {tx{}, true},
		"a connection":                        {conn{}, false},
		"a wrapper of a transaction":          {wrapper{tx{}}, true},
		"a wrapper of a wrapper of one":       {wrapper{wrapper{tx{}}}, true},
		"a wrapper of a connection":           {wrapper{conn{}}, false},
		"a transaction embedded with no word": {struct{ stdlib.DBTX }{tx{}}, false},
	} {
		if got := stdlib.InTransaction(tt.db); got != tt.want {
			t.Errorf("InTransaction(%s) = %t, want %t", name, got, tt.want)
		}
	}
}

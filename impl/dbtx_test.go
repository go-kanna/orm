package impl_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/go-kanna/orm/impl"
	kpgx "github.com/go-kanna/orm/pgx"
	"github.com/go-kanna/orm/stdlib"
)

type pgxConn struct{ kpgx.DBTX }

type pgxTx struct{ pgxConn }

func (pgxTx) Commit(context.Context) error { return nil }

// pgxWrapper wraps a DBTX, which its embedding alone does not tell of a Commit.
type pgxWrapper struct{ kpgx.DBTX }

func (w pgxWrapper) Unwrap() kpgx.DBTX { return w.DBTX }

type sqlConn struct{ stdlib.DBTX }

type sqlTx struct{ sqlConn }

func (sqlTx) Commit() error { return nil }

type sqlWrapper struct{ stdlib.DBTX }

func (w sqlWrapper) Unwrap() stdlib.DBTX { return w.DBTX }

func TestInTransaction(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		got  bool
		want bool
	}{
		"a transaction of pgx":                {impl.InTransaction[kpgx.DBTX](pgxTx{}), true},
		"a connection of pgx":                 {impl.InTransaction[kpgx.DBTX](pgxConn{}), false},
		"a wrapper of a transaction":          {impl.InTransaction[kpgx.DBTX](pgxWrapper{pgxTx{}}), true},
		"a wrapper of a wrapper of one":       {impl.InTransaction[kpgx.DBTX](pgxWrapper{pgxWrapper{pgxTx{}}}), true},
		"a wrapper of a connection":           {impl.InTransaction[kpgx.DBTX](pgxWrapper{pgxConn{}}), false},
		"a transaction embedded with no word": {impl.InTransaction[kpgx.DBTX](struct{ kpgx.DBTX }{pgxTx{}}), false},
		"a transaction of database/sql":       {impl.InTransaction[stdlib.DBTX](sqlTx{}), true},
		"a connection of database/sql":        {impl.InTransaction[stdlib.DBTX](sqlConn{}), false},
		"a wrapper of one of database/sql":    {impl.InTransaction[stdlib.DBTX](sqlWrapper{sqlWrapper{sqlTx{}}}), true},
		"a wrapper of a connection of it":     {impl.InTransaction[stdlib.DBTX](sqlWrapper{sqlConn{}}), false},
	} {
		if tt.got != tt.want {
			t.Errorf("InTransaction(%s) = %t, want %t", name, tt.got, tt.want)
		}
	}
}

func TestCopyFromSQL(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		table   pgx.Identifier
		columns []string
		want    string
	}{
		{pgx.Identifier{"orders"}, []string{"customer_id", "status"}, `COPY "orders" ("customer_id", "status") FROM STDIN`},
		{pgx.Identifier{"shop", "order"}, []string{`say "hi"`}, `COPY "shop"."order" ("say ""hi""") FROM STDIN`},
		{pgx.Identifier{"a\x00b"}, []string{"c"}, `COPY "ab" ("c") FROM STDIN`},
	} {
		if got := impl.CopyFromSQL(tt.table, tt.columns); got != tt.want {
			t.Errorf("CopyFromSQL(%v, %v) = %s, want %s", tt.table, tt.columns, got, tt.want)
		}
		if want := "COPY " + tt.table.Sanitize(); !strings.HasPrefix(impl.CopyFromSQL(tt.table, tt.columns), want+" ") {
			t.Errorf("CopyFromSQL(%v, ...) quotes the table otherwise than %s", tt.table, want)
		}
	}
}

func TestMySQLScanError(t *testing.T) {
	t.Parallel()

	withoutParseTime := errors.New(`sql: Scan error on column index 3, name "placed_at": unsupported Scan, ` +
		"storing driver.Value type []uint8 into type *time.Time")
	if err := impl.MySQLScanError(withoutParseTime); !errors.Is(err, withoutParseTime) ||
		!strings.Contains(err.Error(), "parseTime=true") {
		t.Errorf("MySQLScanError(%v) = %v, want it told of parseTime=true", withoutParseTime, err)
	}
	other := errors.New("sql: no rows in result set")
	if err := impl.MySQLScanError(other); err != other { //nolint:errorlint // the very error, unchanged
		t.Errorf("MySQLScanError(%v) = %v, want it as it is", other, err)
	}
	if err := impl.MySQLScanError(nil); err != nil {
		t.Errorf("MySQLScanError(nil) = %v", err)
	}
}

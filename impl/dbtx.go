package impl

import (
	"context"
	"fmt"
	"strings"
)

// InTransaction reports whether db, the DBTX of orm/pgx or orm/stdlib, is a transaction, which has a Commit, or wraps
// one, as an Unwrap returning the D it wraps tells, however many wrappers deep: what a lock needs to last beyond its
// statement.
func InTransaction[D any](db D) bool {
	for {
		switch any(db).(type) {
		case pgxTx, sqlTx:
			return true
		}
		u, ok := any(db).(interface{ Unwrap() D })
		if !ok {
			return false
		}
		db = u.Unwrap()
	}
}

// pgxTx and sqlTx are transactions of pgx and of database/sql.
type (
	pgxTx interface {
		Commit(ctx context.Context) error
	}
	sqlTx interface{ Commit() error }
)

// CopyFromSQL is the statement the CopyFrom of pgx runs to copy rows into the columns columnNames of tableName, a
// pgx.Identifier, as the hooks of a repository see it: COPY "orders" ("customer_id", "status") FROM STDIN. It quotes
// the names as pgx.Identifier does.
func CopyFromSQL(tableName, columnNames []string) string {
	return "COPY " + pgxIdentifiers(tableName, ".") + " (" + pgxIdentifiers(columnNames, ", ") + ") FROM STDIN"
}

func pgxIdentifiers(names []string, sep string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = `"` + strings.ReplaceAll(strings.ReplaceAll(name, "\x00", ""), `"`, `""`) + `"`
	}
	return strings.Join(quoted, sep)
}

// MySQLScanError returns err, an error of a Scan of a row of MySQL, told that the DSN of go-sql-driver/mysql needs
// parseTime=true when it is what the driver returns without it: a DATETIME or a TIMESTAMP, read as bytes, that a
// time.Time cannot hold. database/sql tells it by its message alone.
func MySQLScanError(err error) error {
	if err == nil || !strings.Contains(err.Error(), "storing driver.Value type []uint8 into type *time.Time") {
		return err
	}
	return fmt.Errorf("%w (a time.Time reads a DATETIME or a TIMESTAMP when the DSN of go-sql-driver/mysql has "+
		"parseTime=true)", err)
}

package describe

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
)

// errNoURL tells of a database URL that is no URL, as a DSN of go-sql-driver/mysql or a connection string of keywords
// is, without quoting it, as it may hold a password.
var errNoURL = errors.New("the database URL is not a URL, as postgres://user:password@host:5432/database and " +
	"mysql://user:password@host:3306/database are")

// Open connects to the database rawURL names: one at postgres:// or postgresql:// through pgx, and one at mysql://
// through go-sql-driver/mysql. The func returned closes the connection.
func Open(ctx context.Context, rawURL string) (Database, func() error, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		// The error of url.Parse quotes the URL, password and all.
		if e, ok := errors.AsType[*url.Error](err); ok {
			err = e.Err
		}
		return nil, nil, fmt.Errorf("read the database URL: %w", err)
	}

	switch u.Scheme {
	case "postgres", "postgresql":
		conn, err := pgx.Connect(ctx, rawURL)
		if err != nil {
			return nil, nil, fmt.Errorf("connect: %w", err)
		}
		return Postgres(conn), func() error { return conn.Close(context.WithoutCancel(ctx)) }, nil
	case "mysql":
		dsn, err := mysqlDSN(u)
		if err != nil {
			return nil, nil, err
		}
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			return nil, nil, fmt.Errorf("connect: %w", err)
		}
		conn, err := db.Conn(ctx)
		if err != nil {
			return nil, nil, errors.Join(fmt.Errorf("connect: %w", err), db.Close())
		}
		return MySQL(conn), func() error { return errors.Join(conn.Close(), db.Close()) }, nil
	case "":
		return nil, nil, errNoURL
	default:
		// The DSN of go-sql-driver/mysql reads as a URL of its user name, as user:password@tcp(host:3306)/database
		// does, with no // after the scheme.
		if u.Opaque != "" {
			return nil, nil, errNoURL
		}
		return nil, nil, fmt.Errorf("the database URL starts %q, but kanna-orm knows postgres:// and mysql://",
			u.Scheme+"://")
	}
}

// mysqlDSN turns mysql://user:password@host:port/database?options into the DSN go-sql-driver/mysql reads.
func mysqlDSN(u *url.URL) (string, error) {
	cfg := mysql.NewConfig()
	if u.User != nil {
		cfg.User = u.User.Username()
		cfg.Passwd, _ = u.User.Password()
	}
	cfg.Net = "tcp"
	cfg.Addr = u.Host
	if u.Port() == "" {
		cfg.Addr = net.JoinHostPort(u.Hostname(), "3306")
	}
	cfg.DBName = strings.TrimPrefix(u.Path, "/")
	if cfg.DBName == "" {
		return "", errors.New("the database URL names no database")
	}

	dsn := cfg.FormatDSN()
	if u.RawQuery != "" {
		dsn += "?" + u.RawQuery
	}
	if _, err := mysql.ParseDSN(dsn); err != nil {
		return "", fmt.Errorf("read the options of the database URL: %w", err)
	}
	return dsn, nil
}

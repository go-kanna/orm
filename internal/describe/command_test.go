package describe_test

import (
	"bytes"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"
	"github.com/go-kanna/internal/packages"
	gomysql "github.com/go-sql-driver/mysql"

	"github.com/go-kanna/orm/internal/describe"
	"github.com/go-kanna/orm/internal/snapshot"
)

// exec runs kanna-orm describe in dir, with an environment holding nothing but env.
func exec(t *testing.T, dir string, env map[string]string, args ...string) (int, string, string) {
	t.Helper()

	var out, errs bytes.Buffer
	code := describe.Exec(cli.Env{Out: &out, Err: &errs, Dir: dir, Getenv: func(k string) string { return env[k] }}, args)
	return code, out.String(), errs.String()
}

// shopSQL writes its query in ledger, which holds no package yet, as the implementations of repositories do not before
// their first run.
const shopSQL = shop + `
-- scopes/order_scopes.sql --
-- scope: Order.PlacedSince(at time.Time)
placed_at >= :at
-- ledger/order_queries.sql --
-- query: CountOpen
SELECT count(*) AS n FROM orders WHERE status = :status
`

func TestExec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setUp func(t *testing.T) (url string, addColumn func())
		added string
	}{
		{"PostgreSQL", func(t *testing.T) (string, func()) {
			t.Helper()
			conn, schema := postgres(t)
			postgresExec(t, conn, `CREATE TABLE orders (
	id bigint PRIMARY KEY, status text NOT NULL, placed_at timestamptz NOT NULL
)`)
			return os.Getenv("KANNA_ORM_TEST_POSTGRES") + "&search_path=" + schema,
				func() { postgresExec(t, conn, "ALTER TABLE orders ADD COLUMN memo text") }
		}, "orders.memo (text)"},
		{"MySQL", func(t *testing.T) (string, func()) {
			t.Helper()
			conn, name := mysql(t)
			mysqlExec(t, conn, `CREATE TABLE orders (
	id BIGINT PRIMARY KEY, status TEXT NOT NULL, placed_at DATETIME(6) NOT NULL
)`)
			cfg, err := gomysql.ParseDSN(os.Getenv("KANNA_ORM_TEST_MYSQL"))
			if err != nil {
				t.Fatal(err)
			}
			u := url.URL{Scheme: "mysql", User: url.UserPassword(cfg.User, cfg.Passwd), Host: cfg.Addr, Path: "/" + name}
			return u.String(), func() { mysqlExec(t, conn, "ALTER TABLE orders ADD COLUMN memo TEXT") }
		}, "orders.memo (text)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dbURL, addColumn := tt.setUp(t)
			dir := writeModule(t, shopSQL)
			env := map[string]string{"DATABASE_URL": dbURL}

			if code, out, errs := exec(t, dir, env, "-v", "./..."); code != 0 || out != "wrote ./kanna-orm.snapshot.json\n" {
				t.Fatalf("describe = %d, %q, %q", code, out, errs)
			}
			data, err := fs.ReadFile(os.DirFS(dir), "kanna-orm.snapshot.json")
			if err != nil {
				t.Fatal(err)
			}
			s, err := snapshot.Unmarshal(data)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"example.com/shop/ledger.CountOpen", "example.com/shop/scopes.Order.PlacedSince"} {
				if _, ok := s.Statements[key]; !ok {
					t.Errorf("the snapshot lacks %s:\n%s", key, data)
				}
			}

			if code, out, errs := exec(t, dir, env, "-v", "./..."); code != 0 || out != "unchanged ./kanna-orm.snapshot.json\n" {
				t.Errorf("describe again = %d, %q, %q", code, out, errs)
			}
			if code, out, errs := exec(t, dir, env, "-check", "./..."); code != 0 || out != "" || errs != "" {
				t.Errorf("describe -check = %d, %q, %q", code, out, errs)
			}

			addColumn()
			want := "./kanna-orm.snapshot.json: error: out of date with the database\n" +
				"  " + tt.added + " is in the database but not in the snapshot\n" +
				"  run go tool kanna-orm describe to update it\n"
			if code, _, errs := exec(t, dir, env, "-check", "./..."); code != 1 || errs != want {
				t.Errorf("describe -check after a column is added = %d,\n%s\nwant 1,\n%s", code, errs, want)
			}
		})
	}
}

func TestSQLDirs(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, shop+`
-- scopes/order_scopes.sql --
-- scope: Order.Open()
status <> 'closed'
-- ledger/order_queries.sql --
-- query: CountOpen
SELECT 1
-- ledger/archive/order_queries.sql --
-- query: CountArchived
SELECT 1
-- notes/notes.txt --
no .sql file here
-- _old/order_queries.sql --
-- query: CountOpen
SELECT 1
-- testdata/order_queries.sql --
-- query: CountOpen
SELECT 1
-- tools/go.mod --
module example.com/tools
-- tools/order_queries.sql --
-- query: CountOpen
SELECT 1
`)
	set, err := packages.Load([]string{"./..."}, packages.Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		patterns []string
		want     []string
	}{
		{[]string{"./..."}, []string{"ledger", "ledger/archive"}},
		{[]string{"./ledger"}, []string{"ledger"}},
		{[]string{"./ledger/..."}, []string{"ledger", "ledger/archive"}},
		{[]string{filepath.Join(dir, "ledger")}, []string{"ledger"}},
		{[]string{"./scopes"}, nil}, // a package, whose .sql files describe reads anyway
		{[]string{"./missing/..."}, nil},
		{[]string{"example.com/shop/..."}, nil}, // an import path, which names no directory
	}
	for _, tt := range tests {
		got, err := describe.SQLDirs(dir, tt.patterns, set.Packages)
		if err != nil {
			t.Errorf("SQLDirs(%v): %v", tt.patterns, err)
			continue
		}
		var rel []string
		for _, d := range got {
			r, err := filepath.Rel(dir, d)
			if err != nil {
				t.Fatal(err)
			}
			rel = append(rel, filepath.ToSlash(r))
		}
		if !slices.Equal(rel, tt.want) {
			t.Errorf("SQLDirs(%v) = %v, want %v", tt.patterns, rel, tt.want)
		}
	}
}

func TestExec_Usage(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, shop)
	tests := []struct {
		name      string
		dir       string
		env       map[string]string
		args      []string
		code      int
		out, errs string
	}{
		{"help", dir, nil, []string{"help"}, 0, "Usage:\n  kanna-orm describe [flags] [packages]", ""},
		{"a flag it does not know", dir, nil, []string{"-dsn", "x"}, 2, "", "flag provided but not defined: -dsn"},
		{"no database", dir, nil, nil, 2, "",
			"kanna-orm describe: no database to describe: give -database-url, or set DATABASE_URL"},
		{"the database of DATABASE_URL", dir, map[string]string{"DATABASE_URL": "sqlite://shop.db"}, nil, 1, "",
			`error: the database URL starts "sqlite://"`},
		{"-database-url over DATABASE_URL", dir, map[string]string{"DATABASE_URL": "sqlite://shop.db"},
			[]string{"-database-url", "oracle://shop"}, 1, "", `error: the database URL starts "oracle://"`},
		{"no module", t.TempDir(), nil, []string{"-database-url", "postgres://localhost/shop"}, 2, "",
			"name the snapshot with -snapshot"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, out, errs := exec(t, tt.dir, tt.env, tt.args...)
			if code != tt.code || !strings.Contains(out, tt.out) || !strings.Contains(errs, tt.errs) {
				t.Errorf("describe %v = %d,\n%s\n%s\nwant %d with %q and %q", tt.args, code, out, errs, tt.code, tt.out, tt.errs)
			}
		})
	}
}

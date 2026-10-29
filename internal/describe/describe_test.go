package describe_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/packages"
	"golang.org/x/tools/txtar"

	"github.com/go-kanna/orm/internal/describe"
	"github.com/go-kanna/orm/internal/placeholder"
	"github.com/go-kanna/orm/internal/snapshot"
)

// module writes the files of archive, a txtar archive, into a module of its own, example.com/shop, and loads its
// packages.
func module(t *testing.T, archive string) []*packages.Package {
	t.Helper()

	set, err := packages.Load([]string{"./..."}, packages.Config{Dir: writeModule(t, archive)})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Diags) > 0 {
		t.Fatalf("load: %v", set.Diags)
	}
	return set.Packages
}

// writeModule writes the files of archive into a module of its own, example.com/shop, and returns its directory.
func writeModule(t *testing.T, archive string) string {
	t.Helper()

	dir := t.TempDir()
	files := txtar.Parse([]byte(archive)).Files
	files = append(files, txtar.File{Name: "go.mod", Data: []byte("module example.com/shop\n\ngo 1.26\n")})
	for _, f := range files {
		path := filepath.Join(dir, filepath.FromSlash(f.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, f.Data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// shop declares a scope type for orders and a repository; a test adds the .sql files.
const shop = `
-- scopes/scopes.go --
package scopes

//kanna:scope table=orders
type Order struct{}

//kanna:scope table=order_lines func=Lines
type OrderLine struct{}
-- repository/repository.go --
package repository

//kanna:repository
type OrderRepository interface {
	CountOpen() int
}
`

// fake is a database that only rewrites the parameters of what it is asked to describe, and keeps what it was asked.
type fake struct{ asked []string }

func (*fake) Dialect() snapshot.Dialect { return snapshot.PostgreSQL }

func (*fake) Tables(context.Context) (map[string]snapshot.Table, error) {
	return map[string]snapshot.Table{}, nil
}

func (f *fake) Statement(_ context.Context, sql string) (snapshot.Statement, error) {
	f.asked = append(f.asked, sql)
	_, names, err := placeholder.Rewrite(sql, snapshot.PostgreSQL)
	if err != nil {
		return snapshot.Statement{}, fmt.Errorf("%w", err)
	}
	st := snapshot.Statement{Columns: []snapshot.Result{{Name: "n", Type: "int8"}}}
	for _, name := range names {
		st.Params = append(st.Params, snapshot.Param{Name: name})
	}
	return st, nil
}

// report renders ds as diag does, naming each file by its base name.
func report(ds []diag.Diag) string {
	lines := make([]string, 0, len(ds))
	for _, d := range ds {
		d.Pos.Filename = filepath.Base(d.Pos.Filename)
		lines = append(lines, d.String())
	}
	return strings.Join(lines, "\n")
}

func TestDescribe_FindsTheStatementsNextToTheDeclarations(t *testing.T) {
	t.Parallel()

	pkgs := module(t, shop+`
-- scopes/order_scopes.sql --
-- scope: Order.Open()
status <> 'closed'

-- scope: Order.PlacedSince(at time.Time)
placed_at >= :at
-- repository/order_repository.sql --
-- query: CountOpen
SELECT count(*) AS n FROM orders WHERE status = :status;
-- other/other.go --
package other
-- other/schema.sql --
-- query: NoneOfOurs
SELECT 1
`)
	db := &fake{}
	s, ds := describe.Describe(t.Context(), db, pkgs)
	if len(ds) > 0 {
		t.Fatalf("Describe:\n%s", report(ds))
	}

	want := map[string]snapshot.Statement{
		"example.com/shop/repository.CountOpen": {
			Digest:  snapshot.Digest("SELECT count(*) AS n FROM orders WHERE status = :status;"),
			Params:  []snapshot.Param{{Name: "status"}},
			Columns: []snapshot.Result{{Name: "n", Type: "int8"}},
		},
		"example.com/shop/scopes.Order.Open": {
			Digest: snapshot.Digest("status <> 'closed'"),
		},
		"example.com/shop/scopes.Order.PlacedSince": {
			Digest: snapshot.Digest("placed_at >= :at"),
			Params: []snapshot.Param{{Name: "at"}},
		},
	}
	if !reflect.DeepEqual(s.Statements, want) {
		t.Errorf("statements =\n%+v\nwant\n%+v", s.Statements, want)
	}
	// A scope is asked about as the condition of a query of its table.
	if !slices.Contains(db.asked, "SELECT 1 FROM \"orders\" WHERE (\nplaced_at >= :at\n)") {
		t.Errorf("asked:\n%s", strings.Join(db.asked, "\n---\n"))
	}
}

func TestDescribe_Mistakes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, archive, want string
	}{
		{"a query next to no repository", `
-- scopes/order_scopes.sql --
-- query: CountOpen
SELECT count(*) FROM orders
`, "order_scopes.sql:1:1: error: -- query: CountOpen is next to no //kanna:repository interface"},
		{"a scope of no scope type", `
-- scopes/order_scopes.sql --
-- scope: Ordr.Open()
status <> 'closed'
`, "order_scopes.sql:1:1: error: -- scope: Ordr.Open: Ordr is no //kanna:scope type of this package\n" +
			"  did you mean Order?"},
		{"a statement written in two files", `
-- repository/a.sql --
-- query: CountOpen
SELECT 1
-- repository/b.sql --
-- query: CountOpen
SELECT 2
`, "b.sql:1:1: error: -- query: CountOpen is written a second time\n  it is also at a.sql:1"},
		{"a placeholder of the database's own in a query", `
-- repository/order_repository.sql --
-- query: CountOpen
SELECT count(*) AS n
FROM orders WHERE status = $1
`, "order_repository.sql:3:28: error: $1 is a placeholder of PostgreSQL's own: write the parameter as :name"},
		{"a placeholder of the database's own in a scope", `
-- scopes/order_scopes.sql --
-- scope: Order.Open()
status <> 'closed'
  AND kind = $2
`, "order_scopes.sql:3:14: error: $2 is a placeholder of PostgreSQL's own: write the parameter as :name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, ds := describe.Describe(t.Context(), &fake{}, module(t, shop+tt.archive))
			if got := report(ds); got != tt.want {
				t.Errorf("Describe reports\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestDescribe_ScopeDirectives(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, directive, want string
	}{
		{"without its table", "//kanna:scope",
			"scopes.go:3:1: error: //kanna:scope wants the table its type is a scope of\n  as in //kanna:scope table=orders"},
		{"with an option it does not know", "//kanna:scope table=orders colour=red",
			"scopes.go:3:1: error: //kanna:scope takes table= and func=, not \"colour=red\"\n" +
				"  as in //kanna:scope table=orders"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pkgs := module(t, `
-- scopes/scopes.go --
package scopes

`+tt.directive+`
type Order struct{}
`)
			_, ds := describe.Describe(t.Context(), &fake{}, pkgs)
			if got := report(ds); got != tt.want {
				t.Errorf("Describe reports\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestDescribe_Databases(t *testing.T) {
	t.Parallel()

	const archive = shop + `
-- scopes/order_scopes.sql --
-- scope: Order.PlacedSince(at time.Time)
placed_at >= :at
-- repository/order_repository.sql --
-- query: CountOpen
SELECT count(*) AS n FROM orders WHERE status = :status
`
	tests := []struct {
		name        string
		db          func(t *testing.T) describe.Database
		since, open snapshot.Statement
	}{
		{"PostgreSQL",
			func(t *testing.T) describe.Database {
				t.Helper()
				conn, _ := postgres(t)
				postgresExec(t, conn, `CREATE TABLE orders (
	id bigint PRIMARY KEY, status text NOT NULL, placed_at timestamptz NOT NULL
)`)
				return describe.Postgres(conn)
			},
			snapshot.Statement{Params: []snapshot.Param{{Name: "at", Type: "timestamptz"}}},
			snapshot.Statement{
				Params:  []snapshot.Param{{Name: "status", Type: "text"}},
				Columns: []snapshot.Result{{Name: "n", Type: "int8"}},
			}},
		{"MySQL",
			func(t *testing.T) describe.Database {
				t.Helper()
				conn, _ := mysql(t)
				mysqlExec(t, conn, `CREATE TABLE orders (
	id BIGINT PRIMARY KEY, status TEXT NOT NULL, placed_at DATETIME(6) NOT NULL
)`)
				return describe.MySQL(conn)
			},
			snapshot.Statement{Params: []snapshot.Param{{Name: "at"}}},
			snapshot.Statement{
				Params:  []snapshot.Param{{Name: "status"}},
				Columns: []snapshot.Result{{Name: "n", Type: "bigint", NotNull: true}},
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, ds := describe.Describe(t.Context(), tt.db(t), module(t, archive))
			if len(ds) > 0 {
				t.Fatalf("Describe:\n%s", report(ds))
			}
			if _, ok := s.Tables["orders"]; !ok {
				t.Errorf("tables = %v, want orders among them", s.Tables)
			}
			got := s.Statements["example.com/shop/scopes.Order.PlacedSince"]
			got.Digest = ""
			if !reflect.DeepEqual(got, tt.since) {
				t.Errorf("Order.PlacedSince = %+v, want %+v", got, tt.since)
			}
			got = s.Statements["example.com/shop/repository.CountOpen"]
			got.Digest = ""
			if !reflect.DeepEqual(got, tt.open) {
				t.Errorf("CountOpen = %+v, want %+v", got, tt.open)
			}
		})
	}
}

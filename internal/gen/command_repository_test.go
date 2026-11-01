package gen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"
	"github.com/go-kanna/internal/packages"

	"github.com/go-kanna/orm/internal/snapshot"
)

// repositoryShop declares orders and their lines, their scope types, a repository of them with a query written by
// hand, and a method of its implementation written by hand in the destination, postgres.
const repositoryShop = `
-- model/model.go --
package model

type Order struct {
	ID     int64
	Status string
	Lines  []OrderLine
}

type OrderLine struct {
	OrderID int64
	SKU     string
}
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm"
)

//kanna:scope table=orders
type Order struct{ q orm.Of[model.Order] }

//kanna:scope table=order_lines
type OrderLine struct{ q orm.Of[model.OrderLine] }

// Open narrows to the orders that are not closed.
func (s Order) Open() Order {
	return s.StatusNe("closed")
}
-- repository/repository.go --
package repository

import (
	"context"

	"example.com/shop/model"
	"example.com/shop/scopes"
)

//kanna:repository
type OrderRepository interface {
	Get(ctx context.Context, id int64, s ...scopes.Order) (model.Order, error)
	List(ctx context.Context, s ...scopes.Order) ([]model.Order, error)
	Insert(ctx context.Context, o model.Order) (model.Order, error)
	ReopenNew(ctx context.Context) (int64, error)
	Archive(ctx context.Context, id int64) error
}
-- postgres/queries.sql --
-- query: ReopenNew
UPDATE orders SET status = 'open' WHERE status = 'new';
-- postgres/archive.go --
package postgres

import "context"

// Archive is written by hand, which kanna-orm leaves to it.
func (r OrderRepository) Archive(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, "UPDATE orders SET status = 'archived' WHERE id = $1", id)
	return err
}
`

// describeRepositoryShop writes the snapshot of the tables repositoryShop reads into the module at dir.
func describeRepositoryShop(t *testing.T, dir string, d snapshot.Dialect) {
	t.Helper()

	s := snapshot.Snapshot{
		Dialect: d,
		Tables: map[string]snapshot.Table{
			"orders": {
				Columns: []snapshot.Column{
					{Name: "id", Type: "int8", NotNull: true, Assigned: true},
					{Name: "status", Type: "text", NotNull: true},
				},
				PrimaryKey: []string{"id"},
			},
			"order_lines": {
				Columns: []snapshot.Column{
					{Name: "order_id", Type: "int8", NotNull: true},
					{Name: "sku", Type: "text", NotNull: true},
				},
				ForeignKeys: []snapshot.ForeignKey{{Columns: []string{"order_id"}, References: "orders",
					ReferencedColumns: []string{"id"}}},
			},
		},
		Statements: map[string]snapshot.Statement{
			"example.com/shop/postgres.ReopenNew": {
				Digest: snapshot.Digest("UPDATE orders SET status = 'open' WHERE status = 'new';"),
			},
		},
	}
	if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCommand_Repositories(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, repositoryShop)
	describeRepositoryShop(t, dir, snapshot.PostgreSQL)
	repository := filepath.Join(dir, "repository")
	args := []string{"-destination", "../postgres", "-driver", "database/sql"}

	// go generate ./... runs repository before scopes, whose methods are not written yet.
	code, out, errOut := run(t, repository, append([]string{"-v"}, args...)...)
	if code != cli.ExitOK || out != "wrote ../postgres/orm_gen.go\n" || errOut != "" {
		t.Fatalf("kanna-orm -v %s = %d\n%s%s", strings.Join(args, " "), code, out, errOut)
	}
	if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
		t.Fatalf("kanna-orm in scopes = %d\n%s", code, errOut)
	}
	set, err := packages.Load([]string{"./..."}, packages.Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Diags) > 0 {
		t.Fatalf("the module does not compile once generated: %v", set.Diags)
	}
	src, err := os.ReadFile(filepath.Join(dir, "postgres", "orm_gen.go")) //nolint:gosec // the file written above
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), ") Archive(") {
		t.Errorf("orm_gen.go declares Archive, which the destination declares by hand:\n%s", src)
	}
	if code, _, errOut := run(t, repository, append([]string{"-check"}, args...)...); code != cli.ExitOK {
		t.Errorf("kanna-orm -check right after generating = %d\n%s", code, errOut)
	}
}

func TestCommand_RepositoriesWithoutRowReads(t *testing.T) {
	t.Parallel()

	const repository = `-- repository/repository.go --
package repository

import (
	"context"

	"example.com/shop/scopes"
)

//kanna:repository table=orders
type Orders interface {
	Count(ctx context.Context, s ...scopes.Order) (int64, error)
	Exists(ctx context.Context, id int64) (bool, error)
	Delete(ctx context.Context, id int64) (int64, error)
}
`
	for _, driver := range []string{"pgx", "database/sql"} {
		t.Run(driver, func(t *testing.T) {
			t.Parallel()

			models, _, _ := strings.Cut(repositoryShop, "-- repository/repository.go --")
			dir := writeModule(t, models+repository)
			describeRepositoryShop(t, dir, snapshot.PostgreSQL)

			if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
				t.Fatalf("kanna-orm in scopes = %d\n%s", code, errOut)
			}
			args := []string{"-destination", "../postgres", "-driver", driver}
			if code, _, errOut := run(t, filepath.Join(dir, "repository"), args...); code != cli.ExitOK {
				t.Fatalf("kanna-orm in repository = %d\n%s", code, errOut)
			}
			cmd := exec.CommandContext(t.Context(), "go", "test", "-mod=mod", "./...")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOWORK=off")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("the module does not compile once generated: %v\n%s", err, out)
			}
		})
	}
}

func TestCommand_RepositoriesRemoved(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, repositoryShop)
	describeRepositoryShop(t, dir, snapshot.PostgreSQL)
	repository := filepath.Join(dir, "repository")
	args := []string{"-destination", "../postgres", "-driver", "database/sql"}
	if code, _, errOut := run(t, repository, args...); code != cli.ExitOK {
		t.Fatalf("kanna-orm = %d\n%s", code, errOut)
	}

	// The directive goes, but Archive, in the destination, still calls what kanna-orm wrote.
	path := filepath.Join(repository, "repository.go")
	src, err := os.ReadFile(path) //nolint:gosec // the file written above
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // the file written above
	if err := os.WriteFile(path, []byte(strings.Replace(string(src), "//kanna:repository\n", "", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := run(t, repository, append([]string{"-v"}, args...)...)
	if code != cli.ExitFailure || !strings.Contains(errOut, "archive.go") {
		t.Errorf("kanna-orm -v with Archive left = %d\n%s%s", code, out, errOut)
	}

	if err := os.Remove(filepath.Join(dir, "postgres", "archive.go")); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = run(t, repository, append([]string{"-v"}, args...)...)
	if code != cli.ExitOK || out != "removed ../postgres/orm_gen.go\n" {
		t.Errorf("kanna-orm -v = %d\n%s%s", code, out, errOut)
	}
}

func TestCommand_StatementsWrittenElsewhere(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, archive, want string }{
		{"a query next to the repositories", `
-- repository/stale.sql --
-- query: CloseStale
UPDATE orders SET status = 'closed' WHERE status = 'new';
`, "./stale.sql:1:1: error: -- query: CloseStale goes where the repositories are implemented, not next to the " +
			"declarations\n  move it into the directory -destination names\n"},
		{"a scope where the repositories are implemented", `
-- postgres/order_scopes.sql --
-- scope: Order.Closed()
status = 'closed'
`, "../postgres/order_scopes.sql:1:1: error: -- scope: Order.Closed: Order is no //kanna:scope type of this " +
			"package\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := writeModule(t, repositoryShop+tt.archive)
			describeRepositoryShop(t, dir, snapshot.PostgreSQL)
			code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", "../postgres", "-driver",
				"database/sql")
			if code != cli.ExitFailure || !strings.Contains(errOut, tt.want) {
				t.Errorf("kanna-orm = %d\n%s\nwant\n%s", code, errOut, tt.want)
			}
		})
	}
}

func TestCommand_ReadsEachSQLFileOnce(t *testing.T) {
	t.Parallel()

	// scopes declares a repository too, and a .sql file both read holds a mistake.
	dir := writeModule(t, repositoryShop+`
-- scopes/repository.go --
package scopes

import (
	"context"

	"example.com/shop/model"
)

//kanna:repository
type OrderRepository interface {
	List(ctx context.Context, s ...Order) ([]model.Order, error)
}
-- scopes/broken.sql --
-- query:
SELECT 1;
`)
	describeRepositoryShop(t, dir, snapshot.PostgreSQL)
	code, _, errOut := run(t, filepath.Join(dir, "scopes"), "-destination", "../impl", "-driver", "database/sql")
	if want := "./broken.sql:1:1: error: -- query: wants the name of its method\n"; code != cli.ExitFailure ||
		!strings.HasPrefix(errOut, want) || strings.Count(errOut, "broken.sql") != 1 {
		t.Errorf("kanna-orm = %d\n%s\nwant the mistake once, as\n%s", code, errOut, want)
	}
}

func TestCommand_RepositoryMistakes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dialect snapshot.Dialect
		dir     string
		args    []string
		extra   string
		want    string
	}{
		{"no destination", snapshot.PostgreSQL, "repository", nil, "",
			"error: example.com/shop/repository declares //kanna:repository interfaces, whose implementations go into " +
				"the package -destination names\n  as in //go:generate go tool kanna-orm -destination ../postgres\n"},
		{"a mistake in the scope types, besides", snapshot.PostgreSQL, "repository", nil,
			"-- scopes/broken.go --\npackage scopes\n\nvar _ int = \"one\"\n",
			"../scopes/broken.go:3:13: error: cannot use \"one\" (untyped string constant) as int value in variable " +
				"declaration\n" +
				"error: example.com/shop/repository declares //kanna:repository interfaces, whose implementations go into " +
				"the package -destination names\n  as in //go:generate go tool kanna-orm -destination ../postgres\n"},
		{"a driver kanna-orm does not know", snapshot.PostgreSQL, "repository",
			[]string{"-destination", "../postgres", "-driver", "sqlite"}, "",
			"error: -driver takes pgx or database/sql, not \"sqlite\"\n"},
		{"pgx on MySQL", snapshot.MySQL, "repository", []string{"-destination", "../postgres", "-driver", "pgx"}, "",
			"error: -driver pgx runs on PostgreSQL, but the snapshot is of MySQL\n"},
		{"the destination the repositories are in", snapshot.PostgreSQL, "repository",
			[]string{"-destination", "."}, "",
			"error: -destination . is example.com/shop/repository, whose interfaces the implementations import\n" +
				"  name a directory of the implementations' own\n"},
		{"the destination the scope types are in", snapshot.PostgreSQL, "repository",
			[]string{"-destination", "../scopes"}, "",
			"error: -destination ../scopes holds the //kanna:scope types of example.com/shop/scopes, whose methods " +
				"go into its orm_gen.go\n  name a directory of the implementations' own\n"},
		{"repositories in two packages", snapshot.PostgreSQL, ".", []string{"-destination", "postgres", "./..."},
			"-- other/other.go --\npackage other\n\n//kanna:repository\ntype Other interface{}\n",
			"error: example.com/shop/other and example.com/shop/repository both declare //kanna:repository " +
				"interfaces, but -destination takes those of one package\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// The scopes are not generated yet, as go generate ./... leaves them for after repository, and what of
			// them is missing goes unsaid.
			dir := writeModule(t, repositoryShop+tt.extra)
			describeRepositoryShop(t, dir, tt.dialect)
			code, _, errOut := run(t, filepath.Join(dir, tt.dir), tt.args...)
			if code != cli.ExitFailure || errOut != tt.want {
				t.Errorf("kanna-orm %s = %d\n%s\nwant %d\n%s", strings.Join(tt.args, " "), code, errOut,
					cli.ExitFailure, tt.want)
			}
		})
	}
}

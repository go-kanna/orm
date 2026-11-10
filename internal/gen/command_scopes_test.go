package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"
	"github.com/go-kanna/internal/packages"

	"github.com/go-kanna/orm/internal/snapshot"
	"github.com/go-kanna/orm/internal/sqlfile"
)

// impliedShop declares the scope types of its models with //kanna:scopes alone, a method of one by hand, and a
// repository taking them.
const impliedShop = `
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

// Email is a value type, of no table.
type Email struct{ s string }
-- scopes/scopes.go --
package scopes

//kanna:scopes model-package=../model

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
	List(ctx context.Context, s ...scopes.Order) ([]model.Order, error)
}
`

func TestCommand_ImpliedScopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, extra string
		// declared and missing are what scopes_gen.go of scopes declares, and does not.
		declared, missing []string
	}{
		{"implied alone", "", []string{"type Order struct", "type OrderLine struct", "func OrderLines()"},
			[]string{"Email"}},
		{"one declared by hand", `
-- scopes/line.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=order_lines
type Line struct{ q scope.Of[model.OrderLine] }
`, []string{"type Order struct", "func (s Line) Of()"},
			[]string{"type OrderLine struct", "type Line struct", "func OrderLines()", "func Lines()"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := writeModule(t, impliedShop+tt.extra)
			describeRepositoryShop(t, dir, snapshot.PostgreSQL)
			repository, scopes := filepath.Join(dir, "repository"), filepath.Join(dir, "scopes")
			args := []string{"-destination", "../postgres", "-driver", "database/sql"}

			// go generate ./... runs repository before scopes.
			if code, _, errOut := run(t, repository, args...); code != cli.ExitOK {
				t.Fatalf("kanna-orm in repository = %d\n%s", code, errOut)
			}
			if code, out, errOut := run(t, scopes, "-v"); code != cli.ExitOK || out != "wrote ./scopes_gen.go\n" {
				t.Fatalf("kanna-orm -v in scopes = %d\n%s%s", code, out, errOut)
			}
			set, err := packages.Load([]string{"./..."}, packages.Config{Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			if len(set.Diags) > 0 {
				t.Fatalf("the module does not compile once generated: %v", set.Diags)
			}
			src, err := os.ReadFile(filepath.Join(scopes, "scopes_gen.go")) //nolint:gosec // the file written above
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range tt.declared {
				if !strings.Contains(string(src), d) {
					t.Errorf("scopes_gen.go lacks %q", d)
				}
			}
			for _, m := range tt.missing {
				if strings.Contains(string(src), m) {
					t.Errorf("scopes_gen.go has %q", m)
				}
			}
			for _, at := range []string{repository, scopes} {
				check := []string{"-check"}
				if at == repository {
					check = append(check, args...)
				}
				if code, _, errOut := run(t, at, check...); code != cli.ExitOK {
					t.Errorf("kanna-orm -check in %s = %d\n%s", filepath.Base(at), code, errOut)
				}
			}
		})
	}
}

func TestCommand_ImpliedScopesMistakes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, scopes, want string
	}{
		{"a package of another module", "//kanna:scopes model-package=example.com/elsewhere/model\n",
			"./scopes.go:3:1: error: //kanna:scopes takes the packages of example.com/shop, not " +
				"example.com/elsewhere/model\n  declare a scope type of each table of its models with //kanna:scope\n"},
		{"a package with no struct named after a table", "//kanna:scopes model-package=example.com/shop/repository\n",
			"./scopes.go:3:1: warning: //kanna:scopes model-package=example.com/shop/repository implies no scope type: " +
				"no struct of it is named after a table of the snapshot\n  as order_lines is after OrderLine\n"},
		{"a name declared already", "//kanna:scopes model-package=example.com/shop/model\n\nfunc Orders() {}\n",
			"./scopes.go:3:1: error: Order: Orders, which the scope type //kanna:scopes implies of orders needs, is " +
				"declared already\n  it is declared at scopes.go:5\n  declare the scope type yourself: " +
				"//kanna:scope table=orders\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := writeModule(t, impliedShop+"-- scopes/scopes.go --\npackage scopes\n\n"+tt.scopes)
			describeRepositoryShop(t, dir, snapshot.PostgreSQL)
			code, _, errOut := run(t, filepath.Join(dir, "scopes"))
			if errOut != tt.want {
				t.Errorf("kanna-orm = %d\n%s\nwant\n%s", code, errOut, tt.want)
			}
		})
	}
}

// TestCommand_ImpliedScopesWrittenByHand generates a package of scope types that //kanna:scopes declares, whose only
// Go file imports nothing, with scopes whose signatures name a type of a package the model's file imports and one of
// the model's own package: before the package has scopes_gen.go, which imports the model's package, and after.
func TestCommand_ImpliedScopesWrittenByHand(t *testing.T) {
	t.Parallel()

	const sql = "-- scope: Order.PlacedSince(at time.Time)\nSELECT 1 FROM orders WHERE placed_at >= :at;\n\n" +
		"-- scope: Order.In(st model.Status)\nstatus = :st\n"
	dir := writeModule(t, `
-- model/model.go --
package model

import "time"

type Order struct {
	ID       int64
	Status   Status
	PlacedAt time.Time
}

type Status string
-- scopes/scopes.go --
package scopes

//kanna:scopes model-package=../model
-- scopes/order_scopes.sql --
`+sql)
	stmts, _ := sqlfile.Parse("order_scopes.sql", []byte(sql))
	s := snapshot.Snapshot{
		Dialect: snapshot.PostgreSQL,
		Tables: map[string]snapshot.Table{"orders": {
			Columns: []snapshot.Column{
				{Name: "id", Type: "int8", NotNull: true, Assigned: true},
				{Name: "status", Type: "text", NotNull: true},
				{Name: "placed_at", Type: "timestamptz", NotNull: true},
			},
			PrimaryKey: []string{"id"},
		}},
		Statements: map[string]snapshot.Statement{
			"example.com/shop/scopes.Order.PlacedSince": {Digest: snapshot.Digest(stmts[0].Text)},
			"example.com/shop/scopes.Order.In":          {Digest: snapshot.Digest(stmts[1].Text)},
		},
	}
	if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
	scopes := filepath.Join(dir, "scopes")

	if code, out, errOut := run(t, scopes, "-v"); code != cli.ExitOK || out != "wrote ./scopes_gen.go\n" {
		t.Fatalf("kanna-orm -v = %d\n%s%s", code, out, errOut)
	}
	set, err := packages.Load([]string{"./..."}, packages.Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Diags) > 0 {
		t.Fatalf("the module does not compile once generated: %v", set.Diags)
	}
	src, err := os.ReadFile(filepath.Join(scopes, "scopes_gen.go")) //nolint:gosec // the file written above
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"func (s Order) PlacedSince(at time.Time) Order {",
		"func (s Order) In(st model.Status) Order {",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("scopes_gen.go lacks %q:\n%s", want, src)
		}
	}
	if code, _, errOut := run(t, scopes, "-check"); code != cli.ExitOK {
		t.Errorf("kanna-orm -check = %d\n%s", code, errOut)
	}
}

// describeUUIDShop writes the snapshot of the tables of orders and their lines, keyed by a uuid, into the module at
// dir.
func describeUUIDShop(t *testing.T, dir string) {
	t.Helper()

	s := snapshot.Snapshot{
		Dialect: snapshot.PostgreSQL,
		Tables: map[string]snapshot.Table{
			"orders": {
				Columns:    []snapshot.Column{{Name: "id", Type: "uuid", NotNull: true, Default: true}},
				PrimaryKey: []string{"id"},
			},
			"order_lines": {
				Columns: []snapshot.Column{
					{Name: "order_id", Type: "uuid", NotNull: true},
					{Name: "sku", Type: "text", NotNull: true},
				},
				PrimaryKey: []string{"order_id", "sku"},
				ForeignKeys: []snapshot.ForeignKey{{Columns: []string{"order_id"}, References: "orders",
					ReferencedColumns: []string{"id"}}},
			},
		},
	}
	if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestCommand_UUIDArraysBeforeGo127 reports a uuid held in a type of [16]byte by a module of Go 1.26, whose
// database/sql cannot read one into it.
func TestCommand_UUIDArraysBeforeGo127(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, `
-- model/model.go --
package model

type OrderID [16]byte

type Order struct {
	ID OrderID
}

type OrderLine struct {
	OrderID OrderID
	SKU     string
}
-- scopes/scopes.go --
package scopes

//kanna:scopes model-package=../model
`)
	describeUUIDShop(t, dir)
	code, _, errOut := run(t, filepath.Join(dir, "scopes"))
	want := "../model/model.go:6:2: error: Order.ID: database/sql reads a uuid into model.OrderID from Go 1.27 on, " +
		"and the module declares an earlier go version\n  declare go 1.27 or later in go.mod\n"
	if code != cli.ExitFailure || !strings.Contains(errOut, want) {
		t.Errorf("kanna-orm in scopes = %d\n%s\nwant it to report\n%s", code, errOut, want)
	}
}

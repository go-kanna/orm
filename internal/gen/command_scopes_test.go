package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"
	"github.com/go-kanna/internal/packages"

	"github.com/go-kanna/orm/internal/snapshot"
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

//kanna:scopes example.com/shop/model

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
		// declared and missing are what orm_gen.go of scopes declares, and does not.
		declared, missing []string
	}{
		{"implied alone", "", []string{"type Order struct", "type OrderLine struct", "func OrderLines()"},
			[]string{"Email"}},
		{"one declared by hand", `
-- scopes/line.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm"
)

//kanna:scope table=order_lines func=Lines
type Line struct{ q orm.Of[model.OrderLine] }
`, []string{"type Order struct", "func Lines() Line"}, []string{"type OrderLine struct", "type Line struct"}},
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
			if code, out, errOut := run(t, scopes, "-v"); code != cli.ExitOK || out != "wrote ./orm_gen.go\n" {
				t.Fatalf("kanna-orm -v in scopes = %d\n%s%s", code, out, errOut)
			}
			set, err := packages.Load([]string{"./..."}, packages.Config{Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			if len(set.Diags) > 0 {
				t.Fatalf("the module does not compile once generated: %v", set.Diags)
			}
			src, err := os.ReadFile(filepath.Join(scopes, "orm_gen.go")) //nolint:gosec // the file written above
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range tt.declared {
				if !strings.Contains(string(src), d) {
					t.Errorf("orm_gen.go lacks %q", d)
				}
			}
			for _, m := range tt.missing {
				if strings.Contains(string(src), m) {
					t.Errorf("orm_gen.go has %q", m)
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
		{"a package of another module", "//kanna:scopes example.com/elsewhere/model\n",
			"./scopes.go:3:1: error: //kanna:scopes takes the packages of example.com/shop, not " +
				"example.com/elsewhere/model\n  declare a scope type of each table of its models with //kanna:scope\n"},
		{"a package with no struct named after a table", "//kanna:scopes example.com/shop/repository\n",
			"./scopes.go:3:1: warning: //kanna:scopes example.com/shop/repository implies no scope type: no struct of " +
				"it is named after a table of the snapshot\n  as order_lines is after OrderLine\n"},
		{"a name declared already", "//kanna:scopes example.com/shop/model\n\nfunc Orders() {}\n",
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

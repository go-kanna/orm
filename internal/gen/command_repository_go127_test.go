//go:build go1.27

package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"
	"github.com/go-kanna/internal/packages"
)

// uuidShop declares orders and their lines, keyed by a type declared of the uuid.UUID of the standard library, the
// scope types //kanna:scopes implies of them, and a repository of them, whose Delete tells its table by table= alone.
const uuidShop = `
-- model/model.go --
package model

import "uuid"

type OrderID uuid.UUID

type Order struct {
	ID    OrderID
	Lines []OrderLine
}

type OrderLine struct {
	OrderID OrderID
	SKU     string
}
-- scopes/scopes.go --
package scopes

//kanna:scopes model-package=../model
-- repository/repository.go --
package repository

import (
	"context"

	"example.com/shop/model"
	"example.com/shop/scopes"
)

//kanna:repository table=orders
type OrderRepository interface {
	Get(ctx context.Context, id model.OrderID, s ...scopes.Order) (model.Order, error)
	List(ctx context.Context, s ...scopes.Order) ([]model.Order, error)
	Insert(ctx context.Context, o model.Order) error
	InsertLines(ctx context.Context, lines []model.OrderLine) error
	Delete(ctx context.Context, id model.OrderID) error
}
`

func TestCommand_RepositoriesOfUUIDs(t *testing.T) {
	t.Parallel()

	dir := writeModuleGo(t, uuidShop, "1.27.0")
	describeUUIDShop(t, dir)
	if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
		t.Fatalf("kanna-orm in scopes = %d\n%s", code, errOut)
	}
	args := []string{"-destination", "../postgres", "-driver", "database/sql"}
	if code, _, errOut := run(t, filepath.Join(dir, "repository"), args...); code != cli.ExitOK {
		t.Fatalf("kanna-orm %s = %d\n%s", strings.Join(args, " "), code, errOut)
	}
	set, err := packages.Load([]string{"./..."}, packages.Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Diags) > 0 {
		t.Fatalf("the module does not compile once generated: %v", set.Diags)
	}

	src, err := os.ReadFile(filepath.Join(dir, "scopes", "scopes_gen.go")) //nolint:gosec // the file written above
	if err != nil {
		t.Fatal(err)
	}
	// A uuid is compared for equality alone, as a value of no order.
	for _, method := range []string{") IDEq(", ") IDIn(", ") IDAsc(", ") OrderIDIn("} {
		if !strings.Contains(string(src), method) {
			t.Errorf("the scope types have no method %s", strings.Trim(method, ")("))
		}
	}
	if strings.Contains(string(src), ") IDLt(") {
		t.Error("the scope types have the method IDLt")
	}
	// A cursor holds an OrderID as a uuid.UUID, so that it encodes as text.
	for _, conv := range []string{"`id`: uuid.UUID(row.ID),", "`id`: (*uuid.UUID)(&row.ID),"} {
		if !strings.Contains(string(src), conv) {
			t.Errorf("the scope types do not convert the key of a cursor: no %s", conv)
		}
	}
}

func TestCommand_UUIDsBeforeGo127(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, uuidShop)
	describeUUIDShop(t, dir)
	code, _, errOut := run(t, filepath.Join(dir, "scopes"))
	want := "../model/model.go:8:2: error: Order.ID: database/sql reads a uuid into model.OrderID from Go 1.27 on, " +
		"and the module declares an earlier go version\n  declare go 1.27 or later in go.mod\n"
	if code != cli.ExitFailure || !strings.Contains(errOut, want) {
		t.Errorf("kanna-orm in scopes = %d\n%s\nwant it to report\n%s", code, errOut, want)
	}
}

// TestCommand_UUIDKeysInErrors writes the key of a uuid column held in a type of [16]byte into an error message as a
// uuid.UUID, which formats as the text of a uuid, unless the type has a String method of its own.
func TestCommand_UUIDKeysInErrors(t *testing.T) {
	t.Parallel()

	stringer := strings.Replace(uuidShop, "type OrderID uuid.UUID\n",
		"type OrderID uuid.UUID\n\nfunc (id OrderID) String() string { return uuid.UUID(id).String() }\n", 1)
	for name, tc := range map[string]struct {
		shop, want string
	}{
		"without String": {uuidShop, `"orders with id %v: %w", uuid.UUID(id), orm.ErrNotFound)`},
		"with String":    {stringer, `"orders with id %v: %w", id, orm.ErrNotFound)`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := writeModuleGo(t, tc.shop, "1.27.0")
			describeUUIDShop(t, dir)
			if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
				t.Fatalf("kanna-orm in scopes = %d\n%s", code, errOut)
			}
			args := []string{"-destination", "../postgres", "-driver", "database/sql"}
			if code, _, errOut := run(t, filepath.Join(dir, "repository"), args...); code != cli.ExitOK {
				t.Fatalf("kanna-orm %s = %d\n%s", strings.Join(args, " "), code, errOut)
			}
			set, err := packages.Load([]string{"./..."}, packages.Config{Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			if len(set.Diags) > 0 {
				t.Fatalf("the module does not compile once generated: %v", set.Diags)
			}
			path := filepath.Join(dir, "postgres", "repository_gen.go")
			src, err := os.ReadFile(path) //nolint:gosec // the file written above
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(string(src), tc.want); got != 2 {
				t.Errorf("Get and Delete write %s %d times, not twice\n%s", tc.want, got, src)
			}
		})
	}
}

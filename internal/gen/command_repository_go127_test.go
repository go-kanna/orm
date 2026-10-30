//go:build go1.27

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

// uuidShop declares orders and their lines, keyed by the uuid.UUID of the standard library, the scope types
// //kanna:scopes implies of them, and a repository of them, whose Delete tells its table by table= alone.
const uuidShop = `
-- model/model.go --
package model

import "uuid"

type Order struct {
	ID    uuid.UUID
	Lines []OrderLine
}

type OrderLine struct {
	OrderID uuid.UUID
	SKU     string
}
-- scopes/scopes.go --
package scopes

//kanna:scopes ../model
-- repository/repository.go --
package repository

import (
	"context"
	"uuid"

	"example.com/shop/model"
	"example.com/shop/scopes"
)

//kanna:repository table=orders
type OrderRepository interface {
	Get(ctx context.Context, id uuid.UUID, s ...scopes.Order) (model.Order, error)
	List(ctx context.Context, s ...scopes.Order) ([]model.Order, error)
	Insert(ctx context.Context, o model.Order) error
	InsertLines(ctx context.Context, lines []model.OrderLine) error
	Delete(ctx context.Context, id uuid.UUID) error
}
`

// describeUUIDShop writes the snapshot of the tables uuidShop reads into the module at dir.
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

func TestCommand_RepositoriesOfUUIDs(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, uuidShop)
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

	src, err := os.ReadFile(filepath.Join(dir, "scopes", "orm_gen.go")) //nolint:gosec // the file written above
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
}

package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
	"github.com/go-kanna/orm/internal/sqlfile"
)

// chainedShop is a module whose model and repository are right, for a test to break one thing of.
const chainedShop = `-- model/model.go --
package model

type Order struct {
	ID    int64
	Lines []OrderLine
}

type OrderLine struct {
	ID      int64
	OrderID int64
}
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=orders
type Order struct{ q scope.Of[model.Order] }

//kanna:scope table=order_lines
type OrderLine struct{ q scope.Of[model.OrderLine] }
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
	Close(ctx context.Context, id int64) (int64, error)
}
-- postgres/queries.sql --
-- query: Close
DELETE FROM orders WHERE id = :id;
`

func TestCommand_ChainedMistakes(t *testing.T) {
	t.Parallel()

	// The model is read from export data, which keeps the line of a field but not its column.
	fieldOfNoColumn := "../model/model.go:4:1: error: Order.ID: [8]byte is neither a column nor a relation\n" +
		`  hold the column in a type with a Scan method, or leave the field out with orm:"-"` + "\n"
	_, queries, _ := strings.Cut(chainedShop, "-- postgres/queries.sql --\n")
	closeQuery, _ := sqlfile.Parse("queries.sql", []byte(queries))
	for _, tt := range []struct {
		name, before, after, dir, want string
	}{
		{name: "a field of no column, which a relation is loaded by", before: "ID    int64", after: "ID    [8]byte",
			dir: "scopes", want: fieldOfNoColumn},
		{name: "a field of no column, which a parameter stands for", before: "ID    int64", after: "ID    [8]byte",
			dir: "repository", want: fieldOfNoColumn},
		{name: "a constructor of the repository declared already", before: "-- postgres/queries.sql --",
			after: "-- postgres/new.go --\npackage postgres\n\ntype NewOrderRepository struct{}\n" +
				"-- postgres/queries.sql --",
			dir: "repository", want: "../postgres/new.go:3:6: error: NewOrderRepository is declared already, and " +
				"kanna-orm writes it for OrderRepository\n  name yours otherwise\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := writeModule(t, strings.Replace(chainedShop, tt.before, tt.after, 1))
			id := snapshot.Column{Name: "id", Type: "int8", NotNull: true}
			s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
				"orders": {Columns: []snapshot.Column{id}, PrimaryKey: []string{"id"}},
				"order_lines": {Columns: []snapshot.Column{id, {Name: "order_id", Type: "int8", NotNull: true}},
					PrimaryKey: []string{"id"}, ForeignKeys: []snapshot.ForeignKey{{Columns: []string{"order_id"},
						References: "orders", ReferencedColumns: []string{"id"}}}},
			}, Statements: map[string]snapshot.Statement{
				"example.com/shop/postgres.Close": {Params: []snapshot.Param{{Name: "id", Type: "int8"}},
					Digest: snapshot.Digest(closeQuery[0].Text)},
			}}
			if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
				t.Fatal(err)
			}
			var args []string
			if tt.dir == "repository" {
				args = []string{"-destination", "../postgres"}
			}
			code, _, errOut := run(t, filepath.Join(dir, tt.dir), args...)
			if code == cli.ExitOK || errOut != tt.want {
				t.Fatalf("generate = %d, reports\n%s\nwant\n%s", code, errOut, tt.want)
			}
		})
	}
}

//go:build go1.27

package gen_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/orm/internal/gen"
	"github.com/go-kanna/orm/internal/snapshot"
)

func TestFields_UUID(t *testing.T) {
	t.Parallel()

	pkg := module(t, orderScope+`
-- model/model.go --
package model

import "uuid"

type Order struct {
	ID         uuid.UUID
	CustomerID *uuid.UUID
	Refs       []uuid.UUID
}
`)
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"orders": {Columns: []snapshot.Column{
			{Name: "id", Type: "uuid", NotNull: true, Default: true},
			{Name: "customer_id", Type: "uuid"},
			{Name: "refs", Type: "uuid[]", NotNull: true},
		}},
	}}
	got, ds := gen.Fields(pkg, s)
	if len(ds) > 0 {
		t.Fatalf("Fields:\n%s", report(ds))
	}
	want := []string{
		"Order.ID uuid.UUID id",
		"Order.CustomerID *uuid.UUID customer_id",
		"Order.Refs []uuid.UUID refs",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Fields =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestFields_UUIDMistakes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, field, want string
	}{
		{"a column that may be NULL in a uuid.UUID", "\tCustomerID uuid.UUID\n",
			"model.go:7:1: error: Order.CustomerID: orders.customer_id may be NULL, which a uuid.UUID cannot hold\n" +
				"  declare it as *uuid.UUID"},
		{"a uuid.UUID for text", "\tNote *uuid.UUID\n",
			"model.go:7:1: error: Order.Note: orders.note is text, which a *uuid.UUID cannot hold\n" +
				"  declare it as *string, or as a type of your own with Scan and Value methods"},
	}
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"orders": {Columns: []snapshot.Column{
			{Name: "id", Type: "uuid", NotNull: true},
			{Name: "customer_id", Type: "uuid"},
			{Name: "note", Type: "text"},
		}},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pkg := module(t, orderScope+`
-- model/model.go --
package model

import "uuid"

type Order struct {
	ID uuid.UUID
`+tt.field+"}\n")
			_, ds := gen.Fields(pkg, s)
			if got := report(ds); got != tt.want {
				t.Errorf("Fields reports\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

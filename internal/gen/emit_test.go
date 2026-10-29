package gen_test

import (
	"path/filepath"
	"testing"

	"github.com/go-kanna/internal/packages"
	"github.com/go-kanna/internal/pkgtest"

	"github.com/go-kanna/orm/internal/gen"
	"github.com/go-kanna/orm/internal/snapshot"
)

// orders is the table of the scope types under testdata.
var orders = snapshot.Table{
	Columns: []snapshot.Column{
		{Name: "id", Type: "int8", NotNull: true, Assigned: true},
		{Name: "status", Type: "text", NotNull: true},
		{Name: "placed_at", Type: "timestamptz", NotNull: true},
		{Name: "note", Type: "text"},
		{Name: "total", Type: "int8", NotNull: true},
		{Name: "paid", Type: "bool", NotNull: true},
		{Name: "contact", Type: "text"},
		{Name: "tags", Type: "_text"},
		{Name: "receipt", Type: "bytea"},
		{Name: "order", Type: "int4", NotNull: true},
	},
	PrimaryKey: []string{"id"},
}

func TestEmit(t *testing.T) {
	t.Parallel()

	for dir, dialect := range map[string]snapshot.Dialect{"postgres": snapshot.PostgreSQL, "mysql": snapshot.MySQL} {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()

			set, err := packages.Load([]string{"./testdata/" + dir}, packages.Config{Regenerated: []string{"orm_gen.go"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(set.Diags) > 0 || len(set.Packages) != 1 {
				t.Fatalf("load: %d packages, %v", len(set.Packages), set.Diags)
			}
			s := snapshot.Snapshot{Dialect: dialect, Tables: map[string]snapshot.Table{"orders": orders}}
			got, ds, err := gen.Emit(set.Packages[0], s)
			if len(ds) > 0 {
				t.Fatalf("Emit:\n%s", report(ds))
			}
			if err != nil {
				t.Fatalf("Emit: %v\n%s", err, got)
			}
			pkgtest.Golden(t, filepath.Join("testdata", dir, "orm_gen.go"), got)
		})
	}
}

func TestEmit_FieldsNamedAlike(t *testing.T) {
	t.Parallel()

	pkg := module(t, orderScope+`
-- model/model.go --
package model

type Order struct {
	ID int64
	Audit
}

type Audit struct {
	ID int64 `+"`orm:\"audit_id\"`"+`
}
`)
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"orders": {Columns: columns("id", "audit_id"), PrimaryKey: []string{"id"}},
	}}
	_, ds, err := gen.Emit(pkg, s)
	if err != nil {
		t.Fatal(err)
	}
	want := "model.go:9:1: error: Order.Audit.ID: its methods would be named as those of Order.ID, such as IDEq\n" +
		`  leave one of them out with orm:"-"`
	if got := report(ds); got != want {
		t.Errorf("Emit reports\n%s\nwant\n%s", got, want)
	}
}

package gen_test

import (
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-kanna/internal/packages"
	"github.com/go-kanna/internal/pkgtest"

	"github.com/go-kanna/orm/internal/gen"
	"github.com/go-kanna/orm/internal/snapshot"
	"github.com/go-kanna/orm/internal/sqlfile"
)

// tables are those of the scope types under testdata. tags has no primary key, and so no page continues by it.
var tables = map[string]snapshot.Table{
	"orders": {
		Columns: []snapshot.Column{
			{Name: "id", Type: "int8", NotNull: true, Assigned: true},
			{Name: "customer_id", Type: "int8", NotNull: true},
			{Name: "status", Type: "text", NotNull: true},
			{Name: "placed_at", Type: "timestamptz", NotNull: true},
			{Name: "note", Type: "text"},
			{Name: "total", Type: "int8", NotNull: true},
			{Name: "paid", Type: "bool", NotNull: true},
			{Name: "contact", Type: "text"},
			{Name: "receipt", Type: "bytea"},
			{Name: "order", Type: "int4", NotNull: true},
		},
		PrimaryKey:  []string{"id"},
		UniqueKeys:  [][]string{{"order"}},
		ForeignKeys: []snapshot.ForeignKey{fk([]string{"customer_id"}, "customers", "id")},
	},
	"customers": {
		Columns: []snapshot.Column{
			{Name: "id", Type: "int8", NotNull: true},
			{Name: "name", Type: "text", NotNull: true},
		},
		PrimaryKey: []string{"id"},
	},
	"order_lines": {
		Columns: []snapshot.Column{
			{Name: "order_id", Type: "int8", NotNull: true},
			{Name: "line_no", Type: "int4", NotNull: true},
			{Name: "sku", Type: "text", NotNull: true},
		},
		PrimaryKey:  []string{"order_id", "line_no"},
		ForeignKeys: []snapshot.ForeignKey{fk([]string{"order_id"}, "orders", "id")},
	},
	"tags": {Columns: []snapshot.Column{{Name: "name", Type: "text", NotNull: true}}, UniqueKeys: [][]string{{"name"}}},
	"labels": {
		Columns:    []snapshot.Column{{Name: "order_id", Type: "int8", NotNull: true}, {Name: "names", Type: "text[]"}},
		PrimaryKey: []string{"order_id"},
	},
	"order_tags": {
		Columns: columns("order_id", "tag"), PrimaryKey: []string{"order_id", "tag"},
		ForeignKeys: []snapshot.ForeignKey{
			fk([]string{"order_id"}, "orders", "id"),
			fk([]string{"tag"}, "tags", "name"),
		},
	},
}

// described records the statements of the .sql files of testdata/dir, as describe would, by their digests alone.
func described(t *testing.T, dir string) map[string]snapshot.Statement {
	t.Helper()

	files, err := filepath.Glob(filepath.Join("testdata", dir, "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	statements := map[string]snapshot.Statement{}
	for _, file := range files {
		data, err := os.ReadFile(file) //nolint:gosec // a file of testdata
		if err != nil {
			t.Fatal(err)
		}
		stmts, ds := sqlfile.Parse(file, data)
		if len(ds) > 0 {
			t.Fatalf("parse %s:\n%s", file, report(ds))
		}
		for _, st := range stmts {
			key := "github.com/go-kanna/orm/internal/gen/testdata/" + dir + "." + st.Name
			if st.Type != "" {
				key = "github.com/go-kanna/orm/internal/gen/testdata/" + dir + "." + st.Type + "." + st.Name
			}
			statements[key] = snapshot.Statement{Digest: snapshot.Digest(st.Text)}
		}
	}
	return statements
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
			s := snapshot.Snapshot{Dialect: dialect, Tables: tables, Statements: described(t, dir)}
			got, ds, err := gen.Emit(set.Packages[0], s)
			if len(ds) > 0 {
				t.Fatalf("Emit:\n%s", report(ds))
			}
			if err != nil {
				t.Fatalf("Emit: %v\n%s", err, got)
			}
			pkgtest.Golden(t, filepath.Join("testdata", dir, "orm_gen.go"), got)

			// The names checked against those of the methods written by hand are those of the methods written.
			methods, err := gen.Methods(set.Packages[0], s)
			if err != nil {
				t.Fatal(err)
			}
			if written := generatedMethods(set.Packages[0]); !reflect.DeepEqual(methods, written) {
				t.Errorf("Methods =\n%v\nbut orm_gen.go declares\n%v", methods, written)
			}
		})
	}
}

// generatedMethods names the methods orm_gen.go declares on each type of pkg, sorted.
func generatedMethods(pkg *packages.Package) map[string][]string {
	methods := map[string][]string{}
	for _, name := range pkg.Types.Scope().Names() {
		named, ok := pkg.Types.Scope().Lookup(name).Type().(*types.Named)
		if !ok {
			continue
		}
		for m := range named.Methods() {
			if filepath.Base(pkg.Fset.Position(m.Pos()).Filename) == "orm_gen.go" {
				methods[name] = append(methods[name], m.Name())
			}
		}
		slices.Sort(methods[name])
	}
	return methods
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

func TestEmit_RelatedScopeTypeElsewhere(t *testing.T) {
	t.Parallel()

	pkg := module(t, `
-- model/model.go --
package model

type OrderLine struct {
	ID        int64
	ProductID int64
	Product   *Product
}

type Product struct{ ID int64 }
-- catalog/catalog.go --
package catalog

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm"
)

//kanna:scope table=products
type Product struct{ q orm.Of[model.Product] }
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm"
)

//kanna:scope table=order_lines
type OrderLine struct{ q orm.Of[model.OrderLine] }
`)
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"order_lines": {
			Columns: columns("id", "product_id"), PrimaryKey: []string{"id"},
			ForeignKeys: []snapshot.ForeignKey{fk([]string{"product_id"}, "products", "id")},
		},
		"products": {Columns: columns("id"), PrimaryKey: []string{"id"}},
	}}
	src, ds, err := gen.Emit(pkg, s)
	if err != nil || len(ds) > 0 {
		t.Fatalf("Emit: %v\n%s", err, report(ds))
	}
	for _, want := range []string{
		"\t\"example.com/shop/catalog\"\n",
		"func (s OrderLine) WithProduct(sub ...catalog.Product) OrderLine {",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("Emit wrote no %q:\n%s", want, src)
		}
	}
}

func TestEmit_RelationsNamedAlike(t *testing.T) {
	t.Parallel()

	pkg := module(t, `
-- model/model.go --
package model

type Order struct {
	ID         int64
	CustomerID int64
	Customer   *Customer
	Extra
}

type Extra struct {
	Customer *Customer
}

type Customer struct{ ID int64 }
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm"
)

//kanna:scope table=orders
type Order struct{ q orm.Of[model.Order] }

//kanna:scope table=customers
type Customer struct{ q orm.Of[model.Customer] }
`)
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"orders": {
			Columns: columns("id", "customer_id"), PrimaryKey: []string{"id"},
			ForeignKeys: []snapshot.ForeignKey{fk([]string{"customer_id"}, "customers", "id")},
		},
		"customers": {Columns: columns("id"), PrimaryKey: []string{"id"}},
	}}
	_, ds, err := gen.Emit(pkg, s)
	if err != nil {
		t.Fatal(err)
	}
	want := "model.go:11:1: error: Order.Extra.Customer: its method would be named as that of Order.Customer, " +
		"WithCustomer\n" + `  leave one of them out with orm:"-"`
	if got := report(ds); got != want {
		t.Errorf("Emit reports\n%s\nwant\n%s", got, want)
	}
}

func TestEmitRepositories(t *testing.T) {
	t.Parallel()

	const testdata = "github.com/go-kanna/orm/internal/gen/testdata/"
	results := map[string][]snapshot.Result{
		"MonthCounts": {{Name: "month", Type: "timestamptz"}, {Name: "count", Type: "int8"}},
		"TotalOf":     {{Name: "customer", Type: "text", NotNull: true}, {Name: "total", Type: "int8"}},
	}
	for _, tt := range []struct {
		scopes, repo string
		d            snapshot.Dialect
		drv, dest    string
	}{
		{"postgres", "repository", snapshot.PostgreSQL, "pgx", "pgxrepo"},
		{"postgres", "repository", snapshot.PostgreSQL, "database/sql", "pgsqlrepo"},
		{"mysql", "mysqlrepository", snapshot.MySQL, "database/sql", "mysqlrepo"},
	} {
		t.Run(tt.dest, func(t *testing.T) {
			t.Parallel()

			set, err := packages.Load([]string{"./testdata/" + tt.scopes, "./testdata/" + tt.repo}, packages.Config{})
			if err != nil {
				t.Fatal(err)
			}
			if len(set.Diags) > 0 {
				t.Fatalf("load: %v", set.Diags)
			}
			s := snapshot.Snapshot{Dialect: tt.d, Tables: tables, Statements: described(t, tt.repo)}
			for name, columns := range results {
				st := s.Statements[testdata+tt.repo+"."+name]
				st.Columns = columns
				s.Statements[testdata+tt.repo+"."+name] = st
			}
			got, ds, err := gen.EmitRepositories(set.Packages, testdata+tt.repo, s, tt.drv, testdata+tt.dest, tt.dest)
			if len(ds) > 0 {
				t.Fatalf("EmitRepositories:\n%s", report(ds))
			}
			if err != nil {
				t.Fatalf("EmitRepositories: %v\n%s", err, got)
			}
			pkgtest.Golden(t, filepath.Join("testdata", tt.dest, "orm_gen.go"), got)
		})
	}
}

func TestEmitRepositories_RelationsItCannotLoad(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, `
-- model/model.go --
package model

type Order struct {
	ID       int64
	TenantID int64
	Lines    []OrderLine
	Invoice  *Invoice
}

type OrderLine struct{ OrderID int32 }

type Invoice struct {
	OrderID  int64
	TenantID int64
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

//kanna:scope table=invoices
type Invoice struct{ q orm.Of[model.Invoice] }
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
`)
	set, err := packages.Load([]string{"./..."}, packages.Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"orders": {
			Columns: columns("id", "tenant_id"), PrimaryKey: []string{"id"}, UniqueKeys: [][]string{{"id", "tenant_id"}},
		},
		"order_lines": {
			Columns: columns("order_id"), ForeignKeys: []snapshot.ForeignKey{fk([]string{"order_id"}, "orders", "id")},
		},
		"invoices": {
			Columns: columns("order_id", "tenant_id"), PrimaryKey: []string{"order_id", "tenant_id"},
			ForeignKeys: []snapshot.ForeignKey{fk([]string{"order_id", "tenant_id"}, "orders", "id", "tenant_id")},
		},
	}}
	_, ds, err := gen.EmitRepositories(set.Packages, "example.com/shop/repository", s, "pgx",
		"example.com/shop/postgres", "postgres")
	if err != nil {
		t.Fatal(err)
	}
	want := "model.go:6:2: error: Order.Lines: Order.ID is an int64, but OrderLine.OrderID, which it is loaded by, is " +
		"an int32\n  declare them alike\n" +
		"model.go:7:2: error: Order.Invoice: kanna-orm does not load a relation by a key of more than one column yet"
	if got := report(ds); got != want {
		t.Errorf("EmitRepositories reports\n%s\nwant\n%s", got, want)
	}
}

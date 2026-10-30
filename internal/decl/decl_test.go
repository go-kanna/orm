package decl_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-kanna/internal/diag"

	"github.com/go-kanna/orm/internal/decl"
)

// read parses src as the file scopes.go, and reads its declarations as "Name table func" for each scope type, and the
// name of each repository interface.
func read(t *testing.T, src string) (scopes, repositories []string, ds []diag.Diag) {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "scopes.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	decls, ds := decl.Read(fset, file)
	for _, s := range decls.Scopes {
		scopes = append(scopes, strings.TrimSpace(s.Spec.Name.Name+" "+s.Table+" "+s.Func))
	}
	for _, r := range decls.Repositories {
		repositories = append(repositories, r.Name.Name)
	}
	return scopes, repositories, ds
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

func TestRead(t *testing.T) {
	t.Parallel()

	scopes, repositories, ds := read(t, `package scopes

//kanna:scope table=orders
type Order struct{}

// OrderLine is a scope of the lines of orders.
//
//kanna:scope table=order_lines func=Lines
type OrderLine struct{}

type (
	//kanna:scope table=person func=People
	Person struct{}

	unmarked struct{}
)

//kanna:repository
type OrderRepository interface{}

var _ = 0
`)
	if len(ds) > 0 {
		t.Fatalf("Read:\n%s", report(ds))
	}
	if want := []string{"Order orders", "OrderLine order_lines Lines", "Person person People"}; !reflect.DeepEqual(
		scopes, want) {
		t.Errorf("scopes = %q, want %q", scopes, want)
	}
	if want := []string{"OrderRepository"}; !reflect.DeepEqual(repositories, want) {
		t.Errorf("repositories = %q, want %q", repositories, want)
	}
}

func TestRead_Models(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "scopes.go", `package scopes

//go:generate go tool kanna-orm

//kanna:scopes example.com/shop/model example.com/shop/billing

//kanna:scope table=order_lines
type Line struct{}
`, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	decls, ds := decl.Read(fset, file)
	if len(ds) > 0 {
		t.Fatalf("Read:\n%s", report(ds))
	}
	if len(decls.Models) != 1 || decls.Models[0].Pos.Line != 5 || !reflect.DeepEqual(decls.Models[0].Paths,
		[]string{"example.com/shop/model", "example.com/shop/billing"}) {
		t.Errorf("Models = %+v", decls.Models)
	}
	if len(decls.Scopes) != 1 {
		t.Errorf("Scopes = %+v, want Line alone", decls.Scopes)
	}

	_, ds = decl.Read(fset, mustParse(t, fset, "package scopes\n\n//kanna:scopes\n"))
	if got, want := report(ds), "scopes.go:3:1: error: //kanna:scopes wants the packages of the models\n"+
		"  as in //kanna:scopes example.com/shop/model"; got != want {
		t.Errorf("Read of //kanna:scopes alone reports\n%s\nwant\n%s", got, want)
	}
}

func mustParse(t *testing.T, fset *token.FileSet, src string) *ast.File {
	t.Helper()

	file, err := parser.ParseFile(fset, "scopes.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func TestTableOf(t *testing.T) {
	t.Parallel()

	tables := []string{"orders", "order_lines", "categories", "statuses", "boxes", "days", "person", "http_logs"}
	for name, want := range map[string]string{
		"Order": "orders", "OrderLine": "order_lines", "Category": "categories", "Status": "statuses", "Box": "boxes",
		"Day": "days", "Person": "person", "HTTPLog": "http_logs", "Email": "",
	} {
		got, ok := decl.TableOf(name, func(table string) bool { return slices.Contains(tables, table) })
		if got != want || ok != (want != "") {
			t.Errorf("TableOf(%s) = %q, %v; want %q", name, got, ok, want)
		}
	}
}

func TestImply(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for name, src := range map[string]string{
		"model.go": "package model\n\ntype Order struct{}\n\ntype (\n\tOrderLine struct{}\n\tEmail struct{}\n" +
			"\tStatus string\n\tAlias = Order\n\tpage[T any] struct{}\n)\n\ntype customer struct{}\n",
		"model_test.go": "package model\n\ntype Customer struct{}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tables := []string{"orders", "order_lines", "customers", "aliases", "statuses"}
	implied := decl.Imply(decl.Models{Paths: []string{"example.com/shop/model", "example.com/elsewhere"}},
		func(path string) (string, bool) { return dir, path == "example.com/shop/model" },
		func(table string) bool { return slices.Contains(tables, table) })
	got := make([]string, 0, len(implied))
	for _, im := range implied {
		got = append(got, im.ModelPath+"."+im.Model+" "+im.Table)
	}
	if want := []string{"example.com/shop/model.Order orders", "example.com/shop/model.OrderLine order_lines"}; !reflect.
		DeepEqual(got, want) {
		t.Errorf("Imply = %q, want %q", got, want)
	}
}

func TestRead_Mistakes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, directive, want string
	}{
		{"a directive misspelled", "//kanna:scop table=orders",
			`scopes.go:3:1: warning: "//kanna:scop table=orders" is not recognized as a directive` + "\n" +
				`  did you mean "//kanna:scope table=orders"?`},
		{"no table", "//kanna:scope",
			"scopes.go:3:1: error: //kanna:scope wants the table its type is a scope of\n  as in //kanna:scope table=orders"},
		{"an option it does not know", "//kanna:scope table=orders colour=red",
			"scopes.go:3:1: error: //kanna:scope takes table= and func=, not \"colour=red\"\n" +
				"  as in //kanna:scope table=orders"},
		{"a table given twice", "//kanna:scope table=orders table=order_lines",
			"scopes.go:3:1: error: //kanna:scope gives table= twice\n  as in //kanna:scope table=orders"},
		{"a function given twice", "//kanna:scope table=orders func=Orders func=AllOrders",
			"scopes.go:3:1: error: //kanna:scope gives func= twice\n  as in //kanna:scope table=orders"},
		{"a function no Go function can be named", "//kanna:scope table=orders func=all-orders",
			"scopes.go:3:1: error: //kanna:scope func=all-orders: \"all-orders\" is not a name a Go function can have\n" +
				"  as in //kanna:scope table=orders"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			scopes, _, ds := read(t, "package scopes\n\n"+tt.directive+"\ntype Order struct{}\n")
			if got := report(ds); got != tt.want {
				t.Errorf("Read reports\n%s\nwant\n%s", got, tt.want)
			}
			if len(scopes) > 0 {
				t.Errorf("scopes = %q, want none", scopes)
			}
		})
	}
}

package decl_test

import (
	"fmt"
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

// read parses src as the file scopes.go, and reads its declarations as "Name table func" for each scope type, and
// "Name table" for each repository interface.
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
		repositories = append(repositories, strings.TrimSpace(r.Spec.Name.Name+" "+r.Table))
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

//kanna:scope
type Customer struct{ q scope.Of[model.Customer] }

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

//kanna:repository table=people
type PersonRepository interface{}

var _ = 0
`)
	if len(ds) > 0 {
		t.Fatalf("Read:\n%s", report(ds))
	}
	want := []string{"Order orders", "Customer customers", "OrderLine order_lines Lines", "Person person People"}
	if !reflect.DeepEqual(scopes, want) {
		t.Errorf("scopes = %q, want %q", scopes, want)
	}
	if want := []string{"OrderRepository", "PersonRepository people"}; !reflect.DeepEqual(repositories, want) {
		t.Errorf("repositories = %q, want %q", repositories, want)
	}
}

func TestRead_Models(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "scopes.go", `package scopes

//go:generate go tool kanna-orm

//kanna:scopes model-package=example.com/shop/model,example.com/shop/billing

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

	for directive, want := range map[string]string{
		"//kanna:scopes":          "//kanna:scopes needs model-package=…",
		"//kanna:scopes ../model": "//kanna:scopes takes model-package=…, not \"../model\"",
		"//kanna:scopes model-package=": "//kanna:scopes takes a value for model-package, " +
			"as in model-package=…",
		"//kanna:scopes model-package=a,,b": "//kanna:scopes model-package=a,,b has an empty package " +
			"among its commas",
		"//kanna:scopes model-package=a model-package=b": "//kanna:scopes takes model-package once",
	} {
		_, ds = decl.Read(fset, mustParse(t, fset, "package scopes\n\n"+directive+"\n"))
		if got, want := report(ds), "scopes.go:3:1: error: "+want+"\n  as in //kanna:scopes model-package=../model"; got !=
			want {
			t.Errorf("Read of %s reports\n%s\nwant\n%s", directive, got, want)
		}
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

	tables := []string{"orders", "order_lines", "categories", "statuses", "boxes", "days", "person", "http_logs",
		"sales_people", "children"}
	for name, want := range map[string]string{
		"Order": "orders", "OrderLine": "order_lines", "Category": "categories", "Status": "statuses", "Box": "boxes",
		"Day": "days", "HTTPLog": "http_logs", "SalesPerson": "sales_people", "Child": "children", "Email": "",
		"Person": "", // person is not plural

	} {
		got, ok := decl.TableOf(name, func(table string) bool { return slices.Contains(tables, table) })
		if got != want || ok != (want != "") {
			t.Errorf("TableOf(%s) = %q, %v; want %q", name, got, ok, want)
		}
	}
}

func TestImply(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "model"), 0o750); err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{
		"model.go": "package model\n\ntype Order struct{}\n\ntype (\n\tOrderLine struct{}\n\tEmail struct{}\n" +
			"\tStatus string\n\tAlias = Order\n\tpage[T any] struct{}\n)\n\ntype customer struct{}\n",
		"model_test.go": "package model\n\ntype Customer struct{}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, "model", name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tables := []string{"orders", "order_lines", "customers", "aliases", "statuses"}
	m := decl.Models{
		Pos:   token.Position{Filename: filepath.Join(root, "scopes", "scopes.go")},
		Paths: []string{"../model", "example.com/elsewhere", "../../outside"},
	}
	implied := decl.Imply(m, root, "example.com/shop", func(table string) bool { return slices.Contains(tables, table) })
	got := make([]string, 0, len(implied))
	for _, im := range implied {
		got = append(got, im.ModelPath+"."+im.Model+" "+im.Table)
	}
	if want := []string{"example.com/shop/model.Order orders", "example.com/shop/model.OrderLine order_lines"}; !reflect.
		DeepEqual(got, want) {
		t.Errorf("Imply = %q, want %q", got, want)
	}
}

func TestModels_Resolve(t *testing.T) {
	t.Parallel()

	root := filepath.FromSlash("/src/shop")
	m := decl.Models{Pos: token.Position{Filename: filepath.Join(root, "scopes", "scopes.go")}}
	for _, tt := range []struct{ path, importPath, dir string }{
		{"example.com/shop/model", "example.com/shop/model", filepath.Join(root, "model")},
		{"../model", "example.com/shop/model", filepath.Join(root, "model")},
		{"./internal/model", "example.com/shop/scopes/internal/model", filepath.Join(root, "scopes", "internal", "model")},
		{"..", "example.com/shop", root},
		{"example.com/shop", "example.com/shop", root},
		{"example.com/shopping/model", "", ""},
		{"../../elsewhere", "", ""},
	} {
		importPath, dir, ok := m.Resolve(tt.path, root, "example.com/shop")
		if importPath != tt.importPath || dir != tt.dir || ok != (tt.importPath != "") {
			t.Errorf("Resolve(%s) = %s, %s, %v; want %s, %s", tt.path, importPath, dir, ok, tt.importPath, tt.dir)
		}
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
		{"an option it does not know", "//kanna:scope table=orders colour=red",
			"scopes.go:3:1: error: //kanna:scope takes table=… or func=…, not \"colour=red\"\n" +
				"  as in //kanna:scope table=orders"},
		{"a table given twice", "//kanna:scope table=orders table=order_lines",
			"scopes.go:3:1: error: //kanna:scope takes table once\n  as in //kanna:scope table=orders"},
		{"a function given twice", "//kanna:scope table=orders func=Orders func=AllOrders",
			"scopes.go:3:1: error: //kanna:scope takes func once\n  as in //kanna:scope table=orders"},
		{"a function no Go function can be named", "//kanna:scope table=orders func=all-orders",
			"scopes.go:3:1: error: //kanna:scope func=all-orders: \"all-orders\" is not a name a Go function can have\n" +
				"  as in //kanna:scope table=orders"},
		{"an option a repository does not take", "//kanna:repository table=orders func=Orders",
			"scopes.go:3:1: error: //kanna:repository takes table=…, not \"func=Orders\"\n" +
				"  as in //kanna:repository table=orders"},
		{"a repository of no table", "//kanna:repository table=",
			"scopes.go:3:1: error: //kanna:repository takes a value for table, as in table=…\n" +
				"  as in //kanna:repository table=orders"},
		{"a repository's table given twice", "//kanna:repository table=orders table=order_lines",
			"scopes.go:3:1: error: //kanna:repository takes table once\n  as in //kanna:repository table=orders"},
		{"an error translator, which a method of the implementation is", "//kanna:repository errors=translate",
			"scopes.go:3:1: error: //kanna:repository takes table=…, not \"errors=translate\"\n" +
				"  as in //kanna:repository table=orders"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			scopes, repositories, ds := read(t, "package scopes\n\n"+tt.directive+"\ntype Order interface{}\n")
			if got := report(ds); got != tt.want {
				t.Errorf("Read reports\n%s\nwant\n%s", got, tt.want)
			}
			if len(scopes) > 0 || len(repositories) > 0 {
				t.Errorf("scopes = %q, repositories = %q, want none", scopes, repositories)
			}
		})
	}
}

func TestRead_ScopesInTheDocOfAType(t *testing.T) {
	t.Parallel()

	_, _, ds := read(t, "package scopes\n\n//kanna:scopes model-package=../model\ntype Order struct{}\n")
	want := "scopes.go:3:1: warning: //kanna:scopes marks no declaration, so nothing reads it in the doc of a type\n" +
		"  write it in a comment of its own, apart from the type"
	if got := report(ds); !strings.Contains(got, want) {
		t.Errorf("Read reports\n%s\nwant\n%s", got, want)
	}
}

func TestRead_StrayDirectives(t *testing.T) {
	t.Parallel()

	const (
		loose = "warning: //kanna:%s is not attached to a declaration, so nothing reads it\n" +
			"  write it right above what it marks, with no blank line between"
		stray = "warning: //kanna:%s marks the type it is the doc of, but this comment is the doc of no one type\n" +
			"  write it right above one type, with no blank line between"
	)
	tests := []struct {
		name, src, key, want string
	}{
		{"a blank line before the type", "//kanna:repository\n\ntype Orders interface{}\n", "repository", loose},
		{"a group of types", "//kanna:scope table=orders\ntype (\n\tOrder struct{}\n\tLine struct{}\n)\n", "scope",
			stray},
		{"a function", "//kanna:repository\nfunc Orders() {}\n", "repository", stray},
		{"a field", "type Order struct {\n\t//kanna:scope table=orders\n\tq int\n}\n", "scope", stray},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			scopes, repositories, ds := read(t, "package scopes\n\n"+tt.src)
			if got := report(ds); !strings.HasSuffix(got, fmt.Sprintf(tt.want, tt.key)) || len(ds) != 1 {
				t.Errorf("Read reports\n%s\nwant\n%s", got, fmt.Sprintf(tt.want, tt.key))
			}
			if len(scopes) > 0 || len(repositories) > 0 {
				t.Errorf("scopes = %q, repositories = %q, want none", scopes, repositories)
			}
		})
	}

	t.Run("one type of a group, and //kanna:scopes", func(t *testing.T) {
		t.Parallel()

		scopes, _, ds := read(t, "package scopes\n\n//kanna:scopes model-package=../model\n\n"+
			"type (\n\t//kanna:scope table=orders\n\tOrder struct{}\n\tLine struct{}\n)\n")
		if len(ds) > 0 || !slices.Equal(scopes, []string{"Order orders"}) {
			t.Errorf("scopes = %q, reports\n%s", scopes, report(ds))
		}
	})
}

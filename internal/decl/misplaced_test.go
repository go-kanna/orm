package decl_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/go-kanna/orm/internal/decl"
)

func TestMisplaced(t *testing.T) {
	t.Parallel()

	const (
		structs    = "//kanna:%s marks a struct type, not %s, so nothing reads it\n  write it right above a struct type"
		interfaces = "//kanna:%s marks an interface type, not %s, so nothing reads it\n" +
			"  write it right above an interface type"
		methods = "//kanna:%s marks a method of an interface, not %s, so nothing reads it\n" +
			"  write it right above a method of an interface"
	)
	errorf := func(at, format, key, shape string) string {
		return "scopes.go:" + at + ": error: " + strings.Replace(strings.Replace(format, "%s", key, 1), "%s", shape, 1)
	}
	for _, tt := range []struct{ name, src, want string }{
		{"a scope of a struct", "//kanna:scope table=orders\ntype Order struct{ q int }\n", ""},
		{"a scope of a type whose underlying type is a struct", "type order struct{ q int }\n\n" +
			"//kanna:scope table=orders\ntype Order order\n", ""},
		{"a repository of an interface", "//kanna:repository\ntype Orders interface {\n\t//kanna:lock update\n" +
			"\tList() error\n\t//kanna:conflict id\n\tUpsert() error\n}\n", ""},
		{"a repository of a type whose underlying type is an interface", "type orders interface{ List() error }\n\n" +
			"//kanna:repository\ntype Orders orders\n", ""},
		{"a scope of a string type", "//kanna:scope table=orders\ntype Order string\n",
			errorf("3:1", structs, "scope", "a string type")},
		{"a scope of an alias", "//kanna:scope table=orders\ntype Order = struct{ q int }\n",
			errorf("3:1", structs, "scope", "a type alias")},
		{"a scope of an interface", "//kanna:scope table=orders\ntype Order interface{}\n",
			errorf("3:1", structs, "scope", "an interface type")},
		{"a scope of a variable", "//kanna:scope table=orders\nvar Order struct{ q int }\n",
			errorf("3:1", structs, "scope", "a variable")},
		{"a scope of a group of types", "//kanna:scope table=orders\ntype (\n\tOrder struct{}\n\tLine struct{}\n)\n",
			errorf("3:1", structs, "scope", "a group of declarations")},
		{"a scope of a field", "type Order struct {\n\t//kanna:scope table=orders\n\tq int\n}\n",
			errorf("4:2", structs, "scope", "a struct field")},
		{"a repository of a struct", "//kanna:repository\ntype Orders struct{}\n",
			errorf("3:1", interfaces, "repository", "a struct type")},
		{"a repository of an alias", "type orders interface{}\n\n//kanna:repository\ntype Orders = orders\n",
			errorf("5:1", interfaces, "repository", "a type alias")},
		{"a repository of a function", "//kanna:repository\nfunc Orders() {}\n",
			errorf("3:1", interfaces, "repository", "a function")},
		{"a lock of the interface", "//kanna:lock update\ntype Orders interface{ List() error }\n",
			errorf("3:1", methods, "lock", "an interface type")},
		{"a lock of an embedded interface", "type Base interface{}\n\ntype Orders interface {\n\t//kanna:lock update\n" +
			"\tBase\n}\n", errorf("6:2", methods, "lock", "a type embedded in an interface")},
		{"a conflict of a method of a struct", "type Orders struct{}\n\n//kanna:conflict id\nfunc (Orders) Upsert() {}\n",
			errorf("5:1", methods, "conflict", "a method")},
		{"a lock of a field", "type Orders struct {\n\t//kanna:lock update\n\tList func() error\n}\n",
			errorf("4:2", methods, "lock", "a struct field")},
		{"in the order they are written", "//kanna:repository\n//kanna:lock update\n//kanna:scope\nfunc Orders() {}\n",
			errorf("3:1", interfaces, "repository", "a function") + "\n" + errorf("4:1", methods, "lock", "a function") +
				"\n" + errorf("5:1", structs, "scope", "a function")},
		{"a scopes, which stands in a comment of its own", "//kanna:scopes model-package=../model\nfunc Orders() {}\n",
			""},
		{"a pluck, which is no more", "//kanna:pluck name\nfunc Orders() {}\n", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "scopes.go", "package scopes\n\n"+tt.src, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			pkg, err := new(types.Config).Check("scopes", fset, []*ast.File{file}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := report(decl.Misplaced(fset, file, pkg)); got != tt.want {
				t.Errorf("Misplaced reports\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

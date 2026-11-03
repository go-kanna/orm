package gen_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"
)

// mapErrorArchive is typedQueryShop with a mapError method on the implementation of Records, local to the package of
// the interface or not, and the destination its implementation is generated into.
func mapErrorArchive(local bool, method string) (string, string) {
	archive := typedQueryShop
	pkg, receiver, destination := "impl", "Records", "../impl"
	if local {
		archive = strings.ReplaceAll(archive, "-- impl/", "-- repository/")
		pkg, receiver, destination = "repository", "*records", "."
	}
	archive += "\n-- " + pkg + "/map_error.go --\npackage " + pkg + `
import "fmt"
var mappedCalls int
func (` + receiver + `) mapError` + method + ` {
	mappedCalls++
	return fmt.Errorf("mapped: %w", err)
}
`
	return archive, destination
}

// relocateQuerySnapshot moves the queries of the snapshot of dir from the package impl to repository, for an
// implementation in the package of its interface.
func relocateQuerySnapshot(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "kanna-orm.snapshot.json")
	data, err := os.ReadFile(path) //nolint:gosec // snapshot written in this test's module
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.ReplaceAll(string(data), "example.com/shop/impl.", "example.com/shop/repository."))
	if err := os.WriteFile(path, data, 0o600); err != nil { //nolint:gosec // test module snapshot
		t.Fatal(err)
	}
}

func TestCommand_MapErrorMistakes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, method string }{
		{"another parameter", "(err string) error"},
		{"another result", "(err error) string"},
		{"two parameters", "(err error, table string) error"},
		{"two results", "(err error) (error, bool)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			archive, destination := mapErrorArchive(true, tt.method)
			dir := writeModule(t, archive)
			writeTypedQuerySnapshot(t, dir)
			relocateQuerySnapshot(t, dir)
			code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", destination)
			if want := "records.mapError: the errors of Records go through it, so it takes an error and returns one"; code ==
				cli.ExitOK || !strings.Contains(errOut, want) {
				t.Fatalf("generate = %d, want %q\n%s", code, want, errOut)
			}
		})
	}
}

func TestCommand_MapError(t *testing.T) {
	t.Parallel()
	for _, local := range []bool{false, true} {
		for _, driver := range []string{"pgx", "database/sql"} {
			name := driver + "/separate"
			if local {
				name = driver + "/local"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				archive, destination := mapErrorArchive(local, "(err error) error")
				pkg := "impl"
				if local {
					pkg = "repository"
				}
				if driver == "database/sql" {
					runtime := strings.Replace(typedQueryRuntime, "invalidState = true", `
    if mappedCalls != 0 { t.Fatal("mapped the error of nothing") }
    invalidState = true`, 1)
					runtime = strings.Replace(runtime,
						`if _, err := records.Get(ctx, 7); err == nil { t.Fatal("invalid enum was accepted") }`, `
    if _, err := records.Get(ctx, 7); err == nil || !strings.HasPrefix(err.Error(), "mapped: ") || mappedCalls != 1 {
        t.Fatalf("Get: %v, mapped %d times", err, mappedCalls)
    }
    if _, err := records.Lookup(ctx, 7); err == nil || !strings.HasPrefix(err.Error(), "mapped: ") || mappedCalls != 2 {
        t.Fatalf("Lookup: %v, mapped %d times", err, mappedCalls)
    }`, 1)
					runtime = strings.Replace(runtime, "package impl", "package "+pkg, 1)
					archive += "\n-- " + pkg + "/runtime_test.go --\n" + runtime
				}
				dir := writeModule(t, archive)
				writeTypedQuerySnapshot(t, dir)
				if local {
					relocateQuerySnapshot(t, dir)
				}
				for _, step := range []struct {
					pkg  string
					args []string
				}{
					{"scopes", nil}, {"repository", []string{"-destination", destination, "-driver", driver}},
				} {
					if code, _, errOut := run(t, filepath.Join(dir, step.pkg), step.args...); code != cli.ExitOK {
						t.Fatalf("generate %s = %d\n%s", step.pkg, code, errOut)
					}
				}
				moduleCommand(t, dir, "test", "-mod=mod", "./...")
			})
		}
	}
}

// TestEmitRepositories_MapsEveryError checks that each method of OrderRepository, whose implementations in testdata
// have a mapError, returns no error but through it.
func TestEmitRepositories_MapsEveryError(t *testing.T) {
	t.Parallel()
	for _, dest := range []string{"postgres/pgx", "mysql/stdlib"} {
		t.Run(dest, func(t *testing.T) {
			t.Parallel()
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, filepath.Join("testdata", dest, "repository_gen.go"), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			methods := 0
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || !fn.Name.IsExported() || receiverType(fn.Recv.List[0].Type) != "OrderRepository" ||
					fn.Type.Results == nil {
					continue
				}
				methods++
				recv := fn.Recv.List[0].Names[0].Name
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if _, ok := n.(*ast.FuncLit); ok {
						return false
					}
					ret, ok := n.(*ast.ReturnStmt)
					if !ok || len(ret.Results) == 0 {
						return true
					}
					if !mapped(ret.Results[len(ret.Results)-1], recv) {
						t.Errorf("%s: %s returns an error not through mapError", fset.Position(ret.Pos()), fn.Name.Name)
					}
					return true
				})
			}
			if methods == 0 {
				t.Fatal("no method of OrderRepository")
			}
		})
	}
}

// receiverType names the type of a receiver.
func receiverType(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	id, _ := expr.(*ast.Ident)
	return id.Name
}

// mapped reports whether err, the last result of a return statement, is nil or goes through recv's mapError.
func mapped(err ast.Expr, recv string) bool {
	if id, ok := err.(*ast.Ident); ok && id.Name == "nil" {
		return true
	}
	call, ok := err.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "mapError" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == recv
}

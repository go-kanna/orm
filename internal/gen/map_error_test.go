package gen_test

import (
	"fmt"
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
		{"another operation", "(op string, err error) error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			archive, destination := mapErrorArchive(true, tt.method)
			dir := writeModule(t, archive)
			writeTypedQuerySnapshot(t, dir)
			relocateQuerySnapshot(t, dir)
			code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", destination)
			want := "records.mapError: the errors of Records go through it, so it takes an error, or the operation of " +
				"Records it comes from and the error, and returns one\n" +
				"  as in func (records) mapError(err error) error\n" +
				"  or func (records) mapError(op recordsOperation, err error) error"
			if code == cli.ExitOK || !strings.Contains(errOut, want) {
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

// operationArchive is typedQueryShop with States in an interface Records embeds, Save written by hand through its
// helper, and a mapError taking the operation of Records, which it keeps.
func operationArchive(local bool) (archive, destination, pkg string) {
	archive = strings.Replace(typedQueryShop, "\tStates(ctx context.Context) ([]model.State, error)\n}",
		"\tstates\n}\ntype states interface {\n\tStates(ctx context.Context) ([]model.State, error)\n}", 1)
	pkg, receiver, destination := "impl", "Records", "../impl"
	if local {
		archive = strings.ReplaceAll(archive, "-- impl/", "-- repository/")
		pkg, receiver, destination = "repository", "records", "."
	}
	archive += "\n-- " + pkg + "/map_error.go --\npackage " + pkg + `
import (
	"context"
	"fmt"
	"example.com/shop/model"
)
var mappedCalls int
var lastOperation recordsOperation
func (` + receiver + `) mapError(op recordsOperation, err error) error {
	mappedCalls++
	lastOperation = op
	return fmt.Errorf("mapped: %w", err)
}
func (r ` + receiver + `) Save(ctx context.Context, record model.Record) (int64, error) {
	id, err := r.save(ctx, record)
	if err != nil {
		return 0, r.mapError(recordsOperationSave, err)
	}
	return id, nil
}
`
	return archive, destination, pkg
}

func TestCommand_MapErrorOperation(t *testing.T) {
	t.Parallel()
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprintf("local=%v", local), func(t *testing.T) {
			t.Parallel()
			archive, destination, pkg := operationArchive(local)
			runtime := strings.Replace(typedQueryRuntime, "invalidState = true", `
    if mappedCalls != 0 { t.Fatal("mapped the error of nothing") }
    invalidState = true`, 1)
			runtime = strings.Replace(runtime,
				`if _, err := records.Get(ctx, 7); err == nil { t.Fatal("invalid enum was accepted") }`, `
    if _, err := records.Get(ctx, 7); err == nil || mappedCalls != 1 || lastOperation != recordsOperationGet {
        t.Fatalf("Get: %v, mapped %d times, last as %q", err, mappedCalls, lastOperation)
    }
    if _, err := records.Lookup(ctx, 7); err == nil || mappedCalls != 2 || lastOperation != recordsOperationLookup {
        t.Fatalf("Lookup: %v, mapped %d times, last as %q", err, mappedCalls, lastOperation)
    }`, 1)
			archive += "\n-- " + pkg + "/runtime_test.go --\n" + strings.Replace(runtime, "package impl", "package "+pkg, 1)
			dir := writeModule(t, archive)
			writeTypedQuerySnapshot(t, dir)
			if local {
				relocateQuerySnapshot(t, dir)
			}
			generate := func() string {
				t.Helper()
				for _, step := range []struct {
					pkg  string
					args []string
				}{
					{"scopes", nil}, {"repository", []string{"-destination", destination, "-driver", "database/sql"}},
				} {
					if code, _, errOut := run(t, filepath.Join(dir, step.pkg), step.args...); code != cli.ExitOK {
						t.Fatalf("generate %s = %d\n%s", step.pkg, code, errOut)
					}
				}
				data, err := os.ReadFile(filepath.Join(dir, pkg, "repository_gen.go")) //nolint:gosec // the output
				if err != nil {
					t.Fatal(err)
				}
				return string(data)
			}

			// The first run writes recordsOperation, which mapError names before it does.
			generated := generate()
			spaced := strings.Join(strings.Fields(generated), " ")
			for _, want := range []string{"type recordsOperation string", `recordsOperationGet recordsOperation = "Get"`,
				`recordsOperationStates recordsOperation = "States"`, `recordsOperationSave recordsOperation = "Save"`,
				"mapError(recordsOperationGet, "} {
				if !strings.Contains(spaced, want) {
					t.Errorf("repository_gen.go has no %q", want)
				}
			}
			if _, helper, _ := strings.Cut(generated, ") save("); strings.Contains(strings.Split(helper, "\nfunc ")[0],
				"mapError") {
				t.Error("the helper of Save maps its errors, which Save maps again")
			}
			moduleCommand(t, dir, "test", "-mod=mod", "./...")

			// mapError may stop taking the operation, and take it again.
			path := filepath.Join(dir, pkg, "map_error.go")
			withOperation, err := os.ReadFile(path) //nolint:gosec // the fixture written above
			if err != nil {
				t.Fatal(err)
			}
			without := strings.NewReplacer("var lastOperation recordsOperation\n", "",
				"mapError(op recordsOperation, err error) error", "mapError(err error) error", "\tlastOperation = op\n", "",
				"r.mapError(recordsOperationSave, err)", "r.mapError(err)").Replace(string(withOperation))
			for _, src := range []string{without, string(withOperation)} {
				if err := os.WriteFile(path, []byte(src), 0o600); err != nil { //nolint:gosec // the fixture written above
					t.Fatal(err)
				}
				generated := generate()
				if takes := strings.Contains(src, "op recordsOperation"); takes != strings.Contains(generated,
					"type recordsOperation") {
					t.Errorf("mapError takes the operation: %v, but repository_gen.go declares it: %v", takes, !takes)
				}
				moduleCommand(t, dir, "build", "-mod=mod", "./...")
			}
		})
	}
}

func TestCommand_MapErrorOperationNames(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, add, want string
	}{
		{"generated twice", "-- repository/operation.go --\npackage repository\n" +
			"import (\n\t\"context\"\n\t\"example.com/shop/scopes\"\n)\n" +
			"//kanna:repository\ntype RecordsOperation interface {\n" +
			"\tCount(ctx context.Context, s ...scopes.Record) (int64, error)\n}\n",
			"Records: kanna-orm writes recordsOperation for it, and for RecordsOperation as well"},
		{"declared by hand", "-- repository/operation.go --\npackage repository\nconst recordsOperationGet = 1\n",
			"recordsOperationGet is declared already, and kanna-orm writes it for Records"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			archive, destination, _ := operationArchive(true)
			dir := writeModule(t, archive+"\n"+tt.add)
			writeTypedQuerySnapshot(t, dir)
			relocateQuerySnapshot(t, dir)
			if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
				t.Fatalf("generate scopes = %d\n%s", code, errOut)
			}
			code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", destination, "-driver",
				"database/sql")
			if code == cli.ExitOK || !strings.Contains(errOut, tt.want) {
				t.Fatalf("generate = %d, want %q\n%s", code, tt.want, errOut)
			}
		})
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

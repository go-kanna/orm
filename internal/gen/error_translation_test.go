package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"
)

const recordsTranslator = `
import "fmt"
var translatedCalls int
var translatedOperation recordsOperation
func translateRecordsError(op recordsOperation, err error) error {
    translatedCalls++
    translatedOperation = op
    return fmt.Errorf("translated %s: %w", op, err)
}
`

func translationArchive(local bool) (string, string) {
	archive := strings.Replace(typedQueryShop, "//kanna:repository",
		"//kanna:repository errors=translateRecordsError", 1)
	pkg, destination := "impl", "../impl"
	if local {
		archive = strings.ReplaceAll(archive, "-- impl/", "-- repository/")
		pkg, destination = "repository", "."
	}
	archive += "\n-- " + pkg + "/translate.go --\npackage " + pkg + "\n" + recordsTranslator
	return archive, destination
}

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

func TestCommand_ErrorTranslationMistakes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, before, after, want string }{
		{"wrong operation type", "op recordsOperation", "op string", "requires a function"},
		{"untyped operation", "op recordsOperation", "op any", "requires a function"},
		{"wrong error parameter", "err error)", "err string)", "requires a function"},
		{"missing function", "errors=translateRecordsError", "errors=missing", "requires a function"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			archive, destination := translationArchive(true)
			archive = strings.Replace(archive, tt.before, tt.after, 1)
			dir := writeModule(t, archive)
			writeTypedQuerySnapshot(t, dir)
			relocateQuerySnapshot(t, dir)
			code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", destination)
			if code == cli.ExitOK || !strings.Contains(errOut, tt.want) {
				t.Fatalf("generate = %d, want %q\n%s", code, tt.want, errOut)
			}
		})
	}
}

func TestCommand_ErrorTranslation(t *testing.T) {
	t.Parallel()
	for _, local := range []bool{false, true} {
		for _, driver := range []string{"pgx", "database/sql"} {
			name := driver + "/separate"
			if local {
				name = driver + "/local"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				archive, destination := translationArchive(local)
				pkg := "impl"
				if local {
					pkg = "repository"
				}
				if driver == "database/sql" {
					runtime := strings.Replace(typedQueryRuntime, "invalidState = true", `
    if translatedCalls != 0 { t.Fatal("translated a successful operation") }
    invalidState = true`, 1)
					runtime = strings.Replace(runtime,
						`if _, err := records.Get(ctx, 7); err == nil { t.Fatal("invalid enum was accepted") }`, `
    if _, err := records.Get(ctx, 7); err == nil || !strings.Contains(err.Error(), "translated Get") {
        t.Fatalf("Get translation: %v", err)
    }
    if translatedCalls != 1 || translatedOperation != recordsOperationGet {
        t.Fatalf("Get operation: %v, calls: %d", translatedOperation, translatedCalls)
    }
    if _, err := records.Lookup(ctx, 7); err == nil { t.Fatal("Lookup error was not translated") }
    if translatedCalls != 2 || translatedOperation != recordsOperationLookup {
        t.Fatalf("Lookup operation: %v, calls: %d", translatedOperation, translatedCalls)
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

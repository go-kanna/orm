package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"
)

func TestCommand_HandWrittenVerbs(t *testing.T) {
	t.Parallel()
	const lookup, save, states = "Lookup(ctx context.Context, id int64) (model.Record, error)",
		"Save(ctx context.Context, record model.Record) (int64, error)", "States(ctx context.Context) ([]model.State, error)"
	for _, tt := range []struct {
		name, method, declared, want string
	}{
		{"a Get of one row", lookup, "GetRecord(ctx context.Context, id int64) (model.Record, error)", ""},
		{"a Find of one row", lookup, "FindRecord(ctx context.Context, id int64) (*model.Record, error)", ""},
		{"a List of rows", lookup, "ListRecords(ctx context.Context, id int64) ([]model.Record, error)", ""},
		{"a Get of rows", lookup, "GetRecords(ctx context.Context, id int64) ([]model.Record, error)",
			"GetRecords: a Get returns one row, as (T, error), and orm.ErrNotFound for none, written by hand as generated"},
		{"a Find of a value", lookup, "FindRecord(ctx context.Context, id int64) (model.Record, error)",
			"FindRecord: a Find returns one row, as (*T, error), and nil for none"},
		{"a List of one row", lookup, "ListRecord(ctx context.Context, id int64) (model.Record, error)",
			"ListRecord: a List returns every row, as ([]T, error)"},
		{"a Count of another type", states, "CountStates(ctx context.Context) (model.State, error)",
			"CountStates: a Count returns the one column of its result, as (int64, error)"},
		{"an Insert returning a column as a count", save,
			"InsertRecord(ctx context.Context, record model.Record) (int64, error)",
			"InsertRecord: an Insert returns error alone, (int64, error) for the number of rows it affects"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			old, _, _ := strings.Cut(tt.method, "(")
			name, _, _ := strings.Cut(tt.declared, "(")
			archive := strings.Replace(typedQueryShop, tt.method, tt.declared, 1)
			archive = strings.Replace(archive, "-- query: "+old+"\n", "-- query: "+name+"\n", 1)
			dir := writeModule(t, archive)
			writeTypedQuerySnapshot(t, dir)
			path := filepath.Join(dir, "kanna-orm.snapshot.json")
			data, err := os.ReadFile(path) //nolint:gosec // snapshot written in this test's module
			if err != nil {
				t.Fatal(err)
			}
			data = []byte(strings.ReplaceAll(string(data), "impl."+old+`"`, "impl."+name+`"`))
			if err := os.WriteFile(path, data, 0o600); err != nil { //nolint:gosec // test module snapshot
				t.Fatal(err)
			}
			code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", "../impl")
			if tt.want == "" && code != cli.ExitOK || tt.want != "" && (code == cli.ExitOK ||
				!strings.Contains(errOut, tt.want)) {
				t.Fatalf("generate = %d, want %q\n%s", code, tt.want, errOut)
			}
		})
	}
}

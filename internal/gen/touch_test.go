package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

func TestCommand_TouchDiagnostics(t *testing.T) {
	t.Parallel()
	const touch = "TouchByName(ctx context.Context, name string, s ...scopes.Note) error"
	for _, tc := range []struct {
		name, old, method, want string
	}{
		{"no update time kanna-orm writes", "GetByKind(ctx context.Context, kind string) (model.Event, error)",
			"TouchByKind(ctx context.Context, kind string, s ...scopes.Event) error",
			"TouchByKind: a Touch writes the time a row is updated at alone, and kanna-orm writes no such time of " +
				"model.Event\n" + `  give it an UpdatedAt time.Time, or tag the field orm:",updated"`},
		{"a row returned", touch, "TouchByName(ctx context.Context, name string, s ...scopes.Note) (model.Note, error)",
			"it returns model.Note, and a Touch returns int64 of the rows its scopes select, or error alone"},
		{"a row taken", touch, "TouchNote(ctx context.Context, n model.Note) error",
			"it takes n, rows to write, and a Touch writes none"},
		{"a change set taken", touch, "TouchByName(ctx context.Context, name string, c scopes.NoteChangeSet) error",
			"TouchByName: a Touch writes the update time alone and takes no change set"},
		{"one row of no unique key", touch, "TouchByBody(ctx context.Context, body string, s ...scopes.Note) error",
			"a Touch of one row requires a unique key; use (int64, error) for multiple rows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			archive := strings.NewReplacer("PARAM1", "$1", "PARAM2", "$2").Replace(auditShop)
			if !strings.Contains(archive, tc.old) {
				t.Fatalf("auditShop declares no %s", tc.old)
			}
			dir := writeModule(t, strings.Replace(archive, tc.old, tc.old+"\n\t"+tc.method, 1))
			s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: auditTables(snapshot.PostgreSQL)}
			if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
				t.Fatal(err)
			}
			if code, _, out := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
				t.Fatalf("scopes: %d, %s", code, out)
			}
			code, _, out := run(t, filepath.Join(dir, "repository"), "-destination", "../impl", "-driver", "database/sql")
			if code == cli.ExitOK || !strings.Contains(out, tc.want) {
				t.Fatalf("generate = %d, want %q\n%s", code, tc.want, out)
			}
		})
	}
}

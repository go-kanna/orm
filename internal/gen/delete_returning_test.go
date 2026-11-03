package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

func TestCommand_DeleteReturningDiagnostics(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, method, want string
		dialect            snapshot.Dialect
	}{
		{"mysql model", "Delete(ctx context.Context, s ...scopes.Record) ([]model.Record, error)",
			"MySQL has no DELETE RETURNING", snapshot.MySQL},
		{"mysql projection", "Delete(ctx context.Context, s ...scopes.Record) ([]model.Card, error)",
			"MySQL has no DELETE RETURNING", snapshot.MySQL},
		{"single result", "Delete(ctx context.Context, id int64, s ...scopes.Record) (model.Card, error)",
			"kanna-orm cannot implement it as declared", snapshot.PostgreSQL},
		{"invalid projection", "Delete(ctx context.Context, s ...scopes.Record) ([]model.Card, error)",
			"no column missing", snapshot.PostgreSQL},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			archive := strings.Replace(projectionShop(), projectionMethods, tt.method, 1)
			if tt.name == "invalid projection" {
				archive = strings.Replace(archive, "Caption string\n}", "Missing string\n}", 1)
			}
			dir := writeModule(t, archive)
			writeTypedQuerySnapshot(t, dir)
			if tt.dialect == snapshot.MySQL {
				path := filepath.Join(dir, snapshot.Name)
				data, err := os.ReadFile(path) //nolint:gosec // generated test snapshot
				if err != nil {
					t.Fatal(err)
				}
				s, err := snapshot.Unmarshal(data)
				if err != nil {
					t.Fatal(err)
				}
				s.Dialect = tt.dialect
				if err := os.WriteFile(path, snapshot.Marshal(s), 0o600); err != nil { //nolint:gosec // generated test snapshot
					t.Fatal(err)
				}
			}
			code, _, out := run(t, filepath.Join(dir, "repository"), "-destination", "../impl")
			if code != cli.ExitFailure || !strings.Contains(out, tt.want) {
				t.Fatalf("generate = %d\n%s\nwant %s", code, out, tt.want)
			}
		})
	}
}

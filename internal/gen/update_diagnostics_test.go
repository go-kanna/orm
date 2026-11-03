package gen_test

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

func TestCommand_UpdateChangesDiagnostics(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, method, want string
		dialect            snapshot.Dialect
	}{
		{"missing unique key", "UpdateFields(ctx context.Context, ownerID *int64, c scopes.RecordChangeSet) error",
			"single-row Update requires a unique key", snapshot.PostgreSQL},
		{"neither keys nor scopes", "UpdateEvery(ctx context.Context, c scopes.RecordChangeSet) (int64, error)",
			"an Update of a change set writes the rows its keys or scopes select, but it takes neither", snapshot.PostgreSQL},
		{"neither keys nor scopes, error alone", "UpdateFields(ctx context.Context, c scopes.RecordChangeSet) error",
			"but it takes neither", snapshot.PostgreSQL},
		{"row and changes", "UpdateFields(ctx context.Context, r model.Record, c scopes.RecordChangeSet) error",
			"change set is accepted only once", snapshot.PostgreSQL},
		{"wrong verb", "InsertFields(ctx context.Context, c scopes.RecordChangeSet) error",
			"change set is accepted only once", snapshot.PostgreSQL},
		{"mysql returning", "UpdateFields(ctx context.Context, id int64, c scopes.RecordChangeSet) (model.Record,error)",
			"MySQL has no UPDATE RETURNING", snapshot.MySQL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			archive := strings.Replace(writeOptionsShop, `"example.com/shop/model"`+"\n)",
				`"example.com/shop/model"`+"\n\"example.com/shop/scopes\"\n)", 1)
			archive = strings.Replace(archive, "Update(ctx context.Context, record model.Record) error", tc.method, 1)
			dir := writeModule(t, archive)
			writeOptionsSnapshot(t, dir, tc.dialect)
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

func TestCommand_UpdateChangesFirstRun(t *testing.T) {
	t.Parallel()
	for _, returning := range []bool{false, true} {
		t.Run(strconv.FormatBool(returning), func(t *testing.T) {
			t.Parallel()
			archive := strings.Replace(writeOptionsShop, `"example.com/shop/model"
)`, `"example.com/shop/model"
"example.com/shop/scopes"
)`, 1)
			result := "error"
			if returning {
				result = "(model.Record,error)"
			}
			archive = strings.Replace(archive, "Update(ctx context.Context, record model.Record) error",
				"Update(ctx context.Context,id int64,c scopes.RecordChangeSet,s ...scopes.Record) "+result, 1)
			dir := writeModule(t, archive)
			writeOptionsSnapshot(t, dir, snapshot.PostgreSQL)
			code, _, out := run(t, filepath.Join(dir, "repository"), "-destination", "../impl", "-driver", "database/sql")
			if code != cli.ExitOK {
				t.Fatalf("repository before scopes: %d, %s", code, out)
			}
			if code, _, out := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
				t.Fatalf("scopes: %d, %s", code, out)
			}
			moduleCommand(t, dir, "test", "-mod=mod", "./...")
		})
	}
}

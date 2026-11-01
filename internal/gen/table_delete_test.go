package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

const tableDeleteShop = `-- repository/repository.go --
package repository
import "context"
type Platform string
//kanna:repository table=devices
type Devices interface {
	Delete(ctx context.Context, id int64) error
	DeleteByToken(ctx context.Context, platform Platform, pushToken string) error
	DeleteByPlatform(ctx context.Context, platform Platform) (int64, error)
}
`

func writeTableDeleteSnapshot(t *testing.T, dir string, dialect snapshot.Dialect) {
	t.Helper()
	s := snapshot.Snapshot{Dialect: dialect, Tables: map[string]snapshot.Table{
		"devices": {
			Columns: []snapshot.Column{{Name: "id", Type: "bigint", NotNull: true},
				{Name: "platform", Type: "text", NotNull: true}, {Name: "push_token", Type: "text", NotNull: true}},
			PrimaryKey: []string{"id"}, UniqueKeys: [][]string{{"platform", "push_token"}},
		},
	}}
	if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCommand_TableDelete(t *testing.T) {
	t.Parallel()
	for _, dialect := range []snapshot.Dialect{snapshot.PostgreSQL, snapshot.MySQL} {
		for _, driver := range []string{"pgx", "database/sql"} {
			if dialect == snapshot.MySQL && driver == "pgx" {
				continue
			}
			t.Run(string(dialect)+"/"+driver, func(t *testing.T) {
				t.Parallel()
				dir := writeModule(t, tableDeleteShop)
				writeTableDeleteSnapshot(t, dir, dialect)
				code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", ".", "-driver", driver)
				if code != cli.ExitOK {
					t.Fatalf("generate = %d\n%s", code, errOut)
				}
				moduleCommand(t, dir, "test", "-mod=mod", "./...")
			})
		}
	}
}

func TestCommand_TableDeleteMistakes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, before, after, want string }{
		{"unknown column", "id int64", "unknown int64", "has no column for the parameter unknown"},
		{"wrong type", "id int64", "id bool", "cannot hold devices.id"},
		{"incomplete unique key", "platform Platform, pushToken string", "platform Platform", "so Delete would delete more"},
		{"unrestricted delete", "id int64", "", "so Delete would delete more"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			archive := strings.Replace(tableDeleteShop, tt.before, tt.after, 1)
			archive = strings.ReplaceAll(archive, "ctx context.Context, )", "ctx context.Context)")
			dir := writeModule(t, archive)
			writeTableDeleteSnapshot(t, dir, snapshot.PostgreSQL)
			code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", ".")
			if code == cli.ExitOK || !strings.Contains(errOut, tt.want) {
				t.Fatalf("generate = %d, want %q\n%s", code, tt.want, errOut)
			}
		})
	}
}

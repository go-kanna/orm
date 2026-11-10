package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

// docShop declares docs, whose body, a bytea, a string holds, and a repository reading them.
const docShop = `
-- model/model.go --
package model

type Doc struct {
	ID   int64
	Body string
	Name string
}
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=docs
type Doc struct{ q scope.Of[model.Doc] }
-- repository/repository.go --
package repository

import (
	"context"

	"example.com/shop/model"
)

//kanna:repository
type Doc interface {
	Get(ctx context.Context, id int64) (model.Doc, error)
}
`

// pgx reads a bytea in binary, which it scans into no string, though database/sql does: the run generating the
// repositories on pgx stops a string holding one, and the run generating the scope types, on no driver, lets it pass.
func TestCommand_BytesOnPgx(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		driver string
		want   string
	}{
		{"pgx", "model.go:5:1: error: Doc.Body: the column body is bytea, which pgx reads into no string\n" +
			"  declare it as []byte, or as a type of your own with Scan and Value methods"},
		{"database/sql", ""},
	} {
		t.Run(tt.driver, func(t *testing.T) {
			t.Parallel()

			dir := writeModule(t, docShop)
			s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
				"docs": {Columns: []snapshot.Column{
					{Name: "id", Type: "int8", NotNull: true},
					{Name: "body", Type: "bytea", NotNull: true},
					{Name: "name", Type: "public.citext", NotNull: true},
				}, PrimaryKey: []string{"id"}},
			}}
			if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
				t.Fatal(err)
			}
			if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
				t.Fatalf("kanna-orm in scopes = %d\n%s", code, errOut)
			}
			code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", "../impl", "-driver", tt.driver)
			if tt.want == "" {
				if code != cli.ExitOK {
					t.Fatalf("kanna-orm = %d\n%s", code, errOut)
				}
				return
			}
			if code == cli.ExitOK || !strings.Contains(errOut, tt.want) {
				t.Errorf("kanna-orm = %d\n%s\nwant it to report\n%s", code, errOut, tt.want)
			}
		})
	}
}

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

// flagShop declares flags, whose enabled, a bit of MySQL, a field of type FIELD holds, and a repository reading them.
const flagShop = `
-- model/model.go --
package model

import "database/sql"

var _ sql.NullBool

type Flag struct {
	ID      int64
	Enabled FIELD
}
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=flags
type Flag struct{ q scope.Of[model.Flag] }
-- repository/repository.go --
package repository

import (
	"context"

	"example.com/shop/model"
)

//kanna:repository
type Flag interface {
	Get(ctx context.Context, id int64) (model.Flag, error)
}
`

// go-sql-driver/mysql reads a bit as bytes, which it converts to no bool: a field holding a bit of MySQL is a []byte,
// or a type with a Scan method.
func TestCommand_BitOnMySQL(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		field string
		want  string
	}{
		{"bool", "model.go:9:1: error: Flag.Enabled: flags.enabled is bit(1), which a bool cannot hold\n" +
			"  declare it as []byte, or as a type of your own with Scan and Value methods\n" +
			"  for a boolean, make the column tinyint(1), which a bool holds"},
		{"[]byte", ""},
		{"sql.NullBool", ""},
	} {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()

			dir := writeModule(t, strings.Replace(flagShop, "FIELD", tt.field, 1))
			s := snapshot.Snapshot{Dialect: snapshot.MySQL, Tables: map[string]snapshot.Table{
				"flags": {Columns: []snapshot.Column{
					{Name: "id", Type: "bigint", NotNull: true},
					{Name: "enabled", Type: "bit(1)", NotNull: true},
				}, PrimaryKey: []string{"id"}},
			}}
			if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
				t.Fatal(err)
			}
			code, _, errOut := run(t, filepath.Join(dir, "scopes"))
			if tt.want == "" {
				if code != cli.ExitOK {
					t.Fatalf("kanna-orm in scopes = %d\n%s", code, errOut)
				}
				code, _, errOut = run(t, filepath.Join(dir, "repository"), "-destination", "../impl")
				if code != cli.ExitOK {
					t.Fatalf("kanna-orm = %d\n%s", code, errOut)
				}
				return
			}
			if code == cli.ExitOK || !strings.Contains(errOut, tt.want) {
				t.Errorf("kanna-orm in scopes = %d\n%s\nwant it to report\n%s", code, errOut, tt.want)
			}
		})
	}
}

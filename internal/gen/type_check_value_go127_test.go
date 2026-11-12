//go:build go1.27

package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
	"github.com/go-kanna/orm/internal/sqlfile"
)

// valueShop declares notes, whose id a field of type FIELD, or OWN in its own package, holds; value types shaped
// as kanna-value writes them, NoteID holding a uuid.UUID and Code a string; a type RawID with Scan and Value methods of
// its own; and a repository reading the notes, and the rows of valueQuery, NoteRow, by an id of type PARAM.
const valueShop = `
-- model/model.go --
package model

import (
	"database/sql"
	"database/sql/driver"
	"uuid"
)

type NoteID struct{ id uuid.UUID }

func (x NoteID) UUID() uuid.UUID { return x.id }

func (x NoteID) Value() (driver.Value, error) { return driver.DefaultParameterConverter.ConvertValue(x.id) }

func (x *NoteID) Scan(src any) error {
	var v sql.Null[uuid.UUID]
	if err := v.Scan(src); err != nil {
		return err
	}
	x.id = v.V
	return nil
}

type Code struct{ s string }

func (x Code) String() string { return x.s }

func (x Code) Value() (driver.Value, error) { return x.s, nil }

func (x *Code) Scan(src any) error {
	var v sql.Null[string]
	if err := v.Scan(src); err != nil {
		return err
	}
	x.s = v.V
	return nil
}

type RawID struct{ b []byte }

func (x RawID) Value() (driver.Value, error) { return x.b, nil }

func (x *RawID) Scan(src any) error {
	b, _ := src.([]byte)
	x.b = append([]byte(nil), b...)
	return nil
}

type Note struct {
	ID   OWN
	Body string
}
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=notes
type Note struct{ q scope.Of[model.Note] }
-- repository/repository.go --
package repository

import (
	"context"

	"example.com/shop/model"
)

type NoteRow struct {
	ID   PARAM
	Body string
}

//kanna:repository
type Note interface {
	Get(ctx context.Context, id FIELD) (model.Note, error)
	Insert(ctx context.Context, n model.Note) error
	ByID(ctx context.Context, id PARAM) ([]NoteRow, error)
}
-- impl/queries.sql --
` + valueQuery

const valueQuery = `-- query: ByID
SELECT id, body FROM notes WHERE id = :id
`

// A value type of kanna-value writes and reads its column as the value it holds, so it holds a column only where that
// value does: a NoteID holding a uuid.UUID no binary(16) or char(36) of MySQL, in a field of a model, a parameter of
// a query written by hand, or a field of its rows, as a uuid.UUID does not. A type with Scan and Value methods of its
// own still holds any column.
func TestCommand_ValueType(t *testing.T) {
	t.Parallel()

	const (
		says      = " is a value type of kanna-value, which writes and reads its column as the uuid.UUID it holds"
		hint      = "  model.NoteID" + says
		fieldHint = "  NoteID" + says
	)
	for _, tt := range []struct {
		name         string
		dialect      snapshot.Dialect
		field, param string
		dbType       string
		want         []string
	}{
		{"a NoteID for a binary(16)", snapshot.MySQL, "model.NoteID", "[]byte", "binary(16)", []string{
			"model.go:50:2: error: Note.ID: notes.id is binary(16), which a NoteID cannot hold\n" +
				"  declare it as []byte, or as a type of your own with Scan and Value methods\n" + fieldHint,
		}},
		{"a NoteID for a char(36)", snapshot.MySQL, "model.NoteID", "[]byte", "char(36)", []string{
			"notes.id is char(36), which a NoteID cannot hold\n" +
				"  declare it as string, or as a type of your own with Scan and Value methods\n" + fieldHint,
		}},
		{"a parameter and a field of rows of a NoteID", snapshot.MySQL, "[]byte", "model.NoteID", "binary(16)",
			[]string{
				"error: -- query: ByID: :id is binary(16), which id, a model.NoteID, is not\n" +
					"  declare it as []byte\n" + hint,
				"error: -- query: ByID: the column id is binary(16), which repository.NoteRow.ID, a model.NoteID, " +
					"cannot hold\n  declare it as []byte\n" + hint,
			}},
		{"a Code for a char(36)", snapshot.MySQL, "model.Code", "model.Code", "char(36)", nil},
		{"a RawID for a binary(16)", snapshot.MySQL, "model.RawID", "model.RawID", "binary(16)", nil},
		{"a NoteID for a uuid", snapshot.PostgreSQL, "model.NoteID", "model.NoteID", "uuid", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			archive := strings.NewReplacer("FIELD", tt.field, "OWN", strings.TrimPrefix(tt.field, "model."), "PARAM",
				tt.param).Replace(valueShop)
			dir := writeModuleGo(t, archive, "1.27.0")
			body := "text"
			stmts, _ := sqlfile.Parse("queries.sql", []byte(valueQuery))
			s := snapshot.Snapshot{Dialect: tt.dialect, Tables: map[string]snapshot.Table{
				"notes": {Columns: []snapshot.Column{
					{Name: "id", Type: tt.dbType, NotNull: true},
					{Name: "body", Type: body, NotNull: true},
				}, PrimaryKey: []string{"id"}},
			}, Statements: map[string]snapshot.Statement{
				"example.com/shop/impl.ByID": {
					Digest: snapshot.Digest(stmts[0].Text),
					Params: []snapshot.Param{{Name: "id", Type: tt.dbType}},
					Columns: []snapshot.OutputColumn{
						{Name: "id", Type: tt.dbType, NotNull: true},
						{Name: "body", Type: body, NotNull: true},
					},
				},
			}}
			if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
				t.Fatal(err)
			}
			code, _, errOut := run(t, filepath.Join(dir, "scopes"))
			if code == cli.ExitOK {
				code, _, errOut = run(t, filepath.Join(dir, "repository"), "-destination", "../impl")
			}
			if tt.want == nil {
				if code != cli.ExitOK {
					t.Fatalf("kanna-orm = %d\n%s", code, errOut)
				}
				return
			}
			for _, want := range tt.want {
				if code == cli.ExitOK || !strings.Contains(errOut, want) {
					t.Errorf("kanna-orm = %d\n%s\nwant it to report\n%s", code, errOut, want)
				}
			}
		})
	}
}

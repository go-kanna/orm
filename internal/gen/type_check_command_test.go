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
		{"pgx", "model.go:5:2: error: Doc.Body: the column body is bytea, which pgx reads into no string\n" +
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

// flagShop declares flags, whose enabled, a bit, a field of type FIELD holds, and a repository reading them.
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
		{"bool", "model.go:9:2: error: Flag.Enabled: flags.enabled is bit(1), which a bool cannot hold\n" +
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

// pgx reads PostgreSQL's bit and varbit into no type but one with a Scan method, and database/sql reads them as text,
// into a string or a []byte: a bool or an integer, which reads text as another value, stops generation on either.
func TestCommand_BitOnPostgreSQL(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		driver, field, column string
		want                  string
	}{
		{"pgx", "bool", "bit(1)", "model.go:9:2: error: Flag.Enabled: flags.enabled is bit(1), which a bool cannot hold\n" +
			"  declare it as string, or as a type of your own with Scan and Value methods\n" +
			"  pgx reads it into no string or []byte, but into a type with a Scan method, such as pgtype.Bits"},
		{"pgx", "string", "varbit", "model.go:9:2: error: Flag.Enabled: the column enabled is varbit, which pgx reads " +
			"into no string\n  declare it as pgtype.Bits, or as a type of your own with Scan and Value methods"},
		{"pgx", "sql.NullString", "bit(8)", ""},
		{"database/sql", "int64", "bit(8)", "flags.enabled is bit(8), which an int64 cannot hold"},
		{"database/sql", "string", "bit(8)", ""},
		{"database/sql", "[]byte", "varbit", ""},
	} {
		t.Run(tt.driver+" "+tt.field+" "+tt.column, func(t *testing.T) {
			t.Parallel()

			dir := writeModule(t, strings.Replace(flagShop, "FIELD", tt.field, 1))
			s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
				"flags": {Columns: []snapshot.Column{
					{Name: "id", Type: "int8", NotNull: true},
					{Name: "enabled", Type: tt.column, NotNull: true},
				}, PrimaryKey: []string{"id"}},
			}}
			if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
				t.Fatal(err)
			}
			code, _, errOut := run(t, filepath.Join(dir, "scopes"))
			if code == cli.ExitOK {
				code, _, errOut = run(t, filepath.Join(dir, "repository"), "-destination", "../impl", "-driver", tt.driver)
			}
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

// jsonShop declares notes, whose tags, a json, a field of type FIELD holds; Status, a type of string, and Count and
// At, value types shaped as kanna-value writes them holding an int64 and a time.Time; and a repository reading the
// notes, and the tags alone into ROW by tagsQuery and into NoteTags, a projection.
const jsonShop = `
-- model/model.go --
package model

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"time"
)

var (
	_ json.RawMessage
	_ time.Time
)

type Status string

type Count struct{ n int64 }

func (x Count) Int64() int64 { return x.n }

func (x Count) Value() (driver.Value, error) { return x.n, nil }

func (x *Count) Scan(src any) error {
	var v sql.Null[int64]
	if err := v.Scan(src); err != nil {
		return err
	}
	x.n = v.V
	return nil
}

type At struct{ t time.Time }

func NewAt(t time.Time) (At, error) { return At{t: t}, nil }

func (x At) Time() time.Time { return x.t }

func (x At) Value() (driver.Value, error) { return x.t, nil }

func (x *At) Scan(src any) error {
	var v sql.Null[time.Time]
	if err := v.Scan(src); err != nil {
		return err
	}
	x.t = v.V
	return nil
}

type Note struct {
	ID   int64
	Tags FIELD
}

type NoteTags struct{ Tags FIELD }
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
	"encoding/json"
	"time"

	"example.com/shop/model"
	"example.com/shop/scopes"
)

var (
	_ json.RawMessage
	_ time.Time
	_ model.Status
)

//kanna:repository
type Note interface {
	Get(ctx context.Context, id int64) (model.Note, error)
	Insert(ctx context.Context, n model.Note) error
	Tags(ctx context.Context) ([]ROW, error)
	ListTags(ctx context.Context, s ...scopes.Note) ([]model.NoteTags, error)
}
-- impl/queries.sql --
` + tagsQuery

const tagsQuery = `-- query: Tags
SELECT tags FROM notes
`

// go-sql-driver/mysql writes json from a string or a []byte alone, and database/sql reads it into no slice and no
// time.Time, so a field of a model holding a json of MySQL in another type, or in a value type holding one, stops
// generation. A number or a bool still holds the json a query written by hand reads, as database/sql reads a json
// holding one into it. pgx, on its own or under database/sql, writes and reads a slice, a number and a bool as JSON,
// and a time.Time on its own, which pgx/stdlib hands database/sql to read as text, as pgx hands a Scan method.
func TestCommand_JSON(t *testing.T) {
	t.Parallel()

	const hint = "  go-sql-driver/mysql writes json from a string or a []byte, such as json.RawMessage, alone"
	for _, tt := range []struct {
		dialect    snapshot.Dialect
		driver     string
		field, row string
		dbCol      string
		want       string
	}{
		{snapshot.MySQL, "", "[]int64", "[]byte", "json", "model.go:51:2: error: Note.Tags: notes.tags is json, " +
			"which a []int64 cannot hold\n  declare it as []byte, or as a type of your own with Scan and Value " +
			"methods\n" + hint},
		{snapshot.MySQL, "", "[]string", "[]byte", "json", "notes.tags is json, which a []string cannot hold"},
		{snapshot.MySQL, "", "int64", "int64", "json", "model.go:51:2: error: Note.Tags: notes.tags is json, " +
			"which an int64 cannot hold\n  declare it as []byte, or as a type of your own with Scan and Value " +
			"methods\n" + hint},
		{snapshot.MySQL, "", "*float64", "float64", "json", "notes.tags is json, which a *float64 cannot hold\n" +
			"  declare it as *[]byte"},
		{snapshot.MySQL, "", "bool", "bool", "json", "notes.tags is json, which a bool cannot hold"},
		{snapshot.MySQL, "", "Count", "[]byte", "json", "notes.tags is json, which a Count cannot hold\n" +
			"  declare it as []byte, or as a type of your own with Scan and Value methods\n" + hint + "\n" +
			"  Count is a value type of kanna-value, which writes and reads its column as the int64 it holds"},
		{snapshot.MySQL, "", "time.Time", "[]byte", "json", "notes.tags is json, which a time.Time cannot hold"},
		{snapshot.MySQL, "", "string", "time.Time", "json", "queries.sql:1:1: error: Tags: the scalar result cannot " +
			"hold column tags (json)"},
		{snapshot.MySQL, "", "json.RawMessage", "json.RawMessage", "json", ""},
		{snapshot.MySQL, "", "[]byte", "[]byte", "json", ""},
		{snapshot.MySQL, "", "string", "string", "json", ""},
		{snapshot.MySQL, "", "Status", "model.Status", "json", ""},
		{snapshot.MySQL, "", "string", "int64", "json", ""},
		{snapshot.MySQL, "", "string", "bool", "json", ""},
		{snapshot.MySQL, "", "int64", "int64", "bigint", ""},
		{snapshot.MySQL, "", "[]string", "[]byte", "text", "notes.tags is text, which a []string cannot hold"},
		{snapshot.PostgreSQL, "pgx", "[]int64", "[]int64", "jsonb", ""},
		{snapshot.PostgreSQL, "pgx", "int64", "float64", "jsonb", ""},
		{snapshot.PostgreSQL, "database/sql", "bool", "bool", "json", ""},
		{snapshot.PostgreSQL, "database/sql", "[]int64", "[]int64", "jsonb", ""},
		{snapshot.PostgreSQL, "database/sql", "[]string", "[]string", "json", ""},
		{snapshot.PostgreSQL, "pgx", "time.Time", "time.Time", "jsonb", ""},
		{snapshot.PostgreSQL, "pgx", "*time.Time", "time.Time", "json", ""},
		{snapshot.PostgreSQL, "database/sql", "time.Time", "string", "jsonb", "model.go:51:2: error: Note.Tags: " +
			"the column tags is jsonb, which database/sql reads into no time.Time\n  declare it as []byte, or as a " +
			"type of your own with Scan and Value methods"},
		{snapshot.PostgreSQL, "database/sql", "time.Time", "string", "json", "model.go:54:23: error: NoteTags.Tags: " +
			"the column tags is json, which database/sql reads into no time.Time"},
		{snapshot.PostgreSQL, "database/sql", "*time.Time", "string", "json", "Note.Tags: the column tags is json, " +
			"which database/sql reads into no *time.Time\n  declare it as *[]byte"},
		{snapshot.PostgreSQL, "database/sql", "string", "time.Time", "json", "queries.sql:1:1: error: Tags: the " +
			"scalar result cannot hold column tags (json)"},
		{snapshot.PostgreSQL, "database/sql", "string", "struct{ Tags time.Time }", "json", "queries.sql:1:1: " +
			"error: -- query: Tags: the column tags is json, which struct{Tags time.Time}.Tags, a time.Time, cannot hold\n" +
			"  declare it as []byte"},
		{snapshot.PostgreSQL, "pgx", "string", "struct{ Tags time.Time }", "json", ""},
		{snapshot.PostgreSQL, "pgx", "At", "[]byte", "jsonb", "notes.tags is jsonb, which an At cannot hold\n" +
			"  declare it as []byte, or as a type of your own with Scan and Value methods\n" +
			"  At is a value type of kanna-value, which writes and reads its column as the time.Time it holds"},
		{snapshot.PostgreSQL, "database/sql", "Count", "model.Count", "json", ""},
	} {
		t.Run(string(tt.dialect)+" "+tt.driver+" "+tt.field+" "+tt.row+" "+tt.dbCol, func(t *testing.T) {
			t.Parallel()

			dir := writeModule(t, strings.NewReplacer("FIELD", tt.field, "ROW", tt.row).Replace(jsonShop))
			id := "int8"
			if tt.dialect == snapshot.MySQL {
				id = "bigint"
			}
			stmts, _ := sqlfile.Parse("queries.sql", []byte(tagsQuery))
			s := snapshot.Snapshot{Dialect: tt.dialect, Tables: map[string]snapshot.Table{
				"notes": {Columns: []snapshot.Column{
					{Name: "id", Type: id, NotNull: true},
					{Name: "tags", Type: tt.dbCol, NotNull: true},
				}, PrimaryKey: []string{"id"}},
			}, Statements: map[string]snapshot.Statement{
				"example.com/shop/impl.Tags": {
					Digest:  snapshot.Digest(stmts[0].Text),
					Columns: []snapshot.OutputColumn{{Name: "tags", Type: tt.dbCol, NotNull: true}},
				},
			}}
			if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
				t.Fatal(err)
			}
			code, _, errOut := run(t, filepath.Join(dir, "scopes"))
			if code == cli.ExitOK {
				args := []string{"-destination", "../impl"}
				if tt.driver != "" {
					args = append(args, "-driver", tt.driver)
				}
				code, _, errOut = run(t, filepath.Join(dir, "repository"), args...)
			}
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

// jsonEnumShop declares notes, whose tags, a json, a field of type FIELD holds; Level, an enum of kanna-value; Status,
// a type of string unmarked; Grade, a value type holding a Level; and a repository reading the tags alone into ROW by
// a query, into NoteTags, a projection, and writing them from a parameter of type PARAM.
const jsonEnumShop = `
-- model/model.go --
package model

import (
	"database/sql"
	"database/sql/driver"
)

// Level is an enum.
//
//kanna:enum
type Level string

const LevelLow Level = "low"

func (x Level) Value() (driver.Value, error) { return string(x), nil }

func (x *Level) Scan(src any) error {
	var v sql.Null[string]
	if err := v.Scan(src); err != nil {
		return err
	}
	*x = Level(v.V)
	return nil
}

type (
	Status string

	//kanna:enum
	Kind string
)

const KindA Kind = "a"

type Grade struct{ l Level }

func NewGrade(l Level) (Grade, error) { return Grade{l: l}, nil }

func (x Grade) Level() Level { return x.l }

func (x Grade) Value() (driver.Value, error) { return string(x.l), nil }

func (x *Grade) Scan(src any) error { return x.l.Scan(src) }

type Note struct {
	ID   int64
	Tags FIELD
}

type NoteTags struct{ Tags FIELD }
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
	"example.com/shop/scopes"
)

var _ model.Status

//kanna:repository
type Note interface {
	Get(ctx context.Context, id int64) (model.Note, error)
	Tags(ctx context.Context) ([]ROW, error)
	SetTags(ctx context.Context, id int64, tags PARAM) error
	ListTags(ctx context.Context, s ...scopes.Note) ([]model.NoteTags, error)
}
-- impl/queries.sql --
` + enumTagsQuery + setTagsQuery

const (
	enumTagsQuery = `-- query: Tags
SELECT tags FROM notes
`
	setTagsQuery = `-- query: SetTags
UPDATE notes SET tags = :tags WHERE id = :id
`
)

// No value of an enum of kanna-value is JSON text, which MySQL and PostgreSQL reject in a json, nor is JSON text one,
// which its Scan rejects, so an enum, a pointer to one, or a value type holding one, stops generation wherever it holds
// a json, written or read alone. A type of string unmarked, which may hold JSON text, does not, nor does an enum
// holding text.
func TestCommand_JSONEnum(t *testing.T) {
	t.Parallel()

	hint := "  Level is an enum of kanna-value, none of whose values is JSON text: hold it in a column of text, such " +
		"as text or varchar"
	for _, tt := range []struct {
		dialect         snapshot.Dialect
		driver          string
		field, row, prm string
		dbCol           string
		want            string
	}{
		{snapshot.MySQL, "", "Level", "string", "string", "json", "model.go:47:2: error: Note.Tags: notes.tags " +
			"is json, which a Level cannot hold\n  declare it as []byte, or as a type of your own with Scan and Value " +
			"methods\n  go-sql-driver/mysql writes json from a string or a []byte, such as json.RawMessage, alone\n" +
			hint},
		{snapshot.PostgreSQL, "pgx", "*Level", "string", "string", "jsonb", "Note.Tags: notes.tags is jsonb, " +
			"which a *Level cannot hold\n  declare it as *[]byte, or as a type of your own with Scan and Value " +
			"methods\n" + hint},
		{snapshot.PostgreSQL, "database/sql", "Grade", "string", "string", "json", "Note.Tags: notes.tags is json, " +
			"which a Grade cannot hold\n  declare it as []byte, or as a type of your own with Scan and Value methods\n" +
			"  Grade is a value type of kanna-value, which writes and reads its column as the Level it holds\n" + hint},
		{snapshot.PostgreSQL, "pgx", "Kind", "string", "string", "json", "notes.tags is json, which a Kind cannot " +
			"hold"},
		{snapshot.PostgreSQL, "pgx", "string", "model.Level", "string", "json", "queries.sql:1:1: error: Tags: the " +
			"scalar result cannot hold column tags (json)\n  model.Level is an enum of kanna-value"},
		{snapshot.PostgreSQL, "database/sql", "string", "struct{ Tags model.Level }", "string", "jsonb",
			"queries.sql:1:1: error: -- query: Tags: the column tags is jsonb, which struct{Tags model.Level}.Tags, a " +
				"model.Level, cannot hold\n  declare it as []byte\n  model.Level is an enum of kanna-value"},
		{snapshot.PostgreSQL, "pgx", "string", "string", "*model.Level", "jsonb", "queries.sql:3:1: error: " +
			"-- query: SetTags: :tags is jsonb, which tags, a *model.Level, is not\n  declare it as []byte\n  " +
			"model.Level is an enum of kanna-value"},
		{snapshot.PostgreSQL, "pgx", "Status", "model.Status", "model.Status", "jsonb", ""},
		{snapshot.MySQL, "", "Status", "model.Status", "model.Status", "json", ""},
		{snapshot.PostgreSQL, "pgx", "Level", "model.Level", "model.Grade", "text", ""},
		{snapshot.MySQL, "", "Grade", "model.Kind", "model.Level", "varchar(16)", ""},
	} {
		t.Run(string(tt.dialect)+" "+tt.driver+" "+tt.field+" "+tt.row+" "+tt.prm+" "+tt.dbCol,
			func(t *testing.T) {
				t.Parallel()

				dir := writeModule(t, strings.NewReplacer("FIELD", tt.field, "ROW", tt.row,
					"PARAM", tt.prm).Replace(jsonEnumShop))
				id := "int8"
				if tt.dialect == snapshot.MySQL {
					id = "bigint"
				}
				stmts, _ := sqlfile.Parse("queries.sql", []byte(enumTagsQuery+setTagsQuery))
				s := snapshot.Snapshot{Dialect: tt.dialect, Tables: map[string]snapshot.Table{
					"notes": {Columns: []snapshot.Column{
						{Name: "id", Type: id, NotNull: true},
						{Name: "tags", Type: tt.dbCol, NotNull: true},
					}, PrimaryKey: []string{"id"}},
				}, Statements: map[string]snapshot.Statement{
					"example.com/shop/impl.Tags": {
						Digest:  snapshot.Digest(stmts[0].Text),
						Columns: []snapshot.OutputColumn{{Name: "tags", Type: tt.dbCol, NotNull: true}},
					},
					"example.com/shop/impl.SetTags": {
						Digest: snapshot.Digest(stmts[1].Text),
						Params: []snapshot.Param{{Name: "tags", Type: tt.dbCol}, {Name: "id", Type: id}},
					},
				}}
				if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
					t.Fatal(err)
				}
				code, _, errOut := run(t, filepath.Join(dir, "scopes"))
				if code == cli.ExitOK {
					args := []string{"-destination", "../impl"}
					if tt.driver != "" {
						args = append(args, "-driver", tt.driver)
					}
					code, _, errOut = run(t, filepath.Join(dir, "repository"), args...)
				}
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

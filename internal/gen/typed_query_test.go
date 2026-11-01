package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

const typedQueryShop = `
-- model/model.go --
package model
import "fmt"
type State string
func (s *State) Scan(value any) error {
	v, ok := value.(string)
	if !ok || v != "active" { return fmt.Errorf("invalid state: %v", value) }
	*s = State(v)
	return nil
}
type Record struct {
	ID int64
	State State ` + "`orm:\"state,nullzero\"`" + `
	Note string ` + "`orm:\"note,nullzero\"`" + `
}
-- scopes/scopes.go --
package scopes
import (
	"example.com/shop/model"
	"github.com/go-kanna/orm"
)
//kanna:scope table=records
type Record struct { q orm.Of[model.Record] }
-- repository/repository.go --
package repository
import (
	"context"
	"example.com/shop/model"
	"example.com/shop/scopes"
)
//kanna:repository
type Records interface {
	Insert(ctx context.Context, record model.Record) error
	Get(ctx context.Context, id int64) (model.Record, error)
	List(ctx context.Context, s ...scopes.Record) ([]model.Record, error)
	Save(ctx context.Context, record model.Record) (int64, error)
	Lookup(ctx context.Context, id int64) (model.Record, error)
	States(ctx context.Context) ([]model.State, error)
}
-- impl/queries.sql --
-- query: Save
INSERT INTO records (state, note) VALUES (:state, :note) RETURNING id;
-- query: Lookup
SELECT id, state, note FROM records WHERE id = :id;
-- query: States
SELECT 'active' AS state;
`

const typedQueryRuntime = `package impl
import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"example.com/shop/model"
)
type fakeDriver struct{}
type fakeConn struct{}
type fakeRows struct { columns []string; values [][]driver.Value }
var lastArgs []driver.NamedValue
var invalidState bool
func (fakeDriver) Open(string) (driver.Conn, error) { return fakeConn{}, nil }
func (fakeConn) Close() error { return nil }
func (fakeConn) Begin() (driver.Tx, error) { return nil, errors.New("unused") }
func (fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (fakeConn) ExecContext(_ context.Context, _ string, args []driver.NamedValue) (driver.Result, error) {
	lastArgs = args
	return driver.RowsAffected(1), nil
}
func (fakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	lastArgs = args
	if strings.HasPrefix(query, "INSERT") { return &fakeRows{[]string{"id"}, [][]driver.Value{{int64(7)}}}, nil }
	if strings.Contains(query, "'active'") { return &fakeRows{[]string{"state"}, [][]driver.Value{{"active"}}}, nil }
	if invalidState {
		return &fakeRows{[]string{"id", "state", "note"}, [][]driver.Value{{int64(7), "invalid", nil}}}, nil
	}
	return &fakeRows{[]string{"id", "state", "note"}, [][]driver.Value{{int64(7), nil, nil}}}, nil
}
func (r *fakeRows) Columns() []string { return r.columns }
func (r *fakeRows) Close() error { return nil }
func (r *fakeRows) Next(values []driver.Value) error {
	if len(r.values) == 0 { return io.EOF }
	copy(values, r.values[0]); r.values = r.values[1:]
	return nil
}
func TestTypedQueries(t *testing.T) {
	sql.Register("typed-records", fakeDriver{})
	db, err := sql.Open("typed-records", "")
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = db.Close() })
	records := NewRecords(db)
	ctx := t.Context()
	if err := records.Insert(ctx, model.Record{}); err != nil { t.Fatal(err) }
	if len(lastArgs) != 2 || lastArgs[0].Value != nil || lastArgs[1].Value != nil {
		t.Fatalf("empty fields: %v", lastArgs)
	}
	id, err := records.Save(ctx, model.Record{State: "active", Note: "kept"})
	if err != nil || id != 7 { t.Fatalf("Save = %v, %v", id, err) }
	if lastArgs[0].Value != "active" || lastArgs[1].Value != "kept" { t.Fatalf("struct fields: %v", lastArgs) }
	for _, find := range []func(context.Context, int64) (model.Record, error){records.Get, records.Lookup} {
		row, err := find(ctx, 7)
		if err != nil || row.ID != 7 || row.State != "" || row.Note != "" { t.Fatalf("read NULL: %+v, %v", row, err) }
	}
	rows, err := records.List(ctx)
	if err != nil || len(rows) != 1 || rows[0].State != "" { t.Fatalf("List = %v, %v", rows, err) }
	states, err := records.States(ctx)
	if err != nil || len(states) != 1 || states[0] != "active" { t.Fatalf("States = %v, %v", states, err) }
	invalidState = true
	if _, err := records.Get(ctx, 7); err == nil { t.Fatal("invalid enum was accepted") }
}
`

func TestCommand_TypedQueries(t *testing.T) {
	t.Parallel()
	for _, driver := range []string{"pgx", "database/sql"} {
		t.Run(driver, func(t *testing.T) {
			t.Parallel()
			archive := typedQueryShop
			if driver == "database/sql" {
				archive += "\n-- impl/runtime_test.go --\n" + typedQueryRuntime
			}
			dir := writeModule(t, archive)
			writeTypedQuerySnapshot(t, dir)
			if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
				t.Fatalf("scopes = %d\n%s", code, errOut)
			}
			args := []string{"-destination", "../impl", "-driver", driver}
			for range 2 {
				if code, _, errOut := run(t, filepath.Join(dir, "repository"), args...); code != cli.ExitOK {
					t.Fatalf("repositories = %d\n%s", code, errOut)
				}
			}
			moduleCommand(t, dir, "test", "-mod=mod", "./...")
		})
	}
}

func TestCommand_TypedQueryMistakes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, before, after, want string
	}{
		{"ambiguous struct fields", "Save(ctx context.Context, record model.Record)",
			"Save(ctx context.Context, first model.Record, second model.Record)", "matches fields of multiple parameters"},
		{"nullzero on number", "Note string", "Note int64", "nullzero requires a string field"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := writeModule(t, strings.Replace(typedQueryShop, tt.before, tt.after, 1))
			writeTypedQuerySnapshot(t, dir)
			code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", "../impl")
			if code != cli.ExitFailure || !strings.Contains(errOut, tt.want) {
				t.Fatalf("kanna-orm = %d\n%s\nwant %s", code, errOut, tt.want)
			}
		})
	}
}

func writeTypedQuerySnapshot(t *testing.T, dir string) {
	t.Helper()
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"records": {Columns: []snapshot.Column{
			{Name: "id", Type: "int8", NotNull: true, Assigned: true},
			{Name: "state", Type: "text"}, {Name: "note", Type: "text"},
		}, PrimaryKey: []string{"id"}},
	}, Statements: map[string]snapshot.Statement{
		"example.com/shop/impl.Save": {
			Digest:  snapshot.Digest("INSERT INTO records (state, note) VALUES (:state, :note) RETURNING id;"),
			Params:  []snapshot.Param{{Name: "state", Type: "text"}, {Name: "note", Type: "text"}},
			Columns: []snapshot.Result{{Name: "id", Type: "int8", NotNull: true}},
		},
		"example.com/shop/impl.Lookup": {
			Digest: snapshot.Digest("SELECT id, state, note FROM records WHERE id = :id;"),
			Params: []snapshot.Param{{Name: "id", Type: "int8"}},
			Columns: []snapshot.Result{{Name: "id", Type: "int8", NotNull: true},
				{Name: "state", Type: "text", Nullable: true}, {Name: "note", Type: "text", Nullable: true}},
		},
		"example.com/shop/impl.States": {
			Digest:  snapshot.Digest("SELECT 'active' AS state;"),
			Columns: []snapshot.Result{{Name: "state", Type: "text"}},
		},
	}}
	if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

package gen_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

func TestCommand_Pluck(t *testing.T) {
	t.Parallel()
	for _, driver := range []string{"pgx", "database/sql"} {
		for _, dialect := range []snapshot.Dialect{snapshot.PostgreSQL, snapshot.MySQL} {
			if driver == "pgx" && dialect == snapshot.MySQL {
				continue
			}
			t.Run(driver+"/"+string(dialect), func(t *testing.T) {
				t.Parallel()
				archive := strings.Replace(typedQueryShop, "type Records interface {", `type Records interface {
	//kanna:pluck id
	IDs(ctx context.Context, s ...scopes.Record) ([]int64, error)
	//kanna:pluck state
	StatesOnly(ctx context.Context, s ...scopes.Record) ([]model.State, error)
	//kanna:pluck note
	Notes(ctx context.Context, s ...scopes.Record) ([]*string, error)
`, 1)
				if driver == "database/sql" {
					archive += "\n-- impl/pluck_test.go --\n" + pluckRuntime
				}
				dir := writeModule(t, archive)
				writeTypedQuerySnapshot(t, dir)
				path := filepath.Join(dir, snapshot.Name)
				data, err := os.ReadFile(path) //nolint:gosec // the snapshot of the test module
				if err != nil {
					t.Fatal(err)
				}
				var s snapshot.Snapshot
				if err := json.Unmarshal(data, &s); err != nil {
					t.Fatal(err)
				}
				s.Dialect = dialect
				if err := os.WriteFile(path, snapshot.Marshal(s), 0o600); err != nil {
					t.Fatal(err)
				}
				if code, _, stderr := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
					t.Fatalf("scopes: %d\n%s", code, stderr)
				}
				for range 2 {
					args := []string{"-destination", "../impl", "-driver", driver}
					if code, _, stderr := run(t, filepath.Join(dir, "repository"), args...); code != cli.ExitOK {
						t.Fatalf("repositories: %d\n%s", code, stderr)
					}
				}
				moduleCommand(t, dir, "test", "-mod=mod", "./...")
			})
		}
	}
}

func TestCommand_PluckMistakes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ method, want string }{
		{"//kanna:pluck missing\n Values(ctx context.Context, s ...scopes.Record) ([]int64, error)",
			"has no mapped column missing"},
		{"//kanna:pluck id\n Values(ctx context.Context, s ...scopes.Record) ([]bool, error)", "cannot hold column id"},
		{"//kanna:pluck id\n Values(ctx context.Context, s ...scopes.Record) (int64, error)",
			"cannot implement it as declared"},
		{"//kanna:pluck id\n Values(ctx context.Context, s ...scopes.Record) error", "cannot implement it as declared"},
		{"//kanna:pluck state\n Values(ctx context.Context, s ...scopes.Record) ([]string, error)", "may be NULL"},
		{"//kanna:pluck\n Values(ctx context.Context, s ...scopes.Record) ([]int64, error)", "takes exactly one column"},
		{"//kanna:pluck id,state\n Values(ctx context.Context, s ...scopes.Record) ([]int64, error)", "has no mapped column"},
		{"//kanna:pluck id state\n Values(ctx context.Context, s ...scopes.Record) ([]int64, error)",
			"takes exactly one column"},
		{"//kanna:pluck id\n //kanna:conflict id\n Values(ctx context.Context, s ...scopes.Record) ([]int64, error)",
			"conflict is for an Upsert"},
		{"//kanna:pluck id\n //kanna:lock update\n Values(ctx context.Context, s ...scopes.Record) ([]int64, error)", "lock"},
		{"//kanna:pluck state\n States(ctx context.Context) ([]model.State, error)", "cannot be combined"},
	} {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()
			archive := typedQueryShop
			if strings.Contains(tt.method, "States(ctx") {
				archive = strings.Replace(archive, "States(ctx context.Context) ([]model.State, error)", "", 1)
			}
			archive = strings.Replace(archive, "type Records interface {", "type Records interface {\n"+tt.method+"\n", 1)
			dir := writeModule(t, archive)
			writeTypedQuerySnapshot(t, dir)
			code, _, stderr := run(t, filepath.Join(dir, "repository"), "-destination", "../impl")
			if code != cli.ExitFailure || !strings.Contains(stderr, tt.want) {
				t.Fatalf("generate: %d\n%s\nwant %s", code, stderr, tt.want)
			}
		})
	}
}

const pluckRuntime = `package impl
import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"example.com/shop/scopes"
)
type pluckDriver struct{}
type pluckConn struct{}
type pluckRows struct { values []driver.Value }
var pluckQuery string
var pluckArgs []driver.NamedValue
var pluckMode string
func (pluckDriver) Open(string) (driver.Conn, error) { return pluckConn{}, nil }
func (pluckConn) Close() error { return nil }
func (pluckConn) Begin() (driver.Tx, error) { return nil, errors.New("unused") }
func (pluckConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (pluckConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	pluckQuery, pluckArgs = q, args
	if pluckMode == "query error" { return nil, errors.New("query failed") }
	if pluckMode == "empty" { return &pluckRows{}, nil }
	if strings.HasPrefix(q, "SELECT id ") { return &pluckRows{[]driver.Value{int64(7), int64(9)}}, nil }
	if strings.HasPrefix(q, "SELECT state ") {
		if pluckMode == "invalid" { return &pluckRows{[]driver.Value{"invalid"}}, nil }
		return &pluckRows{[]driver.Value{nil, "active"}}, nil
	}
	return &pluckRows{[]driver.Value{nil, "kept"}}, nil
}
func (*pluckRows) Columns() []string { return []string{"value"} }
func (*pluckRows) Close() error { return nil }
func (r *pluckRows) Next(dest []driver.Value) error {
	if len(r.values) == 0 {
		if pluckMode == "rows error" { return errors.New("rows failed") }
		return io.EOF
	}
	dest[0], r.values = r.values[0], r.values[1:]
	return nil
}
func TestPluck(t *testing.T) {
	sql.Register("pluck", pluckDriver{})
	db, err := sql.Open("pluck", "")
	if err != nil { t.Fatal(err) }
	t.Cleanup(func(){ _ = db.Close() })
	r := NewRecords(db)
	ctx := t.Context()
	ids, err := r.IDs(ctx, scopes.Records().IDEq(7).IDDesc().Limit(2))
	if err != nil || !reflect.DeepEqual(ids, []int64{7,9}) { t.Fatalf("IDs: %v, %v", ids, err) }
	if !strings.HasPrefix(pluckQuery, "SELECT id FROM records WHERE (id = ") ||
		!strings.Contains(pluckQuery, "ORDER BY id DESC") || !strings.Contains(pluckQuery, "LIMIT 2") ||
		len(pluckArgs) != 1 || pluckArgs[0].Value != int64(7) {
		t.Fatalf("SQL: %s, %v", pluckQuery, pluckArgs)
	}
	states, err := r.StatesOnly(ctx)
	if err != nil || len(states) != 2 || states[0] != "" || states[1] != "active" {
		t.Fatalf("states: %v, %v", states, err)
	}
	notes, err := r.Notes(ctx)
	if err != nil || len(notes) != 2 || notes[0] != nil || *notes[1] != "kept" { t.Fatalf("notes: %v, %v", notes, err) }
	pluckMode = "empty"
	ids, err = r.IDs(ctx)
	if err != nil || ids == nil || len(ids) != 0 { t.Fatalf("empty: %v, %v", ids, err) }
	for _, mode := range []string{"query error", "rows error", "invalid"} {
		pluckMode = mode
		if _, err := r.StatesOnly(ctx); err == nil { t.Fatalf("accepted %s", mode) }
	}
}
`

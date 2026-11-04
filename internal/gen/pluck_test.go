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
	ListIDs(ctx context.Context, s ...scopes.Record) ([]int64, error)
	ListStates(ctx context.Context, s ...scopes.Record) ([]model.State, error)
	ListNotes(ctx context.Context, s ...scopes.Record) ([]string, error)
	GetState(ctx context.Context, id int64, s ...scopes.Record) (model.State, error)
	FindNote(ctx context.Context, id int64, s ...scopes.Record) (*string, error)
	//kanna:lock update
	FirstID(ctx context.Context, s ...scopes.Record) (int64, error)
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
		{"ListMissings(ctx context.Context, s ...scopes.Record) ([]int64, error)",
			"ListMissings: it returns the values of a column, but model.Record has no field Missings is the plural of"},
		{"ListIDsByState(ctx context.Context, s ...scopes.Record) ([]int64, error)",
			"model.Record has no field IDsByState is the plural of"},
		{"ListIDs(ctx context.Context, s ...scopes.Record) ([]bool, error)", "it reads Record.ID, an int64, as a bool"},
		{"GetNames(ctx context.Context, id int64, s ...scopes.Record) (string, error)",
			"model.Record has no field named Names"},
		{"FindNote(ctx context.Context, id int64, s ...scopes.Record) (string, error)", "cannot implement it as declared"},
		{"ListIDs(ctx context.Context, s ...scopes.Record) (int64, error)", "cannot implement it as declared"},
		{"ListStates(ctx context.Context, s ...scopes.Record) ([]string, error)",
			"it reads Record.State, a model.State, as a string"},
		{"//kanna:conflict id\n ListIDs(ctx context.Context, s ...scopes.Record) ([]int64, error)",
			"conflict is for an Upsert"},
		{"//kanna:pluck id\n Values(ctx context.Context, s ...scopes.Record) ([]int64, error)",
			"Values: //kanna:pluck is no more: a List of a column's values is named after its field"},
	} {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()
			archive := typedQueryShop
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

// The plural after List is made as that of a table's name, and with an s only of a field ending in an acronym.
func TestRepositories_PluckPlurals(t *testing.T) {
	t.Parallel()
	for method, want := range map[string]string{
		"\tListStatuses(ctx context.Context, s ...scopes.Order) ([]model.OrderStatus, error)\n": "",
		"\tListCustomerIDs(ctx context.Context, s ...scopes.Order) ([]int64, error)\n":          "",
		"\tListStatuss(ctx context.Context, s ...scopes.Order) ([]model.OrderStatus, error)\n": "repository.go:24:2: " +
			"error: ListStatuss: it returns the values of a column, but model.Order has no field Statuss is the plural " +
			"of\n  name it ListStatuses: the plural of Status is made as that of a table's name",
	} {
		if _, ds := repositories(t, snapshot.PostgreSQL, method, "", nil); ds != want {
			t.Errorf("Repositories of %q reports\n%s\nwant\n%s", method, ds, want)
		}
	}
}

// A Find of the values of a column that may be NULL is told so as one written by hand is, and (**T, error) tells no
// row and NULL apart.
func TestCommand_PluckFindOfNull(t *testing.T) {
	t.Parallel()
	for result, want := range map[string]string{
		"*string": "FindNote: a Find returns nil for no row, so it cannot read column note, which may be NULL\n" +
			"  name it Get, whose (*string, error) returns nil for NULL and orm.ErrNotFound for no row, or return " +
			"(**string, error) to tell the two apart",
		"**string": "",
	} {
		t.Run(result, func(t *testing.T) {
			t.Parallel()
			archive := strings.Replace(typedQueryShop, "Note string `orm:\"note,nullzero\"`", "Note *string", 1)
			archive = strings.Replace(archive, "type Records interface {", "type Records interface {\n"+
				"FindNote(ctx context.Context, id int64, s ...scopes.Record) ("+result+", error)\n", 1)
			dir := writeModule(t, archive)
			writeTypedQuerySnapshot(t, dir)
			code, _, stderr := run(t, filepath.Join(dir, "repository"), "-destination", "../impl")
			if want == "" && code != cli.ExitOK || want != "" && !strings.Contains(stderr, want) {
				t.Fatalf("generate: %d\n%s\nwant %q", code, stderr, want)
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
	ids, err := r.ListIDs(ctx, scopes.Record{}.IDEq(7).IDDesc().Limit(2))
	if err != nil || !reflect.DeepEqual(ids, []int64{7,9}) { t.Fatalf("IDs: %v, %v", ids, err) }
	if !strings.HasPrefix(pluckQuery, "SELECT id FROM records WHERE (id = ") ||
		!strings.Contains(pluckQuery, "ORDER BY id DESC") || !strings.Contains(pluckQuery, "LIMIT 2") ||
		len(pluckArgs) != 1 || pluckArgs[0].Value != int64(7) {
		t.Fatalf("SQL: %s, %v", pluckQuery, pluckArgs)
	}
	states, err := r.ListStates(ctx)
	if err != nil || len(states) != 2 || states[0] != "" || states[1] != "active" {
		t.Fatalf("states: %v, %v", states, err)
	}
	notes, err := r.ListNotes(ctx)
	if err != nil || len(notes) != 2 || notes[0] != "" || notes[1] != "kept" { t.Fatalf("notes: %v, %v", notes, err) }
	state, err := r.GetState(ctx, 7)
	if err != nil || state != "" { t.Fatalf("GetState: %v, %v", state, err) }
	note, err := r.FindNote(ctx, 7)
	if err != nil || note == nil || *note != "" { t.Fatalf("FindNote: %v, %v", note, err) }
	if _, err := r.FirstID(ctx); err == nil { t.Fatal("FirstID locked outside a transaction") }
	pluckMode = "empty"
	ids, err = r.ListIDs(ctx)
	if err != nil || ids == nil || len(ids) != 0 { t.Fatalf("empty: %v, %v", ids, err) }
	for _, mode := range []string{"query error", "rows error", "invalid"} {
		pluckMode = mode
		if _, err := r.ListStates(ctx); err == nil { t.Fatalf("accepted %s", mode) }
	}
}
`

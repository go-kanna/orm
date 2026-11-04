package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

const projectionMethods = `
 ListCards(ctx context.Context, s ...scopes.Record) ([]model.Card, error)
 GetCard(ctx context.Context, id int64, s ...scopes.Record) (model.Card, error)
 FindCard(ctx context.Context, id int64, s ...scopes.Record) (*model.Card, error)
 FirstCard(ctx context.Context, s ...scopes.Record) (model.Card, error)
 //kanna:lock update
 GetCardForUpdate(ctx context.Context, id int64, s ...scopes.Record) (model.Card, error)
 ListKeys(ctx context.Context, s ...scopes.Record) ([]model.Key, error)
`

func projectionShop() string {
	archive := strings.Replace(typedQueryShop, "type Record struct {", `type Key struct { ID int64 }
type Card struct {
 Status State `+"`orm:\"state\"`"+`
 Caption string
}
type Record struct {`, 1)
	archive = strings.Replace(archive, "Note string", "Caption string", 1)
	// Save names its values itself, now that the field holding note is Caption.
	archive = strings.Replace(archive, "Save(ctx context.Context, record model.Record) (int64, error)",
		"Save(ctx context.Context, state model.State, note string) (int64, error)", 1)
	archive = strings.Replace(archive, "type Record struct { q scope.Of[model.Record] }",
		`type Record struct { q scope.Of[model.Record] }
func (s Record) WithUnusedRelation() Record {
 return Record{scope.Of[model.Record](impl.Preload(impl.Of[model.Record](s.q), "Unused", impl.Of[model.Record]{}))}
}`, 1)
	archive = strings.Replace(archive, `"github.com/go-kanna/orm/scope"`, `"github.com/go-kanna/orm/impl"
	"github.com/go-kanna/orm/scope"`, 1)
	return strings.Replace(archive, "type Records interface {", "type Records interface {\n"+projectionMethods, 1)
}

func TestCommand_ReadProjections(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		dialect snapshot.Dialect
		driver  string
	}{
		{snapshot.PostgreSQL, "pgx"}, {snapshot.PostgreSQL, "database/sql"}, {snapshot.MySQL, "database/sql"},
	} {
		t.Run(string(tt.dialect)+"/"+tt.driver, func(t *testing.T) {
			t.Parallel()
			archive := projectionShop()
			if tt.driver == "database/sql" {
				archive += "\n-- impl/projection_test.go --\n" + projectionRuntime
			}
			dir := writeModule(t, archive)
			writeTypedQuerySnapshot(t, dir)
			if tt.dialect == snapshot.MySQL {
				path := filepath.Join(dir, snapshot.Name)
				data, err := os.ReadFile(path) //nolint:gosec // generated test module
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
			for _, step := range []struct {
				pkg  string
				args []string
			}{
				{"scopes", nil}, {"repository", []string{"-destination", "../impl", "-driver", tt.driver}},
			} {
				if code, _, errOut := run(t, filepath.Join(dir, step.pkg), step.args...); code != cli.ExitOK {
					t.Fatalf("generate %s = %d\n%s", step.pkg, code, errOut)
				}
			}
			moduleCommand(t, dir, "test", "-mod=mod", "./...")
		})
	}
}

func TestCommand_ReadProjectionMistakes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, before, after, want string }{
		{"unknown field", "Caption string\n}", "Missing string\n}", "the model of records has no field Missing"},
		{"field named as the column", "Caption string\n}", "Note string\n}", "the model of records has no field Note"},
		{"type mismatch", "Caption string\n}", "Caption *string\n}", "model field has type"},
		{"relation", "Caption string\n}", "Caption string\nOwners []Record\n}", "cannot contain relations"},
		{"model", "Caption string\n}", "Caption string\nOwner *Record\n}", "Owner is a model"},
		{"empty", "Caption string\n}", "Ignored string `orm:\"-\"`\n}", "must select at least one"},
		{"duplicate column", "Caption string\n}", "Caption string\nOther State `orm:\"state\"`\n}",
			"projection selects column state more than once"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			archive := projectionShop()
			if tt.name == "empty" {
				archive = strings.Replace(archive, "Status State `orm:\"state\"`", "", 1)
			}
			archive = strings.Replace(archive, tt.before, tt.after, 1)
			dir := writeModule(t, archive)
			writeTypedQuerySnapshot(t, dir)
			code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", "../impl")
			if code != cli.ExitFailure || !strings.Contains(errOut, tt.want) {
				t.Fatalf("generate = %d\n%s\nwant %s", code, errOut, tt.want)
			}
		})
	}
}

// TestCommand_ReadProjectionNames checks that a field of a projection selects the column of the model's field of its
// name, and not the column of its name.
func TestCommand_ReadProjectionNames(t *testing.T) {
	t.Parallel()
	dir := writeModule(t, `
-- model/model.go --
package model
type Record struct {
 ID int64
 Caption string `+"`orm:\"note\"`"+`
 Note string `+"`orm:\"other\"`"+`
}
type Summary struct { Note string }
-- scopes/scopes.go --
package scopes
import (
 "example.com/shop/model"
 "github.com/go-kanna/orm/scope"
)
//kanna:scope table=records
type Record struct { q scope.Of[model.Record] }
-- repository/repository.go --
package repository
import (
 "context"
 "example.com/shop/model"
 "example.com/shop/scopes"
)
//kanna:repository
type Records interface {
 ListSummaries(ctx context.Context, s ...scopes.Record) ([]model.Summary, error)
}
`)
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"records": {Columns: []snapshot.Column{
			{Name: "id", Type: "int8", NotNull: true}, {Name: "note", Type: "text", NotNull: true},
			{Name: "other", Type: "text", NotNull: true},
		}, PrimaryKey: []string{"id"}},
	}}
	if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
		t.Fatal(errOut)
	}
	if code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", "../impl"); code != cli.ExitOK {
		t.Fatalf("generate = %d\n%s", code, errOut)
	}
	generated, err := os.ReadFile(filepath.Join(dir, "impl", "repository_gen.go")) //nolint:gosec // the test's module
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "SELECT other FROM records") {
		t.Errorf("ListSummaries reads\n%s\nwant SELECT other FROM records", generated)
	}
}

const projectionRuntime = `package impl
import (
 "context"
 "database/sql"
 "database/sql/driver"
 "errors"
 "io"
 "strings"
 "testing"
 "example.com/shop/model"
 "example.com/shop/scopes"
 "github.com/go-kanna/orm"
)
type projectionDriver struct{}
type projectionConn struct{}
type projectionRows struct { values [][]driver.Value }
var projectionMode string
var projectionQuery string
func (projectionDriver) Open(string) (driver.Conn, error) { return projectionConn{}, nil }
func (projectionConn) Close() error { return nil }
func (projectionConn) Begin() (driver.Tx, error) { return nil, errors.New("unused") }
func (projectionConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (projectionConn) QueryContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
 projectionQuery = q
 if err := ctx.Err(); err != nil { return nil, err }
 switch projectionMode {
 case "empty", "rows error": return &projectionRows{}, nil
 case "query error": return nil, errors.New("query failed")
 case "scan error": return &projectionRows{[][]driver.Value{{true, "note"}}}, nil
 case "reverse": return &projectionRows{[][]driver.Value{{"active", "later"}, {"active", "earlier"}}}, nil
 }
 if strings.HasPrefix(q, "SELECT id ") { return &projectionRows{[][]driver.Value{{int64(7)}}}, nil }
 return &projectionRows{[][]driver.Value{{"active", nil}}}, nil
}
func (*projectionRows) Columns() []string {
 if strings.HasPrefix(projectionQuery, "SELECT id ") { return []string{"id"} }
 return []string{"state", "note"}
}
func (*projectionRows) Close() error { return nil }
func (r *projectionRows) Next(dst []driver.Value) error {
 if len(r.values) == 0 {
  if projectionMode == "rows error" { return errors.New("rows failed") }
  return io.EOF
 }
 copy(dst, r.values[0]); r.values = r.values[1:]; return nil
}
func TestProjections(t *testing.T) {
 sql.Register("projections", projectionDriver{})
 db, err := sql.Open("projections", "")
 if err != nil { t.Fatal(err) }
 t.Cleanup(func(){ _ = db.Close() })
 r, ctx := NewRecords(db), t.Context()
 cards, err := r.ListCards(ctx, scopes.Records().IDEq(7).IDDesc().Offset(1).Limit(3))
 if err != nil || len(cards) != 1 || cards[0].Status != "active" || cards[0].Caption != "" {
  t.Fatalf("ListCards = %+v, %v", cards, err)
 }
 if !strings.HasPrefix(projectionQuery, "SELECT state, note FROM records") ||
  !strings.Contains(projectionQuery, "ORDER BY id DESC") || !strings.Contains(projectionQuery, "LIMIT 3") ||
  !strings.Contains(projectionQuery, "OFFSET 1") { t.Fatal(projectionQuery) }
 key, err := r.ListKeys(ctx)
 if err != nil || len(key) != 1 || key[0].ID != 7 { t.Fatalf("ListKeys = %+v, %v", key, err) }
 if _, err := r.GetCard(ctx, 7); err != nil { t.Fatal(err) }
 if _, err := r.GetCardForUpdate(ctx, 7); err == nil || !strings.Contains(err.Error(), "transaction") {
  t.Fatalf("lock: %v", err)
 }
 if card, err := r.FindCard(ctx, 7); err != nil || card == nil { t.Fatalf("FindCard = %+v, %v", card, err) }
 if _, err := r.FirstCard(ctx, scopes.Records().IDAsc()); err != nil { t.Fatal(err) }
 if !strings.Contains(projectionQuery, "LIMIT 1") { t.Fatal(projectionQuery) }
 projectionMode = "reverse"
 cards, err = r.ListCards(ctx, scopes.Records().IDAsc().Before(model.Record{ID: 99}).Limit(2))
 if err != nil || len(cards) != 2 || cards[0].Caption != "earlier" { t.Fatalf("reverse = %+v, %v", cards, err) }
 projectionMode = ""
 if _, err := r.ListCards(ctx, scopes.Records().WithUnusedRelation()); err == nil { t.Fatal("preload was ignored") }
 projectionMode = "empty"
 cards, err = r.ListCards(ctx)
 if err != nil || cards == nil || len(cards) != 0 { t.Fatalf("empty = %+v, %v", cards, err) }
 if _, err := r.GetCard(ctx, 7); !errors.Is(err, orm.ErrNotFound) { t.Fatalf("GetCard: %v", err) }
 if card, err := r.FindCard(ctx, 7); err != nil || card != nil { t.Fatalf("absent FindCard = %+v, %v", card, err) }
 for _, mode := range []string{"query error", "rows error", "scan error"} {
  projectionMode = mode
  if _, err := r.ListCards(ctx); err == nil { t.Fatalf("%s was accepted", mode) }
  if _, err := r.GetCard(ctx, 7); err == nil { t.Fatalf("GetCard accepted %s", mode) }
 }
 projectionMode = ""
 canceled, cancel := context.WithCancel(ctx); cancel()
 if _, err := r.ListCards(canceled); !errors.Is(err, context.Canceled) { t.Fatalf("canceled: %v", err) }
}
`

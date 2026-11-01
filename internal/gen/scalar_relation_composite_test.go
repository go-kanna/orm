package gen_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

const compositeScalarRuntime = `package impl
import (
 "context"
 "database/sql"
 "database/sql/driver"
 "errors"
 "fmt"
 "io"
 "strings"
 "testing"
 "example.com/shop/scopes"
)
type fakeDriver struct{}
type fakeConn struct{}
type fakeRows struct { columns []string; values [][]driver.Value }
var parentCount = 3
var relationQueries int
func (fakeDriver) Open(string) (driver.Conn, error) { return fakeConn{}, nil }
func (fakeConn) Close() error { return nil }
func (fakeConn) Begin() (driver.Tx, error) { return nil, errors.New("unused") }
func (fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (fakeConn) CheckNamedValue(*driver.NamedValue) error { return nil }
func (fakeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
 if !strings.Contains(query, "FROM record_topics") {
  rows := &fakeRows{columns: []string{"id", "tenant_id", "state", "note"}}
  for tenant := 1; tenant <= parentCount; tenant++ {
   rows.values = append(rows.values, []driver.Value{int64(7), int64(tenant), nil, nil})
  }
  return rows, nil
 }
 relationQueries++
 if !strings.Contains(query, "(tenant_id, record_id) IN (") {
  return nil, fmt.Errorf("missing composite condition: %s", query)
 }
 var tenants, ids []int64
 if strings.Contains(query, "unnest") {
  tenants = args[0].Value.([]int64)
  ids = args[1].Value.([]int64)
 } else {
  for i := 0; i < len(args); i += 2 {
   tenants = append(tenants, args[i].Value.(int64))
   ids = append(ids, args[i+1].Value.(int64))
  }
 }
 if len(tenants) > 500 { return nil, errors.New("composite relation exceeded batch size") }
 rows := &fakeRows{columns: []string{"tenant_id", "record_id", "topic"}}
 for i, tenant := range tenants {
  if ids[i] != 7 { return nil, fmt.Errorf("unexpected key: %d, %d", tenant, ids[i]) }
  if tenant == 3 { continue }
  rows.values = append(rows.values, []driver.Value{tenant, ids[i], "news"})
  if tenant == 2 { rows.values = append(rows.values, []driver.Value{tenant, ids[i], "updates"}) }
 }
 return rows, nil
}
func (r *fakeRows) Columns() []string { return r.columns }
func (r *fakeRows) Close() error { return nil }
func (r *fakeRows) Next(values []driver.Value) error {
 if len(r.values) == 0 { return io.EOF }
 copy(values, r.values[0]); r.values = r.values[1:]
 return nil
}
func TestCompositeRelations(t *testing.T) {
 sql.Register("composite-scalars", fakeDriver{})
 db, err := sql.Open("composite-scalars", "")
 if err != nil { t.Fatal(err) }
 t.Cleanup(func() { _ = db.Close() })
 records := NewRecords(db)
 for _, count := range []int{3, 1001} {
  parentCount, relationQueries = count, 0
  rows, err := records.List(t.Context(), scopes.Records().WithTopics())
  if err != nil || len(rows) != count { t.Fatalf("List = %+v, %v", rows, err) }
  for _, row := range rows {
   want := 1
   if row.TenantID == 2 { want = 2 }
   if row.TenantID == 3 { want = 0 }
   if row.Topics == nil || len(row.Topics) != want {
    t.Fatalf("tenant %d: got %v, want %d topics", row.TenantID, row.Topics, want)
   }
  }
  if relationQueries != (count+499)/500 { t.Fatalf("related queries: %d", relationQueries) }
 }
}
`

func TestCommand_CompositeScalarRelations(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		dialect snapshot.Dialect
		driver  string
	}{
		{snapshot.PostgreSQL, "pgx"}, {snapshot.PostgreSQL, "database/sql"}, {snapshot.MySQL, "database/sql"},
	} {
		t.Run(string(tt.dialect)+"/"+tt.driver, func(t *testing.T) {
			t.Parallel()
			archive := strings.Replace(typedQueryShop, "ID int64",
				"ID int64\nTenantID int64\nTopics []string `orm:\"relation=record_topics,column=topic\"`", 1)
			// This fixture reads rows only; custom SQL is covered by the single-key tests.
			archive = strings.Replace(archive, "Insert(ctx context.Context, record model.Record) error", "", 1)
			archive = strings.Replace(archive, "Get(ctx context.Context, id int64) (model.Record, error)", "", 1)
			archive = strings.Replace(archive, "Save(ctx context.Context, record model.Record) (int64, error)", "", 1)
			archive = strings.Replace(archive, "Lookup(ctx context.Context, id int64) (model.Record, error)", "", 1)
			archive = strings.Replace(archive, "States(ctx context.Context) ([]model.State, error)", "", 1)
			archive, _, _ = strings.Cut(archive, "-- impl/queries.sql --")
			if tt.driver == "database/sql" {
				archive += "\n-- impl/runtime_test.go --\n" + compositeScalarRuntime
			}
			dir := writeModule(t, archive)
			writeTypedQuerySnapshot(t, dir)
			path := filepath.Join(dir, snapshot.Name)
			data, err := os.ReadFile(path) //nolint:gosec // test module snapshot
			if err != nil {
				t.Fatal(err)
			}
			s, err := snapshot.Unmarshal(data)
			if err != nil {
				t.Fatal(err)
			}
			s.Dialect, s.Statements = tt.dialect, nil
			parent := s.Tables["records"]
			parent.Columns = slices.Insert(parent.Columns, 1, snapshot.Column{Name: "tenant_id", Type: "bigint", NotNull: true})
			parent.PrimaryKey = []string{"tenant_id", "id"}
			s.Tables["records"] = parent
			s.Tables["record_topics"] = snapshot.Table{Columns: []snapshot.Column{
				{Name: "tenant_id", Type: "bigint", NotNull: true}, {Name: "record_id", Type: "bigint", NotNull: true},
				{Name: "topic", Type: "text", NotNull: true}}, ForeignKeys: []snapshot.ForeignKey{{
				Columns: []string{"tenant_id", "record_id"}, References: "records",
				ReferencedColumns: []string{"tenant_id", "id"}}}}
			if err := os.WriteFile(path, snapshot.Marshal(s), 0o600); err != nil { //nolint:gosec // test snapshot
				t.Fatal(err)
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

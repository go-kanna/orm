package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

const writeOptionsShop = `-- model/model.go --
package model
type Record struct {
 ID int64 ` + "`orm:\",default\"`" + `
 Key string ` + "`orm:\"record_key\"`" + `
 OwnerID *int64
 Note string ` + "`orm:\",nullzero\"`" + `
 Stamp int64 ` + "`orm:\",default\"`" + `
}
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
)
//kanna:repository
type Records interface {
 Insert(ctx context.Context, record model.Record) error
 //kanna:conflict record_key coalesce=owner_id,note
 Upsert(ctx context.Context, record model.Record) (model.Record,error)
 //kanna:conflict record_key coalesce=note keep=owner_id
 UpsertAll(ctx context.Context, records []model.Record) error
 InsertMany(ctx context.Context, records []model.Record) (int64, error)
 Update(ctx context.Context, record model.Record) error
 Get(ctx context.Context, id int64) (model.Record,error)
 GetByKey(ctx context.Context, recordKey string) (model.Record,error)
}
`

const writeOptionsRuntime = `package impl
import (
 "context"
 "database/sql"
 "fmt"
 "os"
 "testing"
 "time"
 "example.com/shop/model"
 _ "github.com/go-sql-driver/mysql"
 _ "github.com/jackc/pgx/v5/stdlib"
)
func TestWriteOptions(t *testing.T) {
 dialect := "DIALECT"
 driver, dsn := "pgx", os.Getenv("KANNA_ORM_TEST_POSTGRES")
 if dialect == "mysql" { driver, dsn = "mysql", os.Getenv("KANNA_ORM_TEST_MYSQL") }
 if dsn == "" { t.Skip("integration database is not configured") }
 db,err := sql.Open(driver,dsn)
 if err != nil { t.Fatal(err) }
 t.Cleanup(func(){ _ = db.Close() })
 ctx := t.Context()
 conn,err := db.Conn(ctx)
 if err != nil { t.Fatal(err) }
 t.Cleanup(func(){ _ = conn.Close() })
 namespace := fmt.Sprintf("kanna_options_%d",time.Now().UnixNano())
 if dialect == "mysql" {
  if _,err := conn.ExecContext(ctx,"CREATE DATABASE "+namespace); err != nil { t.Fatal(err) }
  t.Cleanup(func(){ _,_ = conn.ExecContext(context.Background(),"DROP DATABASE "+namespace) })
  if _,err := conn.ExecContext(ctx,"USE "+namespace); err != nil { t.Fatal(err) }
  _,err = conn.ExecContext(ctx,"CREATE TABLE records (id BIGINT PRIMARY KEY DEFAULT (UUID_SHORT()), "+
   "record_key VARCHAR(64) NOT NULL UNIQUE, owner_id BIGINT, note TEXT, stamp BIGINT NOT NULL DEFAULT 10)")
 } else {
  if _,err := conn.ExecContext(ctx,"CREATE SCHEMA "+namespace); err != nil { t.Fatal(err) }
  t.Cleanup(func(){ _,_ = conn.ExecContext(context.Background(),"DROP SCHEMA "+namespace+" CASCADE") })
  if _,err := conn.ExecContext(ctx,"SET search_path TO "+namespace); err != nil { t.Fatal(err) }
  if _,err := conn.ExecContext(ctx,"CREATE SEQUENCE ids"); err != nil { t.Fatal(err) }
  _,err = conn.ExecContext(ctx,"CREATE TABLE records (id BIGINT PRIMARY KEY DEFAULT nextval('ids'), "+
   "record_key TEXT NOT NULL UNIQUE, owner_id BIGINT, note TEXT, stamp BIGINT NOT NULL DEFAULT 10)")
 }
 if err != nil { t.Fatal(err) }
 records := NewRecords(conn)
 first,err := records.Upsert(ctx,model.Record{ID:999,Key:"one",OwnerID:new(int64(11)),Note:"first",Stamp:99})
 if err != nil { t.Fatal(err) }
 if first.ID == 0 || first.ID == 999 || first.Stamp != 10 { t.Fatalf("database defaults: %+v",first) }
 saved,err := records.Upsert(ctx,model.Record{ID:888,Key:"one",Stamp:99})
 if err != nil { t.Fatal(err) }
 if saved.ID != first.ID || saved.OwnerID == nil || *saved.OwnerID != 11 || saved.Note != "first" || saved.Stamp != 10 {
  t.Fatalf("NULL input did not keep stored values: %+v",saved)
 }
 saved,err = records.Upsert(ctx,model.Record{Key:"one",OwnerID:new(int64(22)),Note:"changed"})
 if err != nil || saved.OwnerID == nil || *saved.OwnerID != 22 || saved.Note != "changed" {
  t.Fatalf("non-NULL update: %+v, %v",saved,err)
 }
 saved.Stamp = 20
 if err := records.Update(ctx,saved); err != nil { t.Fatal(err) }
 got,err := records.Get(ctx,saved.ID)
 if err != nil || got.Stamp != 20 { t.Fatalf("default affected UPDATE: %+v, %v",got,err) }
 if err := records.UpsertAll(ctx,[]model.Record{{Key:"one"},{Key:"two"}}); err != nil { t.Fatal(err) }
 if n,err := records.InsertMany(ctx,[]model.Record{{Key:"four"},{Key:"five"}}); err != nil || n != 2 {
  t.Fatalf("bulk insert count: %d, %v",n,err)
 }
 if n,err := records.InsertMany(ctx,nil); err != nil || n != 0 { t.Fatalf("empty bulk insert: %d, %v",n,err) }
 got,err = records.Get(ctx,first.ID)
 if err != nil || got.Note != "changed" || got.OwnerID == nil || *got.OwnerID != 22 || got.Stamp != 20 {
  t.Fatalf("bulk upsert: %+v, %v",got,err)
 }
 second,err := records.GetByKey(ctx,"two")
 if err != nil || second.ID == first.ID || second.Stamp != 10 || second.OwnerID != nil || second.Note != "" {
  t.Fatalf("bulk defaults: %+v, %v",second,err)
 }
 if err := records.Insert(ctx,model.Record{ID:999,Key:"three",Stamp:99}); err != nil { t.Fatal(err) }
 third,err := records.GetByKey(ctx,"three")
 if err != nil || third.ID == 999 || third.Stamp != 10 { t.Fatalf("INSERT defaults: %+v, %v",third,err) }
}
`

func writeOptionsSnapshot(t *testing.T, dir string, dialect snapshot.Dialect) {
	t.Helper()
	s := snapshot.Snapshot{Dialect: dialect, Tables: map[string]snapshot.Table{
		"records": {Columns: []snapshot.Column{
			{Name: "id", Type: "bigint", NotNull: true, Default: true},
			{Name: "record_key", Type: "text", NotNull: true},
			{Name: "owner_id", Type: "bigint"}, {Name: "note", Type: "text"},
			{Name: "stamp", Type: "bigint", NotNull: true, Default: true},
		}, PrimaryKey: []string{"id"}, UniqueKeys: [][]string{{"record_key"}}},
	}}
	if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCommand_WriteOptions(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		dialect snapshot.Dialect
		driver  string
	}{
		{snapshot.PostgreSQL, "pgx"}, {snapshot.PostgreSQL, "database/sql"}, {snapshot.MySQL, "database/sql"},
	} {
		t.Run(string(tt.dialect)+"/"+tt.driver, func(t *testing.T) {
			t.Parallel()
			archive := writeOptionsShop
			if tt.driver == "database/sql" {
				archive += "\n-- impl/runtime_test.go --\n" + strings.ReplaceAll(writeOptionsRuntime, "DIALECT", string(tt.dialect))
			}
			dir := writeModule(t, archive)
			writeOptionsSnapshot(t, dir, tt.dialect)
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

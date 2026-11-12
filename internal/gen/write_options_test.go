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
 Origin string ` + "`orm:\",insertonly\"`" + `
 Version int64 ` + "`orm:\",readonly\"`" + `
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
 "example.com/shop/scopes"
)
//kanna:repository
type Records interface {
 Insert(ctx context.Context, record model.Record) error
 //kanna:conflict record_key
 Upsert(ctx context.Context, record model.Record) (model.Record,error)
 //kanna:conflict record_key
 UpsertChanges(ctx context.Context, record model.Record, changes scopes.RecordChangeSet) (model.Record,error)
 //kanna:conflict record_key
 UpsertAll(ctx context.Context, records []model.Record) error
 Update(ctx context.Context, record model.Record) error
 Get(ctx context.Context, id int64) (model.Record,error)
 GetByKey(ctx context.Context, key string) (model.Record,error)
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
 "example.com/shop/scopes"
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
   "record_key VARCHAR(64) NOT NULL UNIQUE, owner_id BIGINT, note TEXT, stamp BIGINT NOT NULL DEFAULT 10, "+
   "origin TEXT NOT NULL, version BIGINT NOT NULL DEFAULT 0)")
  if err != nil { t.Fatal(err) }
  _,err = conn.ExecContext(ctx,"CREATE TRIGGER records_inserted BEFORE INSERT ON records FOR EACH ROW "+
   "SET NEW.version = NEW.version + 1")
  if err != nil { t.Fatal(err) }
  _,err = conn.ExecContext(ctx,"CREATE TRIGGER records_updated BEFORE UPDATE ON records FOR EACH ROW "+
   "SET NEW.version = OLD.version + 1 + IF(NEW.version <> OLD.version, 1000, 0)")
 } else {
  if _,err := conn.ExecContext(ctx,"CREATE SCHEMA "+namespace); err != nil { t.Fatal(err) }
  t.Cleanup(func(){ _,_ = conn.ExecContext(context.Background(),"DROP SCHEMA "+namespace+" CASCADE") })
  if _,err := conn.ExecContext(ctx,"SET search_path TO "+namespace); err != nil { t.Fatal(err) }
  if _,err := conn.ExecContext(ctx,"CREATE SEQUENCE ids"); err != nil { t.Fatal(err) }
  _,err = conn.ExecContext(ctx,"CREATE TABLE records (id BIGINT PRIMARY KEY DEFAULT nextval('ids'), "+
   "record_key TEXT NOT NULL UNIQUE, owner_id BIGINT, note TEXT, stamp BIGINT NOT NULL DEFAULT 10, "+
   "origin TEXT NOT NULL, version BIGINT NOT NULL DEFAULT 0)")
  if err != nil { t.Fatal(err) }
  _,err = conn.ExecContext(ctx,"CREATE FUNCTION bump_version() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN "+
   "IF TG_OP = 'INSERT' THEN NEW.version := NEW.version + 1; "+
   "ELSE NEW.version := OLD.version + 1 + CASE WHEN NEW.version <> OLD.version THEN 1000 ELSE 0 END; END IF; "+
   "RETURN NEW; END $$")
  if err != nil { t.Fatal(err) }
  _,err = conn.ExecContext(ctx,"CREATE TRIGGER records_written BEFORE INSERT OR UPDATE ON records "+
   "FOR EACH ROW EXECUTE FUNCTION bump_version()")
 }
 if err != nil { t.Fatal(err) }
 records := NewRecords(conn)
 first,err := records.Upsert(ctx,model.Record{ID:999,Key:"one",OwnerID:new(int64(11)),Note:"first",Stamp:99,Origin:"a",
  Version:99})
 if err != nil { t.Fatal(err) }
 if first.ID == 0 || first.ID == 999 || first.Stamp != 10 || first.Origin != "a" || first.Version != 1 {
  t.Fatalf("database defaults: %+v",first)
 }
 saved,err := records.Upsert(ctx,model.Record{ID:888,Key:"one",Note:"second",Stamp:99,Origin:"b",Version:99})
 if err != nil { t.Fatal(err) }
 if saved.ID != first.ID || saved.OwnerID != nil || saved.Note != "second" || saved.Stamp != 10 ||
  saved.Origin != "a" || saved.Version != 2 {
  t.Fatalf("upsert of a row: %+v",saved)
 }
 saved,err = records.UpsertChanges(ctx,model.Record{Key:"one",Note:"given",Origin:"c"},
  scopes.RecordChanges().SetOwnerID(new(int64(22))).SetStamp(30))
 if err != nil || saved.OwnerID == nil || *saved.OwnerID != 22 || saved.Note != "second" || saved.Stamp != 30 ||
  saved.Origin != "a" {
  t.Fatalf("upsert of a change set: %+v, %v",saved,err)
 }
 saved,err = records.UpsertChanges(ctx,model.Record{Key:"one",Note:"given"},scopes.RecordChanges().SetNote(""))
 if err != nil || saved.Note != "" || saved.OwnerID == nil || *saved.OwnerID != 22 {
  t.Fatalf("upsert of a change set to NULL: %+v, %v",saved,err)
 }
 saved,err = records.UpsertChanges(ctx,model.Record{Key:"one",Note:"given"},scopes.RecordChanges())
 if err != nil || saved.ID != first.ID || saved.Note != "" || saved.Stamp != 30 {
  t.Fatalf("upsert of no changes: %+v, %v",saved,err)
 }
 if _,err := records.UpsertChanges(ctx,model.Record{Key:"one"},scopes.RecordChanges().SetKey("other")); err == nil {
  t.Fatal("upsert of a change set writing its key succeeded")
 }
 if _,err := records.GetByKey(ctx,"other"); err == nil { t.Fatal("upsert of a change set wrote its key") }
 fresh,err := records.UpsertChanges(ctx,model.Record{Key:"four",Note:"fresh",Origin:"d",Version:99},
  scopes.RecordChanges().SetNote("x"))
 if err != nil || fresh.Note != "fresh" || fresh.Origin != "d" || fresh.Stamp != 10 || fresh.Version != 1 {
  t.Fatalf("upsert of a change set inserting: %+v, %v",fresh,err)
 }
 before := saved.Version
 saved.Stamp, saved.Origin, saved.Version = 20, "z", 99
 if err := records.Update(ctx,saved); err != nil { t.Fatal(err) }
 got,err := records.Get(ctx,saved.ID)
 if err != nil || got.Stamp != 20 || got.Origin != "a" || got.Version != before+1 {
  t.Fatalf("update: %+v, %v",got,err)
 }
 bulk := []model.Record{{Key:"one",Note:"bulk",Origin:"y",Version:99},{Key:"two",Origin:"t",Version:99}}
 if err := records.UpsertAll(ctx,bulk); err != nil { t.Fatal(err) }
 got,err = records.Get(ctx,first.ID)
 if err != nil || got.Note != "bulk" || got.OwnerID != nil || got.Stamp != 20 || got.Origin != "a" ||
  got.Version != before+2 {
  t.Fatalf("bulk upsert: %+v, %v",got,err)
 }
 second,err := records.GetByKey(ctx,"two")
 if err != nil || second.ID == first.ID || second.Stamp != 10 || second.OwnerID != nil || second.Origin != "t" ||
  second.Version != 1 {
  t.Fatalf("bulk defaults: %+v, %v",second,err)
 }
 err = records.Insert(ctx,model.Record{ID:999,Key:"three",Stamp:99,Origin:"o",Version:99})
 if err != nil { t.Fatal(err) }
 third,err := records.GetByKey(ctx,"three")
 if err != nil || third.ID == 999 || third.Stamp != 10 || third.Origin != "o" || third.Version != 1 {
  t.Fatalf("INSERT defaults: %+v, %v",third,err)
 }
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
			{Name: "origin", Type: "text", NotNull: true},
			{Name: "version", Type: "bigint", NotNull: true, Default: true},
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

func TestCommand_PointerKey(t *testing.T) {
	t.Parallel()

	archive := strings.Replace(writeOptionsShop, " GetByKey(ctx context.Context, key string) (model.Record,error)\n",
		" ListByOwner(ctx context.Context, ownerID *int64) ([]model.Record,error)\n", 1)
	dir := writeModule(t, archive)
	writeOptionsSnapshot(t, dir, snapshot.PostgreSQL)
	if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
		t.Fatalf("generate scopes = %d\n%s", code, errOut)
	}
	code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", "../impl")
	want := "error: ListByOwner: ownerID is *int64, but a key is compared by its value, and nil would compare by " +
		"= NULL, which matches no row\n  declare it as int64\n" +
		"  narrow to the rows whose owner_id is NULL by the scope's OwnerIDIsNull"
	if code == cli.ExitOK || !strings.Contains(errOut, want) {
		t.Fatalf("generate = %d, want %q\n%s", code, want, errOut)
	}
}

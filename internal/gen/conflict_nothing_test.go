package gen_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

const conflictNothingMethods = `
 //kanna:insert
 //kanna:conflict record_key do=nothing
 Use(ctx context.Context, record model.Record) (int64,error)
 //kanna:conflict record_key do=nothing
 Insert(ctx context.Context, record model.Record) (int64,error)
 //kanna:conflict record_key do=nothing
 InsertOne(ctx context.Context, record model.Record) (model.Record,error)
 //kanna:conflict record_key do=nothing
 InsertQuiet(ctx context.Context, record model.Record) error
 //kanna:conflict record_key do=nothing
 InsertAll(ctx context.Context, records []model.Record) (int64,error)
 //kanna:conflict record_key do=nothing
 InsertRows(ctx context.Context, records []model.Record) ([]model.Record,error)
`

const conflictNothingRuntime = `package impl
import (
 "context"
 "errors"
 "fmt"
 "os"
 "testing"
 "time"
 CONNECTIMPORT
 "example.com/shop/model"
 "github.com/go-kanna/orm"
)
func TestConflictNothing(t *testing.T) {
 ctx := t.Context()
 dsn := os.Getenv("KANNA_ORM_TEST_POSTGRES")
 if dsn == "" { t.Skip("integration database is not configured") }
 CONNECT
 namespace := fmt.Sprintf("kanna_conflict_%d",time.Now().UnixNano())
 if _,err := conn.ExecMETHOD(ctx,"CREATE SCHEMA "+namespace); err != nil { t.Fatal(err) }
 t.Cleanup(func(){ _,_ = conn.ExecMETHOD(context.Background(),"DROP SCHEMA "+namespace+" CASCADE") })
 if _,err := conn.ExecMETHOD(ctx,"SET search_path TO "+namespace); err != nil { t.Fatal(err) }
 if _,err := conn.ExecMETHOD(ctx,"CREATE TABLE records (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, "+
 "record_key TEXT NOT NULL UNIQUE, owner_id BIGINT UNIQUE, note TEXT, stamp BIGINT NOT NULL DEFAULT 10)"); err != nil {
 t.Fatal(err)
 }
 records := NewRecords(conn)
 n,err := records.Insert(ctx,model.Record{Key:"one",Note:"original",OwnerID:new(int64(42))})
 if err != nil || n != 1 { t.Fatalf("first insert: %d %v",n,err) }
 n,err = records.Insert(ctx,model.Record{Key:"one",Note:"changed"})
 if err != nil || n != 0 { t.Fatalf("duplicate insert: %d %v",n,err) }
 row,err := records.InsertOne(ctx,model.Record{Key:"two"})
 if err != nil || row.ID == 0 || row.Key != "two" || row.Stamp != 10 { t.Fatalf("returning: %+v %v",row,err) }
 _,err = records.InsertOne(ctx,model.Record{Key:"two"})
 if !errors.Is(err,orm.ErrNotFound) { t.Fatalf("duplicate returning: %v",err) }
 if err := records.InsertQuiet(ctx,model.Record{Key:"one"}); err != nil { t.Fatal(err) }
 n,err = records.InsertAll(ctx,[]model.Record{{Key:"one"},{Key:"three"},{Key:"three"}})
 if err != nil || n != 1 { t.Fatalf("bulk insert: %d %v",n,err) }
 n,err = records.InsertAll(ctx,nil)
 if err != nil || n != 0 { t.Fatalf("empty insert: %d %v",n,err) }
 rows,err := records.InsertRows(ctx,[]model.Record{{Key:"one"},{Key:"four"}})
 if err != nil || len(rows) != 1 || rows[0].Key != "four" { t.Fatalf("bulk returning: %+v %v",rows,err) }
 rows,err = records.InsertRows(ctx,[]model.Record{{Key:"one"}})
 if err != nil || rows == nil || len(rows) != 0 { t.Fatalf("duplicate rows: %+v %v",rows,err) }
 n,err = records.Use(ctx,model.Record{Key:"named"})
 if err != nil || n != 1 { t.Fatalf("named insert: %d %v",n,err) }
 n,err = records.Use(ctx,model.Record{Key:"named"})
 if err != nil || n != 0 { t.Fatalf("named duplicate: %d %v",n,err) }
 // Duplicate handling must leave the transaction usable.
 tx,err := conn.BeginMETHOD(ctx)
 if err != nil { t.Fatal(err) }
 t.Cleanup(func(){ ROLLBACK })
 bound := NewRecords(tx)
 if _,err := bound.Insert(ctx,model.Record{Key:"one"}); err != nil { t.Fatal(err) }
 if _,err := bound.Insert(ctx,model.Record{Key:"in-tx"}); err != nil { t.Fatal(err) }
 if err := COMMIT; err != nil { t.Fatal(err) }
 // A different unique constraint must still fail.
 if _,err := records.Insert(ctx,model.Record{Key:"other",OwnerID:new(int64(42))}); err == nil {
 t.Fatal("an unrelated unique conflict was ignored")
 }
 got,err := records.GetByKey(ctx,"one")
 if err != nil || got.Note != "original" { t.Fatalf("existing row changed: %+v %v",got,err) }
 if _,err := records.Insert(ctx,model.Record{Key:"after-error"}); err != nil { t.Fatal(err) }
}
`

func conflictNothingShop() string {
	return strings.Replace(writeOptionsShop, "Insert(ctx context.Context, record model.Record) error",
		strings.TrimSpace(conflictNothingMethods), 1)
}

func TestCommand_ConflictNothing(t *testing.T) {
	t.Parallel()
	for _, driver := range []string{"pgx", "database/sql"} {
		t.Run(driver, func(t *testing.T) {
			t.Parallel()
			runtime := conflictNothingRuntime
			if driver == "pgx" {
				runtime = strings.NewReplacer(
					"CONNECTIMPORT", `"github.com/jackc/pgx/v5"`,
					"CONNECT", `conn,err := pgx.Connect(ctx,dsn)
 if err != nil { t.Fatal(err) }
 t.Cleanup(func(){ _ = conn.Close(context.Background()) })`,
					"ExecMETHOD", "Exec", "BeginMETHOD", "Begin",
					"ROLLBACK", "_ = tx.Rollback(context.Background())", "COMMIT", "tx.Commit(ctx)",
				).Replace(runtime)
			} else {
				runtime = strings.NewReplacer(
					"CONNECTIMPORT", `"database/sql"
 _ "github.com/jackc/pgx/v5/stdlib"`,
					"CONNECT", `db,err := sql.Open("pgx",dsn)
 if err != nil { t.Fatal(err) }
 t.Cleanup(func(){ _ = db.Close() })
 conn,err := db.Conn(ctx)
 if err != nil { t.Fatal(err) }
 t.Cleanup(func(){ _ = conn.Close() })`,
					"ExecMETHOD", "ExecContext", "BeginMETHOD(ctx)", "BeginTx(ctx,nil)",
					"ROLLBACK", "_ = tx.Rollback()", "COMMIT", "tx.Commit()",
				).Replace(runtime)
			}
			dir := writeModule(t, conflictNothingShop()+"\n-- impl/runtime_test.go --\n"+runtime)
			writeOptionsSnapshot(t, dir, snapshot.PostgreSQL)
			for _, step := range []struct {
				pkg  string
				args []string
			}{
				{"scopes", nil}, {"repository", []string{"-destination", "../impl", "-driver", driver}},
			} {
				if code, _, errOut := run(t, filepath.Join(dir, step.pkg), step.args...); code != cli.ExitOK {
					t.Fatalf("generate %s = %d\n%s", step.pkg, code, errOut)
				}
			}
			moduleCommand(t, dir, "test", "-mod=mod", "./...")
		})
	}
}

func TestCommand_ConflictNothingMistakes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, before, after, want string
		mysql                     bool
	}{
		{"insert arguments", "//kanna:insert", "//kanna:insert extra", "takes no arguments", false},
		{"insert pluck", "//kanna:insert", "//kanna:insert\n //kanna:pluck id", "cannot be combined", false},
		{"action", "do=nothing", "do=update", "supports only nothing", false},
		{"duplicate action", "do=nothing", "do=nothing do=nothing", "gives do= twice", false},
		{"keep", "do=nothing", "do=nothing keep=stamp", "cannot be combined", false},
		{"coalesce", "do=nothing", "do=nothing coalesce=note", "cannot be combined", false},
		{"upsert", "Insert(ctx context.Context, record model.Record) (int64,error)",
			"UpsertNothing(ctx context.Context, record model.Record) error", "requires an Insert", false},
		{"key", "record_key do=nothing", "note do=nothing", "neither the primary key", false},
		{"unknown", "record_key do=nothing", "missing do=nothing", "has no column missing", false},
		{"mysql", "do=nothing", "do=nothing", "MySQL cannot implement targeted", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := writeModule(t, strings.Replace(conflictNothingShop(), tt.before, tt.after, 1))
			dialect := snapshot.PostgreSQL
			if tt.mysql {
				dialect = snapshot.MySQL
			}
			writeOptionsSnapshot(t, dir, dialect)
			code, _, errOut := run(t, filepath.Join(dir, "scopes"))
			if code == cli.ExitOK {
				code, _, errOut = run(t, filepath.Join(dir, "repository"), "-destination", "../impl")
			}
			if code == cli.ExitOK || !strings.Contains(errOut, tt.want) {
				t.Fatalf("generate = %d want %q\n%s", code, tt.want, errOut)
			}
		})
	}
}

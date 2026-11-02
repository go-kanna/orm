package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

const defaultOnlyShop = `-- model/model.go --
package model
type Record struct { ID int64 ` + "`orm:\",default\"`" + ` }
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
)
//kanna:repository
type Records interface { Insert(ctx context.Context,record model.Record) (model.Record,error) }
`

const defaultOnlyRuntime = `package impl
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
func TestDefaultOnly(t *testing.T) {
 driver,dsn := "pgx",os.Getenv("KANNA_ORM_TEST_POSTGRES")
 if "DIALECT" == "mysql" { driver,dsn = "mysql",os.Getenv("KANNA_ORM_TEST_MYSQL") }
 if dsn == "" { t.Skip("integration database is not configured") }
 db,err := sql.Open(driver,dsn)
 if err != nil { t.Fatal(err) }
 t.Cleanup(func(){ _ = db.Close() })
 ctx := t.Context()
 conn,err := db.Conn(ctx)
 if err != nil { t.Fatal(err) }
 t.Cleanup(func(){ _ = conn.Close() })
 if driver == "mysql" {
  namespace := fmt.Sprintf("kanna_default_%d",time.Now().UnixNano())
  if _,err := conn.ExecContext(ctx,"CREATE DATABASE "+namespace); err != nil { t.Fatal(err) }
  t.Cleanup(func(){ _,_ = conn.ExecContext(context.Background(),"DROP DATABASE "+namespace) })
  if _,err := conn.ExecContext(ctx,"USE "+namespace); err != nil { t.Fatal(err) }
 }
 if _,err := conn.ExecContext(ctx,"CREATE TEMPORARY TABLE records (id BIGINT PRIMARY KEY DEFAULT 77)"); err != nil {
  t.Fatal(err)
 }
 records := NewRecords(conn)
 INSERT_CALL
 var id int64
 if err := conn.QueryRowContext(ctx,"SELECT id FROM records").Scan(&id); err != nil { t.Fatal(err) }
 if id != 77 { t.Fatalf("database default = %d, want 77",id) }
}
`

func TestCommand_DefaultOnly(t *testing.T) {
	t.Parallel()
	for _, dialect := range []snapshot.Dialect{snapshot.PostgreSQL, snapshot.MySQL} {
		t.Run(string(dialect), func(t *testing.T) {
			t.Parallel()
			archive := defaultOnlyShop
			runtime := strings.ReplaceAll(defaultOnlyRuntime, "DIALECT", string(dialect))
			call := `row,err := records.Insert(ctx,model.Record{ID:999})
if err != nil || row.ID != 77 { t.Fatalf("INSERT returned %+v, %v",row,err) }`
			if dialect == snapshot.MySQL {
				archive = strings.Replace(archive, "(model.Record,error)", "error", 1)
				call = `if err := records.Insert(ctx,model.Record{ID:999}); err != nil { t.Fatal(err) }`
			}
			archive += "\n-- impl/runtime_test.go --\n" + strings.ReplaceAll(runtime, "INSERT_CALL", call)
			dir := writeModule(t, archive)
			s := snapshot.Snapshot{Dialect: dialect, Tables: map[string]snapshot.Table{
				"records": {Columns: []snapshot.Column{{Name: "id", Type: "bigint", NotNull: true, Default: true}},
					PrimaryKey: []string{"id"}},
			}}
			if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, step := range []struct {
				pkg  string
				args []string
			}{
				{"scopes", nil}, {"repository", []string{"-destination", "../impl", "-driver", "database/sql"}},
			} {
				if code, _, errOut := run(t, filepath.Join(dir, step.pkg), step.args...); code != cli.ExitOK {
					t.Fatalf("generate %s = %d\n%s", step.pkg, code, errOut)
				}
			}
			moduleCommand(t, dir, "test", "-mod=mod", "./...")
		})
	}
}

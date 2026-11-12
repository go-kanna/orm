package gen_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

// reservedTableShop reads and deletes the rows of groups, a reserved word of MySQL, which it quotes in backquotes.
const reservedTableShop = `-- model/model.go --
package model
type Group struct {
 ID int64
 Name string
}
-- scopes/scopes.go --
package scopes
import (
 "example.com/shop/model"
 "github.com/go-kanna/orm/scope"
)
//kanna:scope table=groups
type Group struct { q scope.Of[model.Group] }
-- repository/repository.go --
package repository
import (
 "context"
 "example.com/shop/model"
 "example.com/shop/scopes"
)
//kanna:repository
type GroupRepository interface {
 Insert(ctx context.Context, group model.Group) error
 Count(ctx context.Context, s ...scopes.Group) (int64, error)
 Exists(ctx context.Context, s ...scopes.Group) (bool, error)
 Delete(ctx context.Context, id int64) error
}
-- impl/runtime_test.go --
package impl
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
)
func TestReservedTable(t *testing.T) {
 dsn := os.Getenv("KANNA_ORM_TEST_MYSQL")
 if dsn == "" { t.Skip("KANNA_ORM_TEST_MYSQL is not set") }
 db,err := sql.Open("mysql",dsn)
 if err != nil { t.Fatal(err) }
 t.Cleanup(func(){ _ = db.Close() })
 ctx := t.Context()
 conn,err := db.Conn(ctx)
 if err != nil { t.Fatal(err) }
 t.Cleanup(func(){ _ = conn.Close() })
 name := fmt.Sprintf("kanna_reserved_%d",time.Now().UnixNano())
 if _,err := conn.ExecContext(ctx,"CREATE DATABASE "+name); err != nil { t.Fatal(err) }
 t.Cleanup(func(){ _,_ = conn.ExecContext(context.Background(),"DROP DATABASE "+name) })
 if _,err := conn.ExecContext(ctx,"USE "+name); err != nil { t.Fatal(err) }
 _,err = conn.ExecContext(ctx,"CREATE TABLE ` + "`groups`" + ` (id BIGINT PRIMARY KEY, name TEXT NOT NULL)")
 if err != nil { t.Fatal(err) }
 groups := NewGroupRepository(conn)
 for _, g := range []model.Group{{ID:1,Name:"a"},{ID:2,Name:"b"}} {
  if err := groups.Insert(ctx,g); err != nil { t.Fatal(err) }
 }
 if n,err := groups.Count(ctx,scopes.Group{}.NameEq("a")); err != nil || n != 1 { t.Errorf("Count = %d, %v",n,err) }
 if ok,err := groups.Exists(ctx,scopes.Group{}.NameEq("c")); err != nil || ok { t.Errorf("Exists = %v, %v",ok,err) }
 if err := groups.Delete(ctx,2); err != nil { t.Fatal(err) }
 if n,err := groups.Count(ctx); err != nil || n != 1 { t.Errorf("Count after Delete = %d, %v",n,err) }
}
`

// The SQL of Count, Exists, and Delete holds the name of the table, which MySQL quotes in backquotes when it is a
// reserved word: the generated code is still Go, and reads the table.
func TestCommand_ReservedTableMySQL(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, reservedTableShop)
	s := snapshot.Snapshot{Dialect: snapshot.MySQL, Tables: map[string]snapshot.Table{
		"groups": {Columns: []snapshot.Column{
			{Name: "id", Type: "bigint", NotNull: true},
			{Name: "name", Type: "text", NotNull: true},
		}, PrimaryKey: []string{"id"}},
	}}
	if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		pkg  string
		args []string
	}{
		{"scopes", nil}, {"repository", []string{"-destination", "../impl"}},
	} {
		if code, _, errOut := run(t, filepath.Join(dir, step.pkg), step.args...); code != cli.ExitOK {
			t.Fatalf("generate %s = %d\n%s", step.pkg, code, errOut)
		}
	}
	moduleCommand(t, dir, "test", "-mod=mod", "./...")
}

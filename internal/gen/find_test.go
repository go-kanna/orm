package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
	"github.com/go-kanna/orm/internal/sqlfile"
)

const findMethods = `
 Find(ctx context.Context, id int64, s ...scopes.Record) (*model.Record,error)
 FindByKey(ctx context.Context, recordKey string) (*model.Record,error)
 //kanna:lock update
 FindForUpdate(ctx context.Context, id int64) (*model.Record,error)
 FindCustom(ctx context.Context, recordKey string) (*model.Record,error)
`

const findQuery = `-- query: FindCustom
SELECT id, record_key, owner_id, note, stamp FROM records WHERE record_key = :recordKey;
`

const findChecks = `
 first,err := records.Upsert(ctx,model.Record{Key:"one",OwnerID:new(int64(11)),Note:"first"})
 if err != nil { t.Fatal(err) }
 got,err := records.Find(ctx,first.ID,scopes.Records().NoteEq("first"))
 if err != nil || got == nil || got.ID != first.ID || got.OwnerID == nil || *got.OwnerID != 11 {
 t.Fatalf("existing row: %+v %v",got,err)
 }
 got,err = records.Find(ctx,first.ID,scopes.Records().NoteEq("different"))
 if err != nil || got != nil { t.Fatalf("filtered row: %+v %v",got,err) }
 got,err = records.Find(ctx,first.ID+100)
 if err != nil || got != nil { t.Fatalf("missing row: %+v %v",got,err) }
 got,err = records.FindByKey(ctx,"one")
 if err != nil || got == nil || got.Note != "first" { t.Fatalf("unique key: %+v %v",got,err) }
 got,err = records.FindByKey(ctx,"missing")
 if err != nil || got != nil { t.Fatalf("missing unique key: %+v %v",got,err) }
 got,err = records.FindCustom(ctx,"one")
 if err != nil || got == nil || got.ID != first.ID { t.Fatalf("custom row: %+v %v",got,err) }
 got,err = records.FindCustom(ctx,"missing")
 if err != nil || got != nil { t.Fatalf("missing custom row: %+v %v",got,err) }
 got,err = records.FindForUpdate(ctx,first.ID)
 if err == nil || got != nil { t.Fatalf("lock without transaction: %+v %v",got,err) }
 tx,err := conn.BeginTx(ctx,nil)
 if err != nil { t.Fatal(err) }
 t.Cleanup(func(){ _ = tx.Rollback() })
 bound := NewRecords(tx)
 got,err = bound.FindForUpdate(ctx,first.ID)
 if err != nil || got == nil { t.Fatalf("locked row: %+v %v",got,err) }
 got,err = bound.FindForUpdate(ctx,first.ID+100)
 if err != nil || got != nil { t.Fatalf("missing locked row: %+v %v",got,err) }
 if err := tx.Commit(); err != nil { t.Fatal(err) }
 canceled,cancel := context.WithCancel(ctx)
 cancel()
 got,err = records.Find(canceled,first.ID)
 if err == nil || got != nil { t.Fatalf("database error: %+v %v",got,err) }
 got,err = records.FindCustom(canceled,"one")
 if err == nil || got != nil { t.Fatalf("custom database error: %+v %v",got,err) }
}
`

func findShop() string {
	archive := strings.Replace(writeOptionsShop, " Get(ctx", findMethods+"\n Get(ctx", 1)
	return strings.Replace(archive, `"context"`, `"context"
 "example.com/shop/scopes"`, 1)
}

func describeFind(t *testing.T, dir string, dialect snapshot.Dialect) {
	t.Helper()
	writeOptionsSnapshot(t, dir, dialect)
	files, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := files.Close(); err != nil {
			t.Error(err)
		}
	})
	data, err := files.ReadFile(snapshot.Name)
	if err != nil {
		t.Fatal(err)
	}
	s, err := snapshot.Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	statements, ds := sqlfile.Parse("queries.sql", []byte(findQuery))
	if len(ds) != 0 || len(statements) != 1 {
		t.Fatalf("parse query: %v", ds)
	}
	s.Statements = map[string]snapshot.Statement{
		"example.com/shop/impl.FindCustom": {
			Digest: snapshot.Digest(statements[0].Text),
			Params: []snapshot.Param{{Name: "recordKey", Type: "text"}},
			Columns: []snapshot.Result{
				{Name: "id", Type: "bigint", NotNull: true},
				{Name: "record_key", Type: "text", NotNull: true},
				{Name: "owner_id", Type: "bigint", Nullable: true},
				{Name: "note", Type: "text", Nullable: true},
				{Name: "stamp", Type: "bigint", NotNull: true},
			},
		},
	}
	if err := files.WriteFile(snapshot.Name, snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCommand_Find(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		dialect snapshot.Dialect
		driver  string
	}{
		{snapshot.PostgreSQL, "pgx"}, {snapshot.PostgreSQL, "database/sql"}, {snapshot.MySQL, "database/sql"},
	} {
		t.Run(string(tt.dialect)+"/"+tt.driver, func(t *testing.T) {
			t.Parallel()
			runtime, _, _ := strings.Cut(writeOptionsRuntime, " first,err := records.Upsert")
			runtime = strings.ReplaceAll(runtime, "TestWriteOptions", "TestFind")
			runtime = strings.ReplaceAll(runtime, "DIALECT", string(tt.dialect))
			runtime = strings.Replace(runtime, `"example.com/shop/model"`, `"example.com/shop/model"
 "example.com/shop/scopes"`, 1)
			runtime += findChecks
			if tt.driver == "pgx" {
				runtime = strings.ReplaceAll(runtime, ` "database/sql"`, "")
				runtime = strings.Replace(runtime, `_ "github.com/jackc/pgx/v5/stdlib"`, `"github.com/jackc/pgx/v5"`, 1)
				start, end := strings.Index(runtime, " db,err := sql.Open"), strings.Index(runtime, " namespace :=")
				runtime = runtime[:start] + ` ctx := t.Context()
 conn,err := pgx.Connect(ctx,dsn)
 if err != nil { t.Fatal(err) }
 t.Cleanup(func(){ _ = conn.Close(context.Background()) })
` + runtime[end:]
				runtime = strings.NewReplacer(
					`driver, dsn := "pgx",`, `dsn :=`,
					`driver, dsn = "mysql",`, `dsn =`,
					"ExecContext", "Exec", "conn.BeginTx(ctx,nil)", "conn.Begin(ctx)",
					"tx.Rollback()", "tx.Rollback(context.Background())", "tx.Commit()", "tx.Commit(ctx)",
				).Replace(runtime)
			}
			archive := findShop() + "\n-- impl/queries.sql --\n" + findQuery + "\n-- impl/runtime_test.go --\n" + runtime
			dir := writeModule(t, archive)
			describeFind(t, dir, tt.dialect)
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

func TestCommand_FindMistakes(t *testing.T) {
	t.Parallel()
	const declaration = "Find(ctx context.Context, id int64, s ...scopes.Record) (*model.Record,error)"
	for _, tt := range []struct{ name, after, want string }{
		{"value", "Find(ctx context.Context, id int64) (model.Record,error)", "cannot implement it as declared"},
		{"slice", "Find(ctx context.Context, id int64) ([]model.Record,error)", "cannot implement it as declared"},
		{"nonunique", "Find(ctx context.Context, note string) (*model.Record,error)", "not a unique key"},
		{"scope only", "Find(ctx context.Context, s ...scopes.Record) (*model.Record,error)", "not a unique key"},
		{"skip locked", "//kanna:lock update skip-locked\n " + declaration, "skip-locked passes a locked row over"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			archive := strings.Replace(findShop(), declaration, tt.after, 1)
			dir := writeModule(t, archive+"\n-- impl/queries.sql --\n"+findQuery)
			describeFind(t, dir, snapshot.PostgreSQL)
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

func TestCommand_FindOnly(t *testing.T) {
	t.Parallel()
	archive, _, _ := strings.Cut(writeOptionsShop, "-- repository/repository.go --")
	archive += `-- repository/repository.go --
package repository
import (
 "context"
 "example.com/shop/model"
)
//kanna:repository
type Records interface {
 Find(ctx context.Context, id int64) (*model.Record,error)
}
`
	dir := writeModule(t, archive)
	writeOptionsSnapshot(t, dir, snapshot.PostgreSQL)
	if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
		t.Fatalf("scopes: %s", errOut)
	}
	if code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", "../impl"); code != cli.ExitOK {
		t.Fatalf("repository: %s", errOut)
	}
	moduleCommand(t, dir, "test", "-mod=mod", "./...")
}

func TestRepositories_FindCompositeKey(t *testing.T) {
	t.Parallel()
	_, ds := repositories(t, snapshot.PostgreSQL, `
 FindUser(ctx context.Context, tenantID int64, email string) (*model.User,error)
`, "", nil)
	if ds != "" {
		t.Fatalf("Find composite key: %s", ds)
	}
}

package gen_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

// auditShop declares notes, whose audit columns are named by the conventions, and events, whose audit columns are
// tagged, the update time left to a trigger of the database, of which the snapshot records no default; repositories
// writing both every way; and a method written by hand writing the update time of a note by the clock.
const auditShop = `
-- model/model.go --
package model

import "time"

type Note struct {
	ID        int64
	Name      string
	Body      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Event struct {
	ID         int64
	Kind       string
	Body       string
	InsertedAt time.Time ` + "`orm:\",created\"`" + `
	ModifiedAt time.Time ` + "`orm:\",updated,default\"`" + `
}
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=notes
type Note struct{ q scope.Of[model.Note] }

//kanna:scope table=events
type Event struct{ q scope.Of[model.Event] }
-- repository/repository.go --
package repository

import (
	"context"

	"example.com/shop/model"
	"example.com/shop/scopes"
	"github.com/go-kanna/orm/stdlib"
)

//kanna:repository
type Notes interface {
	Insert(ctx context.Context, n model.Note) (model.Note, error)
	InsertAll(ctx context.Context, notes []model.Note) error
	Update(ctx context.Context, n model.Note) error
	UpdateWhere(ctx context.Context, n model.Note, s ...scopes.Note) error
	UpdateByName(ctx context.Context, name string, c scopes.NoteChangeSet) error
	UpdatePage(ctx context.Context, c scopes.NoteChangeSet, s ...scopes.Note) (int64, error)
	//kanna:conflict name
	Upsert(ctx context.Context, n model.Note) error
	//kanna:conflict name
	UpsertAll(ctx context.Context, notes []model.Note) error
	//kanna:conflict name
	UpsertChanges(ctx context.Context, n model.Note, c scopes.NoteChangeSet) error
	GetByName(ctx context.Context, name string) (model.Note, error)
	Bump(ctx context.Context, name string) error
	Bind(db stdlib.DBTX) Notes
}

//kanna:repository
type Events interface {
	Insert(ctx context.Context, e model.Event) (model.Event, error)
	Update(ctx context.Context, e model.Event) error
	UpdateByKind(ctx context.Context, kind string, c scopes.EventChangeSet) error
	//kanna:conflict kind
	Upsert(ctx context.Context, e model.Event) error
	//kanna:conflict kind
	UpsertChanges(ctx context.Context, e model.Event, c scopes.EventChangeSet) error
	GetByKind(ctx context.Context, kind string) (model.Event, error)
}
-- impl/bump.go --
package impl

import "context"

// Bump writes the time of the clock over the update time of the note named name, by a statement written by hand.
func (r Notes) Bump(ctx context.Context, name string) error {
	_, err := r.db.ExecContext(ctx, "UPDATE notes SET updated_at = PARAM1 WHERE name = PARAM2", r.db.Now(ctx), name)
	return err
}
`

// auditRuntime writes the rows of auditShop through database/sql into a namespace of its own in the database of
// DIALECT, by a clock an hour later at each call.
const auditRuntime = `package impl

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"example.com/shop/model"
	"example.com/shop/scopes"
	"github.com/go-kanna/orm/option"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// clock tells, at its nth call, the time n hours after start.
type clock struct {
	start time.Time
	calls int
}

func (c *clock) Now(context.Context) time.Time {
	c.calls++
	return c.at(c.calls)
}

func (c *clock) at(n int) time.Time { return c.start.Add(time.Duration(n) * time.Hour) }

func TestAudit(t *testing.T) {
	dialect := "DIALECT"
	driver, dsn := "pgx", os.Getenv("KANNA_ORM_TEST_POSTGRES")
	if dialect == "mysql" {
		driver, dsn = "mysql", os.Getenv("KANNA_ORM_TEST_MYSQL")+"&parseTime=true"
	}
	if dsn == "" || dsn == "&parseTime=true" {
		t.Skip("integration database is not configured")
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	namespace := fmt.Sprintf("kanna_audit_%d", time.Now().UnixNano())
	schema := []string{
		"CREATE TABLE notes (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, name text NOT NULL UNIQUE, " +
			"body text NOT NULL, created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL)",
		"CREATE TABLE events (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, kind text NOT NULL UNIQUE, " +
			"body text NOT NULL, inserted_at timestamptz NOT NULL, modified_at timestamptz NOT NULL)",
		"CREATE FUNCTION assign_modified_at() RETURNS trigger LANGUAGE plpgsql AS " +
			"$$ BEGIN NEW.modified_at := '2000-01-01T00:00:00Z'; RETURN NEW; END $$",
		"CREATE TRIGGER assign_modified_at BEFORE INSERT ON events FOR EACH ROW EXECUTE FUNCTION assign_modified_at()",
	}
	if dialect == "mysql" {
		if _, err := conn.ExecContext(ctx, "CREATE DATABASE "+namespace); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), "DROP DATABASE "+namespace) })
		if _, err := conn.ExecContext(ctx, "USE "+namespace); err != nil {
			t.Fatal(err)
		}
		schema = []string{
			"CREATE TABLE notes (id bigint AUTO_INCREMENT PRIMARY KEY, name varchar(64) NOT NULL UNIQUE, " +
				"body text NOT NULL, created_at datetime(6) NOT NULL, updated_at datetime(6) NOT NULL)",
			"CREATE TABLE events (id bigint AUTO_INCREMENT PRIMARY KEY, kind varchar(64) NOT NULL UNIQUE, " +
				"body text NOT NULL, inserted_at datetime(6) NOT NULL, modified_at datetime(6) NOT NULL)",
			"CREATE TRIGGER assign_modified_at BEFORE INSERT ON events FOR EACH ROW " +
				"SET NEW.modified_at = '2000-01-01 00:00:00'",
		}
	} else {
		if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+namespace); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+namespace+" CASCADE") })
		if _, err := conn.ExecContext(ctx, "SET search_path TO "+namespace); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range schema {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	c := &clock{start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	old := time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)
	notes := NewNotes(conn, option.WithClock(c))
	// check reads the note named name, and checks it was created and updated at the nth calls of the clock.
	check := func(name string, created, updated int) model.Note {
		t.Helper()
		n, err := notes.GetByName(ctx, name)
		if err != nil || !n.CreatedAt.Equal(c.at(created)) || !n.UpdatedAt.Equal(c.at(updated)) {
			t.Fatalf("note %s = %+v, %v; want created at %v and updated at %v", name, n, err, c.at(created),
				c.at(updated))
		}
		return n
	}
	calls := func(n int) {
		t.Helper()
		if c.calls != n {
			t.Fatalf("the clock was read %d times, want %d", c.calls, n)
		}
	}

	inserted, err := notes.Insert(ctx, model.Note{Name: "a", Body: "first", CreatedAt: old, UpdatedAt: old})
	if err != nil || !inserted.CreatedAt.Equal(c.at(1)) || !inserted.UpdatedAt.Equal(c.at(1)) {
		t.Fatalf("Insert returned %+v, %v; want both times %v", inserted, err, c.at(1))
	}
	calls(1)
	if err := notes.InsertAll(ctx, []model.Note{{Name: "b", CreatedAt: old}, {Name: "c"}, {Name: "d"}}); err != nil {
		t.Fatal(err)
	}
	calls(2)
	check("b", 2, 2)
	check("d", 2, 2)

	a := inserted
	a.Body, a.CreatedAt, a.UpdatedAt = "second", old, old
	if err := notes.Update(ctx, a); err != nil {
		t.Fatal(err)
	}
	check("a", 1, 3)
	if err := notes.UpdateWhere(ctx, a, scopes.Note{}.BodyEq("second")); err != nil {
		t.Fatal(err)
	}
	check("a", 1, 4)
	if err := notes.UpdateByName(ctx, "a", scopes.NoteChanges().SetBody("third")); err != nil {
		t.Fatal(err)
	}
	if n := check("a", 1, 5); n.Body != "third" {
		t.Fatalf("Update of a change set wrote %+v", n)
	}
	if err := notes.UpdateByName(ctx, "a", scopes.NoteChanges()); err == nil {
		t.Fatal("Update of an empty change set succeeded")
	}
	check("a", 1, 5)
	calls(6)
	n, err := notes.UpdatePage(ctx, scopes.NoteChanges().SetBody("paged"),
		scopes.Note{}.NameIn("b", "c", "d").NameAsc().Limit(2))
	if err != nil || n != 2 {
		t.Fatalf("Update of a page = %d, %v, want 2", n, err)
	}
	check("b", 2, 7)
	check("c", 2, 7)
	check("d", 2, 2)

	if err := notes.Upsert(ctx, model.Note{Name: "a", Body: "upserted", CreatedAt: old, UpdatedAt: old}); err != nil {
		t.Fatal(err)
	}
	if n := check("a", 1, 8); n.Body != "upserted" {
		t.Fatalf("Upsert meeting a row wrote %+v", n)
	}
	if err := notes.Upsert(ctx, model.Note{Name: "e", Body: "new", CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	check("e", 9, 9)
	if err := notes.UpsertAll(ctx, []model.Note{{Name: "e", Body: "again"}, {Name: "f"}}); err != nil {
		t.Fatal(err)
	}
	check("e", 9, 10)
	check("f", 10, 10)
	if err := notes.UpsertChanges(ctx, model.Note{Name: "a"}, scopes.NoteChanges().SetBody("changed")); err != nil {
		t.Fatal(err)
	}
	check("a", 1, 11)
	if err := notes.UpsertChanges(ctx, model.Note{Name: "a", Body: "kept"}, scopes.NoteChanges()); err != nil {
		t.Fatal(err)
	}
	if n := check("a", 1, 11); n.Body != "changed" {
		t.Fatalf("Upsert of an empty change set rewrote %+v", n)
	}
	if err := notes.UpsertChanges(ctx, model.Note{Name: "g", Body: "new"}, scopes.NoteChanges()); err != nil {
		t.Fatal(err)
	}
	check("g", 13, 13)

	if err := notes.Bump(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	check("b", 2, 14)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := notes.Bind(tx).Insert(ctx, model.Note{Name: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	check("h", 15, 15)
	calls(15)

	ec := &clock{start: c.start}
	events := NewEvents(conn, option.WithClock(ec))
	assigned := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	// checkEvent reads the event of kind, and checks it was inserted at the nth call of the clock and modified when
	// the database assigned it.
	checkEvent := func(kind string, inserted int) model.Event {
		t.Helper()
		e, err := events.GetByKind(ctx, kind)
		if err != nil || !e.InsertedAt.Equal(ec.at(inserted)) || !e.ModifiedAt.Equal(assigned) {
			t.Fatalf("event %s = %+v, %v; want inserted at %v and modified at %v", kind, e, err, ec.at(inserted),
				assigned)
		}
		return e
	}
	e, err := events.Insert(ctx, model.Event{Kind: "signup", Body: "x", InsertedAt: old, ModifiedAt: old})
	if err != nil || !e.InsertedAt.Equal(ec.at(1)) || !e.ModifiedAt.Equal(assigned) {
		t.Fatalf("Insert of an event returned %+v, %v", e, err)
	}
	e.Body, e.InsertedAt, e.ModifiedAt = "y", old, old
	if err := events.Update(ctx, e); err != nil {
		t.Fatal(err)
	}
	if e := checkEvent("signup", 1); e.Body != "y" {
		t.Fatalf("Update of an event wrote %+v", e)
	}
	if err := events.UpdateByKind(ctx, "signup", scopes.EventChanges().SetBody("z")); err != nil {
		t.Fatal(err)
	}
	checkEvent("signup", 1)
	if err := events.Upsert(ctx, model.Event{Kind: "signup", Body: "w", InsertedAt: old, ModifiedAt: old}); err != nil {
		t.Fatal(err)
	}
	checkEvent("signup", 1)
	if err := events.UpsertChanges(ctx, model.Event{Kind: "login", ModifiedAt: old},
		scopes.EventChanges().SetBody("v")); err != nil {
		t.Fatal(err)
	}
	checkEvent("login", 3)
	if ec.calls != 3 {
		t.Fatalf("the clock of events was read %d times, want 3, by the writes that insert", ec.calls)
	}
}
`

// auditTables describes the tables of auditShop on the database of d.
func auditTables(d snapshot.Dialect) map[string]snapshot.Table {
	text, at := "text", "timestamptz"
	if d == snapshot.MySQL {
		text, at = "varchar(64)", "datetime(6)"
	}
	id := snapshot.Column{Name: "id", Type: "bigint", NotNull: true, Assigned: true}
	return map[string]snapshot.Table{
		"notes": {Columns: []snapshot.Column{id, {Name: "name", Type: text, NotNull: true},
			{Name: "body", Type: "text", NotNull: true}, {Name: "created_at", Type: at, NotNull: true},
			{Name: "updated_at", Type: at, NotNull: true},
		}, PrimaryKey: []string{"id"}, UniqueKeys: [][]string{{"name"}}},
		"events": {Columns: []snapshot.Column{id, {Name: "kind", Type: text, NotNull: true},
			{Name: "body", Type: "text", NotNull: true}, {Name: "inserted_at", Type: at, NotNull: true},
			{Name: "modified_at", Type: at, NotNull: true},
		}, PrimaryKey: []string{"id"}, UniqueKeys: [][]string{{"kind"}}},
	}
}

// generateAuditShop writes archive, auditShop and more, into a module with the snapshot of d, and generates its
// scopes and its repositories into impl through driver.
func generateAuditShop(t *testing.T, archive string, d snapshot.Dialect, driver string) string {
	t.Helper()

	param1, param2 := "$1", "$2"
	if d == snapshot.MySQL {
		param1, param2 = "?", "?"
	}
	archive = strings.NewReplacer("PARAM1", param1, "PARAM2", param2).Replace(archive)
	dir := writeModule(t, archive)
	s := snapshot.Snapshot{Dialect: d, Tables: auditTables(d)}
	if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
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
	return dir
}

func TestCommand_Audit(t *testing.T) {
	t.Parallel()
	for _, d := range []snapshot.Dialect{snapshot.PostgreSQL, snapshot.MySQL} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			archive := auditShop + "\n-- impl/audit_test.go --\n" + strings.ReplaceAll(auditRuntime, "DIALECT", string(d))
			dir := generateAuditShop(t, archive, d, "database/sql")
			moduleCommand(t, dir, "test", "-mod=mod", "./...")
		})
	}
}

// TestCommand_AuditGenerated checks what kanna-orm writes of the audit columns: the clock read once, before a write
// stamping them; no setter of one in a change set; and no statement writing one the database assigns.
func TestCommand_AuditGenerated(t *testing.T) {
	t.Parallel()
	for _, d := range []snapshot.Dialect{snapshot.PostgreSQL, snapshot.MySQL} {
		t.Run(string(d), func(t *testing.T) {
			t.Parallel()
			dir := generateAuditShop(t, auditShop, d, "database/sql")
			scopes, err := os.ReadFile(filepath.Join(dir, "scopes", "scopes_gen.go")) //nolint:gosec // written above
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(scopes), "func (c NoteChangeSet) SetBody(") ||
				regexp.MustCompile(`(Set|Add)(CreatedAt|UpdatedAt|InsertedAt|ModifiedAt)\(`).Match(scopes) {
				t.Errorf("the change sets set an audit column, or not Body:\n%s", scopes)
			}
			src, err := os.ReadFile(filepath.Join(dir, "impl", "repository_gen.go")) //nolint:gosec // written above
			if err != nil {
				t.Fatal(err)
			}
			methods := map[string]string{}
			for _, m := range regexp.MustCompile(`(?ms)^func \(r (\w+)\) (\w+)\(.*?^}$`).FindAllStringSubmatch(string(src),
				-1) {
				methods[m[1]+"."+m[2]] = m[0]
			}
			if !strings.Contains(string(src), "func (d kDatabase) Now(ctx context.Context) time.Time { "+
				"return d.options.Now(ctx) }") {
				t.Errorf("kDatabase has no Now:\n%s", src)
			}
			for name, want := range map[string]int{
				"Notes.Insert": 1, "Notes.InsertAll": 1, "Notes.Update": 1, "Notes.UpdateWhere": 1,
				"Notes.UpdateByName": 1, "Notes.UpdatePage": 1, "Notes.Upsert": 1, "Notes.UpsertAll": 1,
				"Notes.UpsertChanges": 1, "Notes.GetByName": 0, "Events.Insert": 1, "Events.Update": 0,
				"Events.UpdateByKind": 0, "Events.Upsert": 1, "Events.UpsertChanges": 1,
			} {
				if got := strings.Count(methods[name], "now := r.db.Now(ctx)"); got != want {
					t.Errorf("%s reads the clock %d times, want %d:\n%s", name, got, want, methods[name])
				}
			}
			for _, name := range []string{"Notes.InsertAll", "Notes.UpsertAll"} {
				if i := strings.Index(methods[name], "len(notes) == 0"); i < 0 ||
					strings.Index(methods[name], "now := ") < i {
					t.Errorf("%s reads the clock before it sees it has rows to write:\n%s", name, methods[name])
				}
			}
			for _, name := range []string{"Notes.UpdateByName", "Notes.UpdatePage", "Notes.UpsertChanges"} {
				if !strings.Contains(methods[name], `.Touch("updated_at", now)`) {
					t.Errorf("%s does not touch updated_at:\n%s", name, methods[name])
				}
			}
			for _, name := range []string{"Events.Insert", "Events.Update", "Events.UpdateByKind", "Events.Upsert",
				"Events.UpsertChanges"} {
				if regexp.MustCompile(`INSERT INTO events \([^)]*modified_at|modified_at = |"modified_at"`).
					MatchString(methods[name]) || strings.Contains(methods[name], "Touch(") {
					t.Errorf("%s writes modified_at, which a trigger assigns:\n%s", name, methods[name])
				}
			}
		})
	}
}

// auditCopyShop declares entries, whose creation time a Copy writes as its rows hold it, and whose update time the
// database assigns.
const auditCopyShop = `
-- model/model.go --
package model

import "time"

type Entry struct {
	ID        int64
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time ` + "`orm:\",default\"`" + `
}
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=entries
type Entry struct{ q scope.Of[model.Entry] }
-- repository/repository.go --
package repository

import (
	"context"

	"example.com/shop/model"
	"example.com/shop/scopes"
)

//kanna:repository
type Entries interface {
	Copy(ctx context.Context, rows []model.Entry) (int64, error)
	List(ctx context.Context, s ...scopes.Entry) ([]model.Entry, error)
}
-- impl/copy_test.go --
package impl

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"example.com/shop/model"
	"example.com/shop/scopes"
	"github.com/go-kanna/orm/hook"
	"github.com/go-kanna/orm/option"
	"github.com/jackc/pgx/v5"
)

// stopped is a clock no Copy reads.
type stopped struct{ t *testing.T }

func (c stopped) Now(context.Context) time.Time {
	c.t.Error("Copy read the clock")
	return time.Time{}
}

func TestAuditCopy(t *testing.T) {
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, os.Getenv("KANNA_ORM_TEST_POSTGRES"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.WithoutCancel(ctx)) })
	schema := fmt.Sprintf("kanna_audit_copy_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema+"; SET search_path TO "+schema+";\n"+
		"CREATE TABLE entries (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, name text NOT NULL, "+
		"created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL DEFAULT '2000-01-01T00:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "DROP SCHEMA "+schema+" CASCADE") })

	var copied string
	entries := NewEntries(conn, option.WithClock(stopped{t}), option.WithHook(hook.Hook{
		After: func(_ context.Context, e hook.Execution) { copied = e.SQL },
	}))
	imported := time.Date(2019, 4, 1, 9, 30, 0, 0, time.UTC)
	if n, err := entries.Copy(ctx, []model.Entry{{Name: "a", CreatedAt: imported, UpdatedAt: imported},
		{Name: "b", CreatedAt: imported.Add(time.Hour)}}); err != nil || n != 2 {
		t.Fatalf("Copy = %d, %v, want 2", n, err)
	}
	if want := "COPY \"entries\" (\"name\", \"created_at\") FROM STDIN"; copied != want {
		t.Fatalf("Copy ran %s, want %s", copied, want)
	}
	stored, err := entries.List(ctx, scopes.Entry{}.NameAsc())
	assigned := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err != nil || len(stored) != 2 || !stored[0].CreatedAt.Equal(imported) ||
		!stored[1].CreatedAt.Equal(imported.Add(time.Hour)) || !stored[0].UpdatedAt.Equal(assigned) ||
		!stored[1].UpdatedAt.Equal(assigned) {
		t.Fatalf("copied %+v, %v; want the creation times given, and the update times the database assigns", stored,
			err)
	}
}
`

// A Copy writes the audit columns as its rows hold them, but for one the database assigns, and reads no clock.
func TestCommand_AuditCopy(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, auditCopyShop)
	at := snapshot.Column{Name: "created_at", Type: "timestamptz", NotNull: true}
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"entries": {Columns: []snapshot.Column{
			{Name: "id", Type: "bigint", NotNull: true, Assigned: true}, {Name: "name", Type: "text", NotNull: true},
			at, {Name: "updated_at", Type: "timestamptz", NotNull: true, Default: true},
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
	src, err := os.ReadFile(filepath.Join(dir, "impl", "repository_gen.go")) //nolint:gosec // the file written above
	if err != nil {
		t.Fatal(err)
	}
	want := `func (r Entries) Copy(ctx context.Context, rows []model.Entry) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	n, err := r.db.CopyFrom(ctx, pgx.Identifier{"entries"}, []string{"name", "created_at"},
		pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
			return []any{rows[i].Name, rows[i].CreatedAt}, nil
		}))
	if err != nil {
		return 0, fmt.Errorf("copy into entries: %w", err)
	}
	return n, nil
}
`
	if !strings.Contains(string(src), want) {
		t.Errorf("repository_gen.go has no\n%s\n%s", want, src)
	}
	if os.Getenv("KANNA_ORM_TEST_POSTGRES") == "" {
		t.Skip("KANNA_ORM_TEST_POSTGRES is not set; make db-up test-db sets it")
	}
	moduleCommand(t, dir, "test", "-mod=mod", "./...")
}

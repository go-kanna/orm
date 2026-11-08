package gen_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-kanna/internal/cli"
)

// copyRuntime copies the rows of copyShop through pgx into a schema of its own in the database
// KANNA_ORM_TEST_POSTGRES names.
const copyRuntime = `package impl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"example.com/shop/model"
	"example.com/shop/scopes"
	"github.com/go-kanna/orm/hook"
	"github.com/go-kanna/orm/option"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCopy(t *testing.T) {
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, os.Getenv("KANNA_ORM_TEST_POSTGRES"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.WithoutCancel(ctx)) })
	schema := fmt.Sprintf("kanna_copy_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema+"; SET search_path TO "+schema+";\n"+
		"CREATE TABLE events (id bigserial PRIMARY KEY, kind text NOT NULL, tags text[], note text, "+
		"created_at timestamptz NOT NULL DEFAULT '2026-11-08T00:00:00Z');\n"+
		"CREATE TABLE tags (name text PRIMARY KEY, labels text[]);"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "DROP SCHEMA "+schema+" CASCADE") })

	var done []hook.Execution
	events := NewEvent(conn, option.WithHook(hook.Hook{After: func(_ context.Context, e hook.Execution) {
		done = append(done, e)
	}}))
	assigned := []model.Event{{Kind: "assigned", Tags: []string{"a", "b"}, CreatedAt: time.Now()}, {Kind: "assigned too"}}
	if n, err := events.Copy(ctx, assigned); err != nil || n != 2 {
		t.Fatalf("Copy of rows giving no id = %d, %v, want 2", n, err)
	}
	if n, err := events.Copy(ctx, []model.Event{{ID: 100, Kind: "given", Note: "noted"}}); err != nil || n != 1 {
		t.Fatalf("Copy of rows giving their ids = %d, %v, want 1", n, err)
	}
	statements := []string{` + "`" + `COPY "events" ("kind", "tags", "note") FROM STDIN` + "`" + `,
		` + "`" + `COPY "events" ("kind", "tags", "note", "id") FROM STDIN` + "`" + `}
	if len(done) != 2 || done[0].SQL != statements[0] || done[1].SQL != statements[1] || done[0].Args != nil ||
		done[0].Err != nil || done[1].Err != nil || done[0].Duration <= 0 {
		t.Fatalf("the hooks saw %+v, want %q", done, statements)
	}
	done = nil
	_, err = events.Copy(ctx, []model.Event{{Kind: "mixed"}, {ID: 200, Kind: "mixed"}})
	var mapped CopyError
	if !errors.As(err, &mapped) || mapped.Op != "Copy" || len(done) != 0 {
		t.Fatalf("Copy of rows giving an id and not = %v, with %d statements, want an error through mapError and none",
			err, len(done))
	}
	stored, err := events.List(ctx, scopes.Event{}.IDAsc())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range stored {
		got = append(got, fmt.Sprintf("%d %s %v %q %s", e.ID, e.Kind, e.Tags, e.Note,
			e.CreatedAt.UTC().Format(time.DateOnly)))
	}
	want := []string{` + "`" + `1 assigned [a b] "" 2026-11-08` + "`" + `,
		` + "`" + `2 assigned too [] "" 2026-11-08` + "`" + `, ` + "`" + `100 given [] "noted" 2026-11-08` + "`" + `}
	if !slices.Equal(got, want) {
		t.Fatalf("copied\n%q\nwant\n%q", got, want)
	}
	var nulls int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM events WHERE note IS NULL").Scan(&nulls); err != nil || nulls != 2 {
		t.Fatalf("notes left NULL: %d, %v, want 2", nulls, err)
	}

	done = nil
	if n, err := events.Copy(ctx, nil); err != nil || n != 0 || len(done) != 0 {
		t.Fatalf("Copy of no rows = %d, %v, with %d statements, want none", n, err, len(done))
	}
	_, err = events.Copy(ctx, []model.Event{{ID: 100, Kind: "again"}})
	var pg *pgconn.PgError
	if !errors.As(err, &mapped) || mapped.Op != "Copy" || !errors.As(err, &pg) || pg.Code != "23505" {
		t.Fatalf("Copy of an id there = %v, want a unique violation through mapError", err)
	}
	if len(done) != 1 || done[0].Err == nil {
		t.Fatalf("the hooks saw %+v, want the copy failed", done)
	}

	tags := []model.Tag{{Name: "go", Labels: []string{"lang", "tool"}}, {Name: "sql"}}
	if n, err := events.CopyTags(ctx, tags); err != nil || n != 2 {
		t.Fatalf("CopyTags = %d, %v, want 2", n, err)
	}
	var labels []string
	if err := conn.QueryRow(ctx, "SELECT labels FROM tags WHERE name = 'go'").Scan(&labels); err != nil ||
		!slices.Equal(labels, []string{"lang", "tool"}) {
		t.Fatalf("labels copied: %v, %v", labels, err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := events.Bind(tx).Copy(ctx, []model.Event{{Kind: "rolled back"}}); err != nil || n != 1 {
		t.Fatalf("Copy in a transaction = %d, %v, want 1", n, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM events").Scan(&count); err != nil || count != 3 {
		t.Fatalf("events after a rolled back copy: %d, %v, want 3", count, err)
	}
}
`

func TestCommand_Copy_Postgres(t *testing.T) {
	t.Parallel()

	if os.Getenv("KANNA_ORM_TEST_POSTGRES") == "" {
		t.Skip("KANNA_ORM_TEST_POSTGRES is not set; make db-up test-db sets it")
	}
	dir, code, errOut := generateCopyShop(t, copyShop+"-- impl/copy_test.go --\n"+copyRuntime, "pgx")
	if code != cli.ExitOK {
		t.Fatalf("kanna-orm = %d\n%s", code, errOut)
	}
	if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
		t.Fatalf("kanna-orm in scopes = %d\n%s", code, errOut)
	}
	moduleCommand(t, dir, "test", "-mod=mod", "./...")
}

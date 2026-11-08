package gen_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

// copyShop declares events, whose id the database assigns unless a row gives it and whose created_at it defaults,
// and tags, which hold an array; a repository copying the rows of both; and its mapError in the destination, impl.
const copyShop = `
-- model/model.go --
package model

import "time"

type Event struct {
	ID        int64
	Kind      string
	Tags      []string
	Note      string    ` + "`orm:\",nullzero\"`" + `
	CreatedAt time.Time ` + "`orm:\",default\"`" + `
}

type Tag struct {
	Name   string
	Labels []string
}
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=events
type Event struct{ q scope.Of[model.Event] }

//kanna:scope table=tags
type Tag struct{ q scope.Of[model.Tag] }
-- repository/repository.go --
package repository

import (
	"context"

	"example.com/shop/model"
	"example.com/shop/scopes"
	kpgx "github.com/go-kanna/orm/pgx"
)

//kanna:repository
type Event interface {
	Copy(ctx context.Context, rows []model.Event) (int64, error)
	CopyTags(ctx context.Context, tags []model.Tag) (int64, error)
	List(ctx context.Context, s ...scopes.Event) ([]model.Event, error)
	Bind(db kpgx.CopyDBTX) Event
}
-- impl/map_error.go --
package impl

// CopyError is an error Event returns, which has gone through its mapError.
type CopyError struct {
	Op  string
	Err error
}

func (e CopyError) Error() string { return e.Op + ": " + e.Err.Error() }

func (e CopyError) Unwrap() error { return e.Err }

func (Event) mapError(op eventOperation, err error) error { return CopyError{string(op), err} }
`

// describeCopyShop writes the snapshot of the tables copyShop writes into the module at dir.
func describeCopyShop(t *testing.T, dir string) {
	t.Helper()

	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"events": {
			Columns: []snapshot.Column{
				{Name: "id", Type: "int8", NotNull: true, Assigned: true, Writable: true,
					AssignedBy: "nextval('events_id_seq'::regclass)"},
				{Name: "kind", Type: "text", NotNull: true},
				{Name: "tags", Type: "text[]"},
				{Name: "note", Type: "text"},
				{Name: "created_at", Type: "timestamptz", NotNull: true, Default: true},
			},
			PrimaryKey: []string{"id"},
		},
		"tags": {
			Columns:    []snapshot.Column{{Name: "name", Type: "text", NotNull: true}, {Name: "labels", Type: "text[]"}},
			PrimaryKey: []string{"name"},
		},
	}, Statements: map[string]snapshot.Statement{
		"example.com/shop/impl.CopyKinds": {Digest: snapshot.Digest("INSERT INTO tags (name) SELECT kind FROM events;")},
	}}
	if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

// generateCopyShop writes archive, copyShop or one changed from it, as a module, generates its repositories on
// driver, and returns the module's directory, what kanna-orm exited with, and what it reported.
func generateCopyShop(t *testing.T, archive, driver string) (string, int, string) {
	t.Helper()

	dir := writeModule(t, archive)
	describeCopyShop(t, dir)
	moduleCommand(t, dir, "mod", "tidy")
	code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", "../impl", "-driver", driver)
	return dir, code, errOut
}

// A Copy writes its rows by one CopyFrom, which the DBTX of its destination has, into the columns an Insert of many
// rows writes: id when every row gives it, and none of it when no row does.
func TestCommand_Copy(t *testing.T) {
	t.Parallel()

	dir, code, errOut := generateCopyShop(t, copyShop, "pgx")
	if code != cli.ExitOK {
		t.Fatalf("kanna-orm = %d\n%s", code, errOut)
	}
	if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
		t.Fatalf("kanna-orm in scopes = %d\n%s", code, errOut)
	}
	data, err := os.ReadFile(filepath.Join(dir, "impl", "repository_gen.go")) //nolint:gosec // the file written above
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	for _, want := range []string{
		"type DBTX = kpgx.CopyDBTX\n",
		`func (d kDatabase) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, ` +
			`rowSrc pgx.CopyFromSource) (int64, error) {
	ctx, trace := d.options.StartQuery(ctx, kpgx.CopyFromSQL(tableName, columnNames), nil)
	n, err := d.dbtx.CopyFrom(ctx, tableName, columnNames, rowSrc)
	trace.End(err)
	return n, err
}
`,
		`func (r Event) Copy(ctx context.Context, rows []model.Event) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	withID := rows[0].ID != 0
	for _, row := range rows[1:] {
		if (row.ID != 0) != withID {
			return 0, r.mapError(eventOperationCopy, fmt.Errorf("orm: Copy: some rows give id and some leave it to ` +
			`the database, which one COPY cannot do: copy them apart"))
		}
	}
	columns := []string{"kind", "tags", "note"}
	if withID {
		columns = append(columns, "id")
	}
	n, err := r.db.CopyFrom(ctx, pgx.Identifier{"events"}, columns,
		pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
			if withID {
				return []any{rows[i].Kind, rows[i].Tags, sql.Null[string]{V: rows[i].Note, ` +
			`Valid: rows[i].Note != ""}, rows[i].ID}, nil
			}
			return []any{rows[i].Kind, rows[i].Tags, sql.Null[string]{V: rows[i].Note, Valid: rows[i].Note != ""}}, nil
		}))
	if err != nil {
		return 0, r.mapError(eventOperationCopy, fmt.Errorf("copy into events: %w", err))
	}
	return n, nil
}
`,
		`func (r Event) CopyTags(ctx context.Context, tags []model.Tag) (int64, error) {
	if len(tags) == 0 {
		return 0, nil
	}
	n, err := r.db.CopyFrom(ctx, pgx.Identifier{"tags"}, []string{"name", "labels"},
		pgx.CopyFromSlice(len(tags), func(i int) ([]any, error) {
			return []any{tags[i].Name, tags[i].Labels}, nil
		}))
	if err != nil {
		return 0, r.mapError(eventOperationCopyTags, fmt.Errorf("copy into tags: %w", err))
	}
	return n, nil
}
`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("repository_gen.go has no\n%s\n%s", want, src)
		}
	}
	moduleCommand(t, dir, "vet", "-mod=mod", "./...")
}

// copyTx is a transaction exposing the DBTX it runs on, as a package of the application declares one.
const copyTx = `
-- db/db.go --
package db

import (
	kpgx "github.com/go-kanna/orm/pgx"
	"github.com/jackc/pgx/v5"
)

type Tx struct{ tx pgx.Tx }

func (t Tx) DBTX() kpgx.DBTX { return t.tx }

type CopyTx struct{ tx pgx.Tx }

func (t CopyTx) DBTX() kpgx.CopyDBTX { return t.tx }
`

// Each declaration generates a package that compiles, whose DBTX copies rows only when a method kanna-orm writes
// copies them, or stops generation with what it reports.
func TestCommand_CopyDeclarations(t *testing.T) {
	t.Parallel()

	const bind, kpgx = "\tBind(db kpgx.CopyDBTX) Event\n", "\tkpgx \"github.com/go-kanna/orm/pgx\"\n"
	const copyDBTX = "type DBTX = kpgx.CopyDBTX\n"
	withoutBind := func(archive string) string {
		return strings.Replace(strings.Replace(archive, bind, "", 1), kpgx, "", 1)
	}
	// bindAs has Event bind a transaction of copyTx.
	bindAs := func(declaration string) func(string) string {
		return func(archive string) string {
			archive = strings.Replace(archive, bind, "\t"+declaration+"\n", 1)
			return strings.Replace(archive, kpgx, "\t\"example.com/shop/db\"\n", 1) + copyTx
		}
	}
	noCopyFrom := "error: Bind: %s has no CopyFrom, which the DBTX kanna-orm writes has, as Event.Copy copies rows " +
		"by it\n  %s CopyDBTX of github.com/go-kanna/orm/pgx, which *pgxpool.Pool, *pgx.Conn, and pgx.Tx implement"
	for _, tt := range []struct {
		name   string
		change func(string) string
		driver string
		// want is what kanna-orm reports, or the DBTX it writes for a declaration that generates.
		want string
	}{
		{"database/sql", withoutBind, "database/sql", "repository.go:12:2: error: Copy: " +
			"database/sql has no CopyFrom of pgx, which a Copy writes its rows by\n  generate the repositories on pgx: " +
			"-driver pgx\n  or write the rows by an Insert of many rows: Insert(ctx context.Context, rows " +
			"[]model.Order) error"},
		{"Bind of a DBTX that cannot copy", func(a string) string {
			return strings.Replace(a, bind, "\tBind(db kpgx.DBTX) Event\n", 1)
		}, "pgx", "repository.go:16:2: " + fmt.Sprintf(noCopyFrom, "its argument", "take")},
		{"Bind of a transaction whose DBTX cannot copy", bindAs("Bind(tx db.Tx) Event"), "pgx",
			"repository.go:16:2: " + fmt.Sprintf(noCopyFrom, "what DBTX() of its argument returns", "have DBTX() return")},
		{"Bind of a repository declared before the one that copies", func(a string) string {
			return strings.Replace(a, "//kanna:repository\ntype Event interface {", "//kanna:repository\n"+
				"type Tag interface {\n\tBind(db kpgx.DBTX) Tag\n\tGet(ctx context.Context, name string) "+
				"(model.Tag, error)\n}\n\n//kanna:repository\ntype Event interface {", 1)
		}, "pgx", "repository.go:13:2: " + fmt.Sprintf(noCopyFrom, "its argument", "take")},
		{"Bind of a transaction whose DBTX copies", bindAs("Bind(tx db.CopyTx) Event"), "pgx", copyDBTX},
		{"a Copy written by hand calling its helper", func(a string) string {
			return a + `-- impl/copy.go --
package impl

import (
	"context"
	"errors"

	"example.com/shop/model"
)

func (r Event) Copy(ctx context.Context, rows []model.Event) (int64, error) {
	for _, row := range rows {
		if row.Kind == "" {
			return 0, errors.New("an event needs a kind")
		}
	}
	return r.copy(ctx, rows)
}
`
		}, "pgx", copyDBTX},
		{"a Copy written by hand calling no helper", func(a string) string {
			a = strings.Replace(a, "\tCopyTags(ctx context.Context, tags []model.Tag) (int64, error)\n", "", 1)
			return strings.Replace(a, bind, "\tBind(db kpgx.DBTX) Event\n", 1) + `-- impl/copy.go --
package impl

import (
	"context"

	"example.com/shop/model"
)

// Copy inserts the rows one by one, by no CopyFrom.
func (r Event) Copy(ctx context.Context, rows []model.Event) (int64, error) {
	for _, row := range rows {
		if _, err := r.db.Exec(ctx, "INSERT INTO events (kind) VALUES ($1)", row.Kind); err != nil {
			return 0, err
		}
	}
	return int64(len(rows)), nil
}
`
		}, "pgx", "type DBTX = kpgx.DBTX\n"},
		{"a query named after Copy", func(a string) string {
			return strings.Replace(a, "\tList(", "\tCopyKinds(ctx context.Context) error\n\tList(", 1) +
				"-- impl/queries.sql --\n-- query: CopyKinds\nINSERT INTO tags (name) SELECT kind FROM events;\n"
		}, "pgx", "error: CopyKinds: a Copy returns the number of rows it writes, as " +
			"(int64, error), written by hand as generated\n  name it after no verb to return anything else"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir, code, errOut := generateCopyShop(t, tt.change(copyShop), tt.driver)
			if strings.HasPrefix(tt.want, "type DBTX") {
				if code != cli.ExitOK {
					t.Fatalf("kanna-orm = %d\n%s", code, errOut)
				}
				src, err := os.ReadFile(filepath.Join(dir, "impl", "repository_gen.go")) //nolint:gosec // written above
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(src), tt.want) {
					t.Errorf("repository_gen.go has no %q\n%s", tt.want, src)
				}
				if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
					t.Fatalf("kanna-orm in scopes = %d\n%s", code, errOut)
				}
				moduleCommand(t, dir, "vet", "-mod=mod", "./...")
				return
			}
			if code != cli.ExitFailure || !strings.Contains(errOut, tt.want) {
				t.Fatalf("kanna-orm = %d\n%s\nwant\n%s", code, errOut, tt.want)
			}
		})
	}
}

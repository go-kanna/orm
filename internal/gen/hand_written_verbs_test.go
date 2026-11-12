package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/gen"
	"github.com/go-kanna/orm/internal/snapshot"
)

func TestCommand_HandWrittenVerbs(t *testing.T) {
	t.Parallel()
	const lookup, save, states = "Lookup(ctx context.Context, id int64) (model.Record, error)",
		"Save(ctx context.Context, record model.Record) (int64, error)", "States(ctx context.Context) ([]model.State, error)"
	for _, tt := range []struct {
		name, method, declared, want string
	}{
		{"a Get of one row", lookup, "GetRecord(ctx context.Context, id int64) (model.Record, error)", ""},
		{"a Find of one row", lookup, "FindRecord(ctx context.Context, id int64) (*model.Record, error)", ""},
		{"a List of rows", lookup, "ListRecords(ctx context.Context, id int64) ([]model.Record, error)", ""},
		{"a Get of rows", lookup, "GetRecords(ctx context.Context, id int64) ([]model.Record, error)",
			"GetRecords: a Get returns one row, as (T, error), and orm.ErrNotFound for none, written by hand as generated"},
		{"a Find of a value", lookup, "FindRecord(ctx context.Context, id int64) (model.Record, error)",
			"FindRecord: a Find returns one row, as (*T, error), and nil for none"},
		{"a List of one row", lookup, "ListRecord(ctx context.Context, id int64) (model.Record, error)",
			"ListRecord: a List returns every row, as ([]T, error)"},
		{"a Count of another type", states, "CountStates(ctx context.Context) (model.State, error)",
			"CountStates: a Count returns the one column of its result, as (int64, error)"},
		{"a Max of one column", states, "MaxState(ctx context.Context) (model.State, error)", ""},
		{"a Sum of rows", lookup, "SumRecords(ctx context.Context, id int64) ([]model.Record, error)",
			"SumRecords: a Sum returns the one column of its result, as (T, error) or (*T, error)"},
		{"an Avg of a string", states, "AvgState(ctx context.Context) (model.State, error)",
			"AvgState: an Avg returns the one column of its result, as (float64, error), (float32, error), or a " +
				"pointer to one"},
		{"an Insert returning a column as a count", save,
			"InsertRecord(ctx context.Context, record model.Record) (int64, error)",
			"InsertRecord: an Insert returns error alone, (int64, error) for the number of rows it affects"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if code, errOut := generateRenamedQuery(t, typedQueryShop, tt.method, tt.declared); tt.want == "" &&
				code != cli.ExitOK || tt.want != "" && (code == cli.ExitOK || !strings.Contains(errOut, tt.want)) {
				t.Fatalf("generate = %d, want %q\n%s", code, tt.want, errOut)
			}
		})
	}
}

// generateRenamedQuery generates the repositories of archive, a module of typedQueryShop's, with method, one of its
// methods a query implements, declared as declared, the query renamed after it, and reports what the run returned.
func generateRenamedQuery(t *testing.T, archive, method, declared string) (int, string) {
	t.Helper()
	return generateRewrittenQuery(t, archive, method, declared, "", "")
}

// generateRewrittenQuery is generateRenamedQuery with the SQL of the query from, as the snapshot describes it, written
// as to in archive.
func generateRewrittenQuery(t *testing.T, archive, method, declared, from, to string) (int, string) {
	t.Helper()
	old, _, _ := strings.Cut(method, "(")
	name, _, _ := strings.Cut(declared, "(")
	archive = strings.Replace(archive, method, declared, 1)
	archive = strings.Replace(archive, "-- query: "+old+"\n", "-- query: "+name+"\n", 1)
	dir := writeModule(t, archive)
	writeTypedQuerySnapshot(t, dir)
	path := filepath.Join(dir, "kanna-orm.snapshot.json")
	data, err := os.ReadFile(path) //nolint:gosec // snapshot written in this test's module
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.ReplaceAll(string(data), "impl."+old+`"`, "impl."+name+`"`))
	if from != "" {
		data = []byte(strings.Replace(string(data), snapshot.Digest(from), snapshot.Digest(to), 1))
	}
	if err := os.WriteFile(path, data, 0o600); err != nil { //nolint:gosec // test module snapshot
		t.Fatal(err)
	}
	code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", "../impl")
	return code, errOut
}

func TestCommand_ScopedQueries(t *testing.T) {
	t.Parallel()
	const lookup, states = "Lookup(ctx context.Context, id int64) (model.Record, error)",
		"States(ctx context.Context) ([]model.State, error)"
	extra := strings.Replace(typedQueryShop, "//kanna:repository", `type Shadowed struct {
	model.Record
	Other string `+"`orm:\"note\"`"+`
}
//kanna:repository`, 1)
	for _, tt := range []struct {
		name, archive, method, declared, want string
	}{
		{"a Get of the model", typedQueryShop, lookup,
			"GetRecord(ctx context.Context, id int64, s ...scopes.Record) (model.Record, error)", ""},
		{"a Count", typedQueryShop, lookup,
			"CountRecords(ctx context.Context, id int64, s ...scopes.Record) (model.Record, error)",
			"CountRecords: a query written by hand takes scopes as a Get, a Find, a First, or a List"},
		{"values of a column", typedQueryShop, states,
			"ListStates(ctx context.Context, s ...scopes.Record) ([]model.State, error)",
			"ListStates: with scopes of model.Record, it reads rows of model.Record, or of a struct embedding it"},
		{"a column held twice", extra, lookup,
			"GetShadowed(ctx context.Context, id int64, s ...scopes.Record) (Shadowed, error)",
			"Shadowed.Record.Note and repository.Shadowed.Other both hold the column note"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if code, errOut := generateRenamedQuery(t, tt.archive, tt.method, tt.declared); tt.want == "" &&
				code != cli.ExitOK || tt.want != "" && (code == cli.ExitOK || !strings.Contains(errOut, tt.want)) {
				t.Fatalf("generate = %d, want %q\n%s", code, tt.want, errOut)
			}
		})
	}
}

func TestListOf(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ before, after, want string }{
		{"WHERE id IN (", ")", "InList"},
		{"WHERE id NOT IN (", ") ORDER BY id", "InList"},
		{"WHERE id in\n  ( ", " )", "InList"},
		{"ORDER BY FIELD(id, ", ")", "List"},
		{"WHERE id IN (", ", 0)", "List"},
		{"WHERE id IN (0, ", ")", "List"},
		{"FROM a JOIN (", ")", "List"},
	} {
		if got := gen.ListOf(tt.before, tt.after); got != tt.want {
			t.Errorf("ListOf(%q, %q) = %s, want %s", tt.before, tt.after, got, tt.want)
		}
	}
}

func TestCommand_ScopedQueryLocks(t *testing.T) {
	t.Parallel()
	const (
		lookup   = "Lookup(ctx context.Context, id int64) (model.Record, error)"
		declared = "ListRecords(ctx context.Context, id int64, s ...scopes.Record) ([]model.Record, error)"
		sql      = "SELECT id, state, note FROM records WHERE id = :id;"
	)
	for _, tt := range []struct {
		name, archive, sql string
		ok                 bool
		want               string
	}{
		{"a lock in the query", typedQueryShop, "SELECT id, state, note FROM records WHERE id = :id\nFOR UPDATE SKIP LOCKED;",
			true, "queries.sql:5:1: warning: -- query: ListRecords: its FOR UPDATE locks the rows of the query before the " +
				"scopes narrow and page them\n  write the condition and the page in the query, and take no scopes"},
		{"a lock in a comment of the query", typedQueryShop,
			"SELECT id, state, note FROM records WHERE id = :id; -- not FOR UPDATE", true, ""},
		{"//kanna:lock", strings.Replace(typedQueryShop, "\tLookup(ctx", "\t//kanna:lock update\n\tLookup(ctx", 1), sql,
			false, "ListRecords: //kanna:lock is for a method kanna-orm writes the SQL of\n" +
				"  lock in -- query: ListRecords, with the condition and the page written there, and take no scopes\n" +
				"  a lock there with scopes takes the rows before the scopes narrow and page them"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			archive := strings.Replace(tt.archive, sql, tt.sql, 1)
			code, errOut := generateRewrittenQuery(t, archive, lookup, declared, sql, tt.sql)
			if (code == cli.ExitOK) != tt.ok || !strings.Contains(errOut, tt.want) ||
				tt.want == "" && strings.Contains(errOut, "lock") {
				t.Fatalf("generate = %d, want %q\n%s", code, tt.want, errOut)
			}
		})
	}
}

func TestCommand_ScopedQueryOrder(t *testing.T) {
	t.Parallel()
	const (
		lookup   = "Lookup(ctx context.Context, id int64) (model.Record, error)"
		declared = "ListRecords(ctx context.Context, id int64, s ...scopes.Record) ([]model.Record, error)"
		sql      = "SELECT id, state, note FROM records WHERE id = :id;"
		warning  = "queries.sql:3:1: warning: -- query: ListRecords: its ORDER BY orders no rows the scopes read\n" +
			"  order them by the scopes, or keep the ORDER BY with a LIMIT"
	)
	for _, tt := range []struct{ name, sql, want string }{
		{"an ORDER BY", "SELECT id, state, note FROM records WHERE id = :id ORDER BY note;", warning},
		{"an ORDER BY with a LIMIT", "SELECT id, state, note FROM records WHERE id = :id ORDER BY note LIMIT 5;", ""},
		{"a name holding order", "SELECT sort_order.id, state, note FROM records AS sort_order WHERE id = :id;", ""},
		{"an ORDER BY a name holding limit",
			"SELECT id, state, note FROM records AS limit_count WHERE id = :id ORDER BY limit_count.note;", warning},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			archive := strings.Replace(typedQueryShop, sql, tt.sql, 1)
			code, errOut := generateRewrittenQuery(t, archive, lookup, declared, sql, tt.sql)
			if code != cli.ExitOK || !strings.Contains(errOut, tt.want) ||
				tt.want == "" && strings.Contains(errOut, "ORDER BY") {
				t.Fatalf("generate = %d, want %q\n%s", code, tt.want, errOut)
			}
		})
	}
}

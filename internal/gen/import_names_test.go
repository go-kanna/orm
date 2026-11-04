package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

// TestCommand_ImportsBeforeLocals checks that a local kanna-orm names after a field yields to the package of the
// field's type, which keeps its name: the array a bulk insert of a field named Time writes is time2, of time.Time.
func TestCommand_ImportsBeforeLocals(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, `
-- model/model.go --
package model

import "time"

type Event struct {
	ID   int64
	Time time.Time
}
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=events
type Event struct{ q scope.Of[model.Event] }
-- repository/repository.go --
package repository

import (
	"context"

	"example.com/shop/model"
)

//kanna:repository
type Events interface {
	InsertAll(ctx context.Context, events []model.Event) error
}
`)
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"events": {Columns: []snapshot.Column{
			{Name: "id", Type: "int8", NotNull: true}, {Name: "time", Type: "timestamptz", NotNull: true},
		}, PrimaryKey: []string{"id"}},
	}}
	if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
		t.Fatalf("generate scopes = %d\n%s", code, errOut)
	}
	if code, _, errOut := run(t, filepath.Join(dir, "repository"), "-destination", "../impl"); code != cli.ExitOK {
		t.Fatalf("generate repository = %d\n%s", code, errOut)
	}
	generated, err := os.ReadFile(filepath.Join(dir, "impl", "repository_gen.go")) //nolint:gosec // the test's module
	if err != nil {
		t.Fatal(err)
	}
	if src := string(generated); strings.Contains(src, `time2 "time"`) ||
		!strings.Contains(src, "time2 := make([]time.Time, len(events))") {
		t.Errorf("the time package yields its name to the local of Time:\n%s", src)
	}
}

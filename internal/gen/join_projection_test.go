package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

// joinedProjectionShop is a module whose records refer to owners by owner_id and by creator_id, and to teams, which
// have no scope type, by team_id. Card is the projection its repository reads, whose fields FIELDS stands for, by the
// method METHOD stands for.
const joinedProjectionShop = `
-- model/model.go --
package model
type Record struct { ID int64; OwnerID int64; CreatorID int64; TeamID int64 }
type Owner struct { ID int64; Name *string }
type Card struct { FIELDS }
-- scopes/scopes.go --
package scopes
import (
 "example.com/shop/model"
 "github.com/go-kanna/orm/scope"
)
//kanna:scope table=records
type Record struct { q scope.Of[model.Record] }
//kanna:scope table=owners
type Owner struct { q scope.Of[model.Owner] }
-- repository/repository.go --
package repository
import (
 "context"
 "example.com/shop/model"
 "example.com/shop/scopes"
)
//kanna:repository
type Records interface {
 METHOD
}
`

func writeJoinedProjectionShop(t *testing.T, fields, method string, twoTables bool) string {
	t.Helper()
	dir := writeModule(t, strings.NewReplacer("FIELDS", fields, "METHOD", method).Replace(joinedProjectionShop))
	records := snapshot.Table{Columns: []snapshot.Column{
		{Name: "id", Type: "int8", NotNull: true}, {Name: "owner_id", Type: "int8", NotNull: true},
		{Name: "creator_id", Type: "int8", NotNull: true}, {Name: "team_id", Type: "int8", NotNull: true},
	}, PrimaryKey: []string{"id"}, ForeignKeys: []snapshot.ForeignKey{
		{Columns: []string{"owner_id"}, References: "owners", ReferencedColumns: []string{"id"}},
		{Columns: []string{"creator_id"}, References: "owners", ReferencedColumns: []string{"id"}},
		{Columns: []string{"team_id"}, References: "teams", ReferencedColumns: []string{"id"}},
	}}
	if twoTables {
		records.ForeignKeys = append(records.ForeignKeys,
			snapshot.ForeignKey{Columns: []string{"owner_id"}, References: "teams", ReferencedColumns: []string{"id"}})
	}
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"records": records,
		"owners": {Columns: []snapshot.Column{{Name: "id", Type: "int8", NotNull: true}, {Name: "name", Type: "text"}},
			PrimaryKey: []string{"id"}},
		"teams": {Columns: []snapshot.Column{{Name: "id", Type: "int8", NotNull: true}, {Name: "name", Type: "text"}},
			PrimaryKey: []string{"id"}},
	}}
	if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, out := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
		t.Fatalf("generate scopes = %d\n%s", code, out)
	}
	return dir
}

const listCards = "List(ctx context.Context, s ...scopes.Record) ([]model.Card, error)"

func TestCommand_JoinedProjection(t *testing.T) {
	t.Parallel()
	dir := writeJoinedProjectionShop(t, "ID int64\nOwner struct{ Name *string }\n"+
		"Creator *struct{ Name *string }", listCards, false)
	if code, _, out := run(t, filepath.Join(dir, "repository"), "-destination", "../impl"); code != cli.ExitOK {
		t.Fatalf("generate = %d\n%s", code, out)
	}
	generated, err := os.ReadFile(filepath.Join(dir, "impl", "repository_gen.go")) //nolint:gosec // the test's module
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"SELECT records.id, owner.name, creator.id IS NOT NULL, creator.name",
		" INNER JOIN owners AS owner ON owner.id = records.owner_id LEFT JOIN owners AS creator ON creator.id = " +
			"records.creator_id",
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("List reads\n%s\nwant %s", generated, want)
		}
	}
}

func TestCommand_JoinedProjectionMistakes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, fields, method, want string
		twoTables                  bool
	}{
		{name: "no foreign key of the name", fields: "Maker struct{ Name *string }",
			want: `records.owner_id to owners joins it: tag the field orm:"fk=owner_id"`},
		{name: "two foreign keys of the column", fields: "Owner struct{ Name *string }", twoTables: true,
			want: "two foreign keys of records.owner_id refer to owners and teams"},
		{name: "a table of no scope type", fields: "Team struct{ Name *string }",
			want: "no scope type is declared for teams, whose model the fields of Team are matched to"},
		{name: "a model", fields: "Owner *Owner", want: "Owner is a model, whose rows a relation reads"},
		{name: "the alias of the table read", fields: "Records struct{ Name *string } `orm:\"fk=owner_id\"`",
			want: "Records joins its table as records, as which the table records is read already"},
		{name: "two fields of one alias", fields: "Owner struct{ Name *string }\n" +
			"OWNER struct{ Name *string } `orm:\"fk=creator_id\"`", want: "OWNER joins its table as owner, as which Owner"},
		{name: "a field the model has not", fields: "Owner struct{ Missing string }",
			want: "the model of owners has no field Missing"},
		{name: "a type unlike the model's", fields: "Owner struct{ Name string }",
			want: "projection field Owner.Name has type string; the model field has type *string"},
		{name: "a table two joins away", fields: "Owner struct{ Team struct{ Name *string } }",
			want: "Owner.Team joins a table through Owner"},
		{name: "a column tag on the struct", fields: "Owner struct{ Name *string } `orm:\"owner\"`",
			want: `orm:"owner" names a column, and Owner holds the columns of a table joined`},
		{name: "a lock", fields: "Owner struct{ Name *string }", method: "//kanna:lock update\n" + listCards,
			want: "Owner joins a table, and a projection joining one is read without a lock"},
		{name: "a Delete", fields: "Owner struct{ Name *string }",
			method: "Delete(ctx context.Context, s ...scopes.Record) ([]model.Card, error)",
			want:   "a Delete reading a projection joins no table, as Owner would"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			method := tc.method
			if method == "" {
				method = listCards
			}
			dir := writeJoinedProjectionShop(t, "ID int64\n"+tc.fields, method, tc.twoTables)
			code, _, out := run(t, filepath.Join(dir, "repository"), "-destination", "../impl")
			if code != cli.ExitFailure || !strings.Contains(out, tc.want) {
				t.Fatalf("generate = %d\n%s\nwant %s", code, out, tc.want)
			}
		})
	}
}

func TestCommand_SelfJoinedProjection(t *testing.T) {
	t.Parallel()
	dir := writeModule(t, `
-- model/model.go --
package model
type Category struct { ID int64; ParentID *int64; Name string }
type Path struct { Name string; Parent *struct{ Name string } }
-- scopes/scopes.go --
package scopes
import (
 "example.com/shop/model"
 "github.com/go-kanna/orm/scope"
)
//kanna:scope table=categories
type Category struct { q scope.Of[model.Category] }
-- repository/repository.go --
package repository
import (
 "context"
 "example.com/shop/model"
 "example.com/shop/scopes"
)
//kanna:repository
type Categories interface {
 ListPaths(ctx context.Context, s ...scopes.Category) ([]model.Path, error)
}
`)
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"categories": {Columns: []snapshot.Column{
			{Name: "id", Type: "int8", NotNull: true}, {Name: "parent_id", Type: "int8"},
			{Name: "name", Type: "text", NotNull: true},
		}, PrimaryKey: []string{"id"}, ForeignKeys: []snapshot.ForeignKey{
			{Columns: []string{"parent_id"}, References: "categories", ReferencedColumns: []string{"id"}},
		}},
	}}
	if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, out := run(t, filepath.Join(dir, "scopes")); code != cli.ExitOK {
		t.Fatalf("generate scopes = %d\n%s", code, out)
	}
	if code, _, out := run(t, filepath.Join(dir, "repository"), "-destination", "../impl"); code != cli.ExitOK {
		t.Fatalf("generate repository = %d\n%s", code, out)
	}
	generated, err := os.ReadFile(filepath.Join(dir, "impl", "repository_gen.go")) //nolint:gosec // the test's module
	if err != nil {
		t.Fatal(err)
	}
	if want := " LEFT JOIN categories AS parent ON parent.id = categories.parent_id"; !strings.Contains(string(generated),
		want) {
		t.Errorf("ListPaths reads\n%s\nwant %s", generated, want)
	}
}

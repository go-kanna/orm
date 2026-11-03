package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

func TestCommand_JoinedProjectionMistakes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, field, want string
		noFK              bool
	}{
		{"unknown column", `Label string ` + "`orm:\"owners.missing\"`", "no column", false},
		{"nullable column", `Label string ` + "`orm:\"owners.name\"`", "may be NULL", false},
		{"ambiguous source", "Name *string", "matches multiple", false},

		{"FK-less unknown column", `Label string ` + "`orm:\"owners.missing\"`", "no column", true},
		{"FK-less nullable column", `Label string ` + "`orm:\"owners.name\"`", "may be NULL", true},
		{"FK-less unqualified column", "Name *string", "no column", true},
		{"unknown table", `Label string ` + "`orm:\"missing.name\"`", "no column", true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeModule(t, `
-- model/model.go --
package model
type Record struct { ID int64; OwnerID int64 }
type Card struct { `+tc.field+` }
-- scopes/scopes.go --
package scopes
import (
 "example.com/shop/model"
 "github.com/go-kanna/orm/scope"
)
//kanna:scope table=records
type Record struct { q scope.Of[model.Record] }
-- repository/repository.go --
package repository
import (
 "context"
 "example.com/shop/model"
 "example.com/shop/scopes"
)
//kanna:repository
type Records interface { List(ctx context.Context,s ...scopes.Record) ([]model.Card,error) }
`)
			s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
				"records": {Columns: []snapshot.Column{
					{Name: "id", Type: "int8", NotNull: true}, {Name: "owner_id", Type: "int8", NotNull: true},
				}, PrimaryKey: []string{"id"}, ForeignKeys: []snapshot.ForeignKey{
					{Columns: []string{"owner_id"}, References: "owners", ReferencedColumns: []string{"id"}},
					{Columns: []string{"owner_id"}, References: "teams", ReferencedColumns: []string{"id"}},
				}},
				"owners": {Columns: []snapshot.Column{{Name: "id", Type: "int8", NotNull: true}, {Name: "name", Type: "text"}}},
				"teams":  {Columns: []snapshot.Column{{Name: "id", Type: "int8", NotNull: true}, {Name: "name", Type: "text"}}},
			}}
			if tc.noFK {
				records := s.Tables["records"]
				records.ForeignKeys = nil
				s.Tables["records"] = records
			}
			if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
				t.Fatal(err)
			}
			code, _, out := run(t, filepath.Join(dir, "repository"), "-destination", "../impl")
			if code != cli.ExitFailure || !strings.Contains(out, tc.want) {
				t.Fatalf("generate = %d\n%s\nwant %s", code, out, tc.want)
			}
		})
	}
}

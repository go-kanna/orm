package gen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/snapshot"
)

const scalarRelationModel = `-- model/model.go --
package model
type Record struct {
    ID int64
    Topics []string ` + "`orm:\"relation=record_states,column=state\"`" + `
}
-- scopes/scopes.go --
package scopes
import (
    "example.com/shop/model"
    "github.com/go-kanna/orm"
)
//kanna:scope table=records
type Record struct { q orm.Of[model.Record] }
`

func TestCommand_ScalarRelationDiagnostics(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, before, after, want string
		change                    func(*snapshot.Table)
	}{
		{name: "missing table", before: "relation=record_states", after: "relation=missing", want: "no table missing"},
		{name: "missing column", before: "column=state", after: "column=missing", want: "has no column missing"},
		{name: "missing value option", before: ",column=state", want: "requires column="},
		{name: "unknown option", before: "column=state", after: "column=state,unknown=x",
			want: "unknown scalar relation option"},
		{name: "not a slice", before: "[]string", after: "string", want: "requires a slice"},
		{name: "incompatible value", before: "[]string", after: "[]int64", want: "cannot hold record_states.state"},
		{name: "nullable value", want: "including its nullability", change: func(table *snapshot.Table) {
			table.Columns[1].NotNull = false
		}},
		{name: "missing foreign key", want: "no matching foreign key", change: func(table *snapshot.Table) {
			table.ForeignKeys = nil
		}},
		{name: "ambiguous foreign key", want: "multiple foreign keys", change: func(table *snapshot.Table) {
			table.Columns = append(table.Columns, snapshot.Column{Name: "other_record_id", Type: "int8", NotNull: true})
			table.ForeignKeys = append(table.ForeignKeys, snapshot.ForeignKey{Columns: []string{"other_record_id"},
				References: "records", ReferencedColumns: []string{"id"}})
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			archive := scalarRelationModel
			if tt.before != "" {
				archive = strings.Replace(archive, tt.before, tt.after, 1)
			}
			dir := writeModule(t, archive)
			table := snapshot.Table{Columns: []snapshot.Column{
				{Name: "record_id", Type: "int8", NotNull: true}, {Name: "state", Type: "text", NotNull: true},
			}, ForeignKeys: []snapshot.ForeignKey{{Columns: []string{"record_id"}, References: "records",
				ReferencedColumns: []string{"id"}}}}
			if tt.change != nil {
				tt.change(&table)
			}
			s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
				"records": {Columns: []snapshot.Column{{Name: "id", Type: "int8", NotNull: true}},
					PrimaryKey: []string{"id"}},
				"record_states": table,
			}}
			if err := os.WriteFile(filepath.Join(dir, snapshot.Name), snapshot.Marshal(s), 0o600); err != nil {
				t.Fatal(err)
			}
			code, _, errOut := run(t, filepath.Join(dir, "scopes"))
			if code == cli.ExitOK || !strings.Contains(errOut, tt.want) {
				t.Fatalf("generate = %d, want %q\n%s", code, tt.want, errOut)
			}
		})
	}
}

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
    "github.com/go-kanna/orm/scope"
)
//kanna:scope table=records
type Record struct { q scope.Of[model.Record] }
`

func TestCommand_ScalarRelationDiagnostics(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, before, after, want string
		change                    func(*snapshot.Table)
	}{
		{name: "missing table", before: "relation=record_states", after: "relation=missing", want: "no table missing"},
		{name: "missing column", before: "column=state", after: "column=missing", want: "the snapshot has no column record_states.missing"},
		{name: "missing inferred column", before: ",column=state", want: "has no column whose plural is Topics"},
		{name: "ambiguous inferred column", before: "Topics []string `orm:\"relation=record_states,column=state\"`",
			after: "Leaves []string `orm:\"relation=record_states\"`",
			want:  "leaf, leave are all columns of record_states whose plural is Leaves", change: func(table *snapshot.Table) {
				table.Columns = append(table.Columns, snapshot.Column{Name: "leaf", Type: "text", NotNull: true},
					snapshot.Column{Name: "leave", Type: "text", NotNull: true})
			}},
		{name: "unknown option", before: "column=state", after: "column=state,unknown=x",
			want: "unknown scalar relation option"},
		{name: "single value", before: "[]string", after: "string",
			want: "a value picked from them is written in SQL"},
		{name: "incompatible value", before: "[]string", after: "[]int64", want: "cannot hold record_states.state"},
		{name: "nullable value", want: "including its nullability", change: func(table *snapshot.Table) {
			table.Columns[1].NotNull = false
		}},
		{name: "missing foreign key", want: "no foreign key of record_states.record_id to records reads it",
			change: func(table *snapshot.Table) { table.ForeignKeys = nil }},
		{name: "foreign key of another name", want: "record_states.other_record_id to records reads it: name its " +
			"columns with fk=other_record_id", change: func(table *snapshot.Table) {
			table.Columns = append(table.Columns, snapshot.Column{Name: "other_record_id", Type: "int8", NotNull: true})
			table.ForeignKeys = []snapshot.ForeignKey{{Columns: []string{"other_record_id"}, References: "records",
				ReferencedColumns: []string{"id"}}}
		}},
		{name: "two foreign keys of the name", want: "two foreign keys of record_states.record_id refer to records",
			change: func(table *snapshot.Table) { table.ForeignKeys = append(table.ForeignKeys, table.ForeignKeys[0]) }},
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

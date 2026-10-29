package snapshot_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kanna/orm/internal/snapshot"
)

func TestDiff(t *testing.T) {
	t.Parallel()

	const key = "example.com/shop/repository.MonthCounts"
	tests := []struct {
		name   string
		change func(s *snapshot.Snapshot)
		want   []string
	}{
		{"nothing", func(*snapshot.Snapshot) {}, nil},
		{"another dialect", func(s *snapshot.Snapshot) { s.Dialect = snapshot.MySQL },
			[]string{"the snapshot is of postgresql, but the database is mysql"}},
		{"a table added", func(s *snapshot.Snapshot) { s.Tables["refunds"] = snapshot.Table{} },
			[]string{"table refunds is in the database but not in the snapshot"}},
		{"a table dropped", func(s *snapshot.Snapshot) { delete(s.Tables, "customers") },
			[]string{"table customers is in the snapshot but not in the database"}},
		{"a column added", func(s *snapshot.Snapshot) {
			t := s.Tables["orders"]
			t.Columns = append(slices.Clone(t.Columns), snapshot.Column{Name: "memo", Type: "text"})
			s.Tables["orders"] = t
		}, []string{"orders.memo (text) is in the database but not in the snapshot"}},
		{"a column dropped", func(s *snapshot.Snapshot) {
			t := s.Tables["orders"]
			t.Columns = slices.DeleteFunc(slices.Clone(t.Columns), func(c snapshot.Column) bool { return c.Name == "note" })
			s.Tables["orders"] = t
		}, []string{"orders.note is in the snapshot but not in the database"}},
		{"a column changed", func(s *snapshot.Snapshot) {
			t := s.Tables["orders"]
			t.Columns = slices.Clone(t.Columns)
			t.Columns[3] = snapshot.Column{Name: "status", Type: "varchar", NotNull: true}
			s.Tables["orders"] = t
		}, []string{
			"orders.status is varchar, not null in the database but text, not null, with a default in the snapshot",
		}},
		{"columns in another order", func(s *snapshot.Snapshot) {
			t := s.Tables["orders"]
			t.Columns = slices.Clone(t.Columns)
			t.Columns[0], t.Columns[1] = t.Columns[1], t.Columns[0]
			s.Tables["orders"] = t
		}, []string{"the columns of orders are in another order"}},
		{"keys changed", func(s *snapshot.Snapshot) {
			t := s.Tables["orders"]
			t.PrimaryKey = []string{"number"}
			t.UniqueKeys = [][]string{{"number"}}
			t.ForeignKeys = nil
			s.Tables["orders"] = t
		}, []string{
			"the primary key of orders is not the snapshot's",
			"the unique keys of orders are not the snapshot's",
			"the foreign keys of orders are not the snapshot's",
		}},
		{"keys in another order", func(s *snapshot.Snapshot) {
			t := s.Tables["orders"]
			t.UniqueKeys = [][]string{{"customer_id", "number"}, {"number"}}
			s.Tables["orders"] = t
		}, nil},
		{"a statement added", func(s *snapshot.Snapshot) {
			s.Statements["example.com/shop/repository.Refund"] = snapshot.Statement{}
		}, []string{"example.com/shop/repository.Refund is in the .sql files but not in the snapshot"}},
		{"a statement removed", func(s *snapshot.Snapshot) { delete(s.Statements, key) },
			[]string{key + " is in the snapshot but in no .sql file"}},
		{"a statement rewritten", func(s *snapshot.Snapshot) {
			st := s.Statements[key]
			st.Digest = "1111111111111111"
			s.Statements[key] = st
		}, []string{key + " has been rewritten since it was described"}},
		{"a statement described otherwise", func(s *snapshot.Snapshot) {
			st := s.Statements[key]
			st.Columns = []snapshot.Result{{Name: "month", Type: "date"}}
			s.Statements[key] = st
		}, []string{key + " is described otherwise by the database"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			now := shop()
			tt.change(&now)
			if got := snapshot.Diff(shop(), now); !slices.Equal(got, tt.want) {
				t.Errorf("Diff =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

package gen_test

import (
	"testing"

	"github.com/go-kanna/orm/internal/gen"
	"github.com/go-kanna/orm/internal/snapshot"
)

func TestFields_ReadOnlyMistakes(t *testing.T) {
	t.Parallel()

	table := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"orders": {Columns: []snapshot.Column{
			{Name: "id", Type: "int8", NotNull: true},
			{Name: "version", Type: "int8", NotNull: true},
			{Name: "serial", Type: "int8", NotNull: true, Assigned: true},
			{Name: "updated_at", Type: "timestamptz", NotNull: true},
			{Name: "modified_at", Type: "timestamptz", NotNull: true},
		}},
	}}
	tests := []struct {
		name, field, want string
	}{
		{"with default", "Version int64 `orm:\",readonly,default\"`",
			"model.go:7:2: error: Order.Version: readonly leaves orders.version out of every write already, default " +
				"too\n  remove default"},
		{"with insertonly", "Version int64 `orm:\",insertonly,readonly\"`",
			"model.go:7:2: error: Order.Version: readonly leaves orders.version out of every write already, " +
				"insertonly too\n  remove insertonly"},
		{"a column the database assigns", "Serial int64 `orm:\",readonly\"`",
			"model.go:7:2: error: Order.Serial: the database assigns orders.serial already, which no write of " +
				"kanna-orm writes\n  remove readonly"},
		{"the update time by tag", "ModifiedAt time.Time `orm:\",updated,readonly\"`",
			"model.go:7:2: error: Order.ModifiedAt: a time a row is updated at that the database writes is tagged " +
				"default, not readonly\n" + `  tag it orm:",updated,default"`},
		{"the update time by name", "UpdatedAt time.Time `orm:\",readonly\"`",
			"model.go:7:2: error: Order.UpdatedAt: updated_at is the time a row is updated, which the database " +
				"writes when tagged default, not readonly\n  replace readonly with default"},
		{"a relation", "Lines []OrderLine `orm:\",readonly\"`",
			`model.go:7:2: error: Order.Lines: orm:",readonly" is for a column, and Lines is a relation`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pkg := module(t, orderScope+`
-- model/model.go --
package model

import "time"

type Order struct {
	ID int64
	`+tt.field+`
}

type OrderLine struct{ ID int64 }

var _ time.Time
`)
			_, ds := gen.Fields(pkg, table)
			if got := report(ds); got != tt.want {
				t.Errorf("Fields reports\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

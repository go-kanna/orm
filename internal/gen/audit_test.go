package gen_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/orm/internal/gen"
	"github.com/go-kanna/orm/internal/snapshot"
)

// auditTable is orders, with columns of times for the fields of a test to hold.
var auditTable = snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
	"orders": {Columns: []snapshot.Column{
		{Name: "id", Type: "int8", NotNull: true},
		{Name: "created_at", Type: "timestamptz", NotNull: true},
		{Name: "updated_at", Type: "timestamptz", NotNull: true, Default: true},
		{Name: "inserted_at", Type: "timestamptz", NotNull: true},
		{Name: "modified_at", Type: "timestamptz", NotNull: true, Default: true},
		{Name: "base_created_at", Type: "timestamptz", NotNull: true},
		{Name: "deleted_at", Type: "timestamptz"},
		{Name: "note", Type: "text", NotNull: true},
	}},
}}

func TestFields_Audit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, fields, decls string
		want                []string
	}{
		{"by name", "\tCreatedAt time.Time\n\tUpdatedAt time.Time\n", "",
			[]string{"Order.CreatedAt time.Time created_at created", "Order.UpdatedAt time.Time updated_at updated"}},
		{"by name, of a struct embedded", "\tAudit\n", "type Audit struct {\n\tCreatedAt time.Time\n" +
			"\tUpdatedAt time.Time\n}\n",
			[]string{"Order.Audit.CreatedAt time.Time created_at created",
				"Order.Audit.UpdatedAt time.Time updated_at updated"}},
		{"by name, holding a column of another name", "\tCreatedAt time.Time `orm:\"inserted_at\"`\n", "",
			[]string{"Order.CreatedAt time.Time inserted_at created"}},
		{"by tag", "\tInsertedAt time.Time `orm:\",created\"`\n\tModifiedAt time.Time `orm:\",updated\"`\n", "",
			[]string{"Order.InsertedAt time.Time inserted_at created", "Order.ModifiedAt time.Time modified_at updated"}},
		{"by tag, which the database assigns", "\tUpdatedAt time.Time `orm:\",default\"`\n" +
			"\tModifiedAt time.Time `orm:\",created,default\"`\n", "",
			[]string{"Order.UpdatedAt time.Time updated_at updated", "Order.ModifiedAt time.Time modified_at created"}},
		{"by name and by tag, which a trigger assigns, as no default tells",
			"\tCreatedAt time.Time `orm:\",default\"`\n\tInsertedAt time.Time `orm:\",updated,default\"`\n", "",
			[]string{"Order.CreatedAt time.Time created_at created", "Order.InsertedAt time.Time inserted_at updated"}},
		{"a tag over a name", "\tCreatedAt time.Time\n\tInsertedAt time.Time `orm:\",created\"`\n" +
			"\tUpdatedAt time.Time\n", "",
			[]string{"Order.CreatedAt time.Time created_at", "Order.InsertedAt time.Time inserted_at created",
				"Order.UpdatedAt time.Time updated_at updated"}},
		{"a tag of the other kind over a name", "\tCreatedAt time.Time `orm:\",updated\"`\n", "",
			[]string{"Order.CreatedAt time.Time created_at updated"}},
		{"a time of another name", "\tDeletedAt *time.Time\n", "", []string{"Order.DeletedAt *time.Time deleted_at"}},
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
`+tt.fields+"}\n\n"+tt.decls)
			got, ds := gen.Fields(pkg, auditTable)
			if len(ds) > 0 {
				t.Fatalf("Fields:\n%s", report(ds))
			}
			if want := append([]string{"Order.ID int64 id"}, tt.want...); !reflect.DeepEqual(got, want) {
				t.Errorf("Fields =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

func TestFields_AuditMistakes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, fields, decls, want string
	}{
		{"a pointer by name", "\tUpdatedAt *time.Time\n", "",
			"model.go:7:2: error: Order.UpdatedAt: updated_at, the time a row is updated, is a time.Time, not a " +
				"*time.Time\n  declare it as time.Time\n" +
				`  or, for a column kanna-orm leaves to you, name the field otherwise, holding the column by ` +
				`orm:"updated_at"`},
		{"a pointer by tag", "\tDeletedAt *time.Time `orm:\",updated\"`\n", "",
			"model.go:7:2: error: Order.DeletedAt: deleted_at, the time a row is updated, is a time.Time, not a " +
				"*time.Time\n  declare it as time.Time"},
		{"a type of its own", "\tCreatedAt Stamp\n", "type Stamp time.Time\n\nfunc (s *Stamp) Scan(any) error { " +
			"return nil }\n",
			"model.go:7:2: error: Order.CreatedAt: created_at, the time a row is created, is a time.Time, not a " +
				"Stamp\n  declare it as time.Time\n" +
				`  or, for a column kanna-orm leaves to you, name the field otherwise, holding the column by ` +
				`orm:"created_at"`},
		{"a string by tag", "\tNote string `orm:\",created\"`\n", "",
			"model.go:7:2: error: Order.Note: note, the time a row is created, is a time.Time, not a string\n" +
				"  declare it as time.Time"},
		{"insertonly on the creation time", "\tCreatedAt time.Time `orm:\",insertonly\"`\n", "",
			"model.go:7:2: error: Order.CreatedAt: created_at is the time a row is created, and an insert alone " +
				"writes it already\n  remove insertonly"},
		{"insertonly on the update time", "\tModifiedAt time.Time `orm:\",updated,insertonly\"`\n", "",
			"model.go:7:2: error: Order.ModifiedAt: modified_at is the time a row is updated, and every write of its " +
				"row writes it, which insertonly would leave out\n  remove insertonly"},
		{"two tagged", "\tInsertedAt time.Time `orm:\",created\"`\n\tModifiedAt time.Time `orm:\",created\"`\n", "",
			"model.go:8:2: error: Order.ModifiedAt: Order.InsertedAt is the time a row of orders is created already\n" +
				"  tag one field alone created"},
		{"two by name", "\tCreatedAt time.Time\n\tBase\n",
			"type Base struct {\n\tCreatedAt time.Time `orm:\"base_created_at\"`\n}\n",
			"model.go:14:2: error: Order.Base.CreatedAt: Order.CreatedAt is the time a row of orders is created " +
				"already\n" + `  leave one of them out with orm:"-", or tag the one meant created`},
		{"both tags", "\tInsertedAt time.Time `orm:\",created,updated\"`\n", "",
			"model.go:7:2: error: Order.InsertedAt: a column is the time its row was created or the time it was " +
				"updated, not both"},
		{"a tag twice", "\tInsertedAt time.Time `orm:\",created,created\"`\n", "",
			`model.go:7:2: error: Order.InsertedAt: unknown or duplicate orm option "created"`},
		{"default of no database default", "\tNote string `orm:\",default\"`\n", "",
			"model.go:7:2: error: Order.Note: default requires orders.note to have a database default"},
		{"default of no database default, by a name a tag makes no audit column",
			"\tCreatedAt time.Time `orm:\",default\"`\n\tInsertedAt time.Time `orm:\",created\"`\n", "",
			"model.go:7:2: error: Order.CreatedAt: default requires orders.created_at to have a database default"},
		{"a tag on a relation", "\tLines []OrderLine `orm:\",created\"`\n", "type OrderLine struct{ ID int64 }\n",
			`model.go:7:2: error: Order.Lines: orm:",created" is for a column, and Lines is a relation`},
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
`+tt.fields+"}\n\nvar _ time.Time\n\n"+tt.decls)
			_, ds := gen.Fields(pkg, auditTable)
			if got := report(ds); got != tt.want {
				t.Errorf("Fields reports\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

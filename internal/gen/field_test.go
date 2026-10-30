package gen_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/orm/internal/gen"
	"github.com/go-kanna/orm/internal/snapshot"
)

// orderScope declares the scope type of orders, whose model a test declares in the package model.
const orderScope = `
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm"
)

//kanna:scope table=orders
type Order struct{ q orm.Of[model.Order] }
`

func TestFields(t *testing.T) {
	t.Parallel()

	pkg := module(t, orderScope+`
-- model/model.go --
package model

import "time"

type Order struct {
	ID         int64
	CustomerID CustomerID
	Status     OrderStatus
	PlacedAt   time.Time
	Memo       *string `+"`orm:\"note\"`"+`
	Tags       []string
	Receipt    []byte
	Contact    *Email
	Scores     []*float64
	Ignored    string `+"`orm:\"-\"`"+`
	draft      bool   `+"`orm:\"-\"`"+`
	_          int
	Audit
	Lines      []OrderLine
	Customer   *Customer `+"`orm:\"fk=customer_id\"`"+`
	Coupon     Coupon
	Refunds    []*Refund
}

type (
	CustomerID  int64
	OrderStatus string
)

// Email is read through Scan, as a value type is.
type Email struct{ s string }

func (e *Email) Scan(any) error { return nil }

type Audit struct {
	CreatedAt time.Time
	UpdatedAt *time.Time
}

type (
	OrderLine struct{ ID int64 }
	Customer  struct{ ID int64 }
	Coupon    struct{ Code string }
	Refund    struct{ ID int64 }
)
`)
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"orders": {Columns: []snapshot.Column{
			{Name: "id", Type: "int8", NotNull: true, Assigned: true},
			{Name: "customer_id", Type: "int8", NotNull: true},
			{Name: "status", Type: "text", NotNull: true},
			{Name: "placed_at", Type: "timestamptz", NotNull: true},
			{Name: "note", Type: "text"},
			{Name: "tags", Type: "_text"},
			{Name: "receipt", Type: "bytea"},
			{Name: "contact", Type: "text"},
			{Name: "scores", Type: "_float8"},
			{Name: "created_at", Type: "timestamptz", NotNull: true, Default: true},
			{Name: "updated_at", Type: "timestamptz"},
			{Name: "legacy", Type: "text"},
		}},
	}}
	got, ds := gen.Fields(pkg, s)
	if len(ds) > 0 {
		t.Fatalf("Fields:\n%s", report(ds))
	}
	want := []string{
		"Order.ID int64 id",
		"Order.CustomerID model.CustomerID customer_id",
		"Order.Status model.OrderStatus status",
		"Order.PlacedAt time.Time placed_at",
		"Order.Memo *string note",
		"Order.Tags []string tags",
		"Order.Receipt []byte receipt",
		"Order.Contact *model.Email contact",
		"Order.Scores []*float64 scores",
		"Order.Audit.CreatedAt time.Time created_at",
		"Order.Audit.UpdatedAt *time.Time updated_at",
		"Order.Lines []model.OrderLine relation",
		"Order.Customer *model.Customer relation fk=customer_id",
		"Order.Coupon model.Coupon relation",
		"Order.Refunds []*model.Refund relation",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Fields =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestFields_Mistakes(t *testing.T) {
	t.Parallel()

	// The package model is read from export data, which keeps the line of a field but not its column.
	tests := []struct {
		name, fields, decls, want string
	}{
		{"a tag it cannot read", "\tMemo *string `orm:\"note,omitempty\"`\n", "",
			`model.go:8:1: error: Order.Memo: orm:"note,omitempty" is neither a column, -, nor fk=<column>`},
		{"an unexported field", "\tmemo *string\n", "",
			"model.go:8:1: error: Order.memo: the code kanna-orm writes cannot reach an unexported field\n" +
				`  export it, or leave it out with orm:"-"`},
		{"an unexported struct embedded", "\taudit\n", "type audit struct{ Note *string }\n",
			"model.go:8:1: error: Order.audit: the code kanna-orm writes cannot reach an unexported field\n" +
				`  export it, or leave it out with orm:"-"`},
		{"a pointer embedded", "\t*Audit\n", "type Audit struct{ Note *string }\n",
			"model.go:8:1: error: Order.Audit: an embedded pointer may be nil, and so cannot be read into\n" +
				`  embed Audit itself, or leave it out with orm:"-"`},
		{"a tag on an embedded struct", "\tAudit `orm:\"audit\"`\n", "type Audit struct{ Note *string }\n",
			`model.go:8:1: error: Order.Audit: orm:"audit" is on an embedded struct, whose fields hold the columns` + "\n" +
				"  tag those fields instead"},
		{"a field misspelled", "\tCustmerID int64\n", "",
			"model.go:8:1: error: Order.CustmerID: orders has no column custmer_id\n  did you mean customer_id?"},
		{"a field named otherwise", "\tMemo *string\n", "",
			"model.go:8:1: error: Order.Memo: orders has no column memo\n" +
				`  name the column with orm:"<column>", or leave the field out with orm:"-"`},
		{"a column misspelled", "\tMemo *string `orm:\"notes\"`\n", "",
			"model.go:8:1: error: Order.Memo: orders has no column notes\n  did you mean note?"},
		{"a column held twice", "\tMemo *string `orm:\"note\"`\n\tNote *string\n", "",
			"model.go:9:1: error: Order.Note: orders.note is held by Order.Memo already\n" +
				`  leave one of them out with orm:"-"`},
		{"a column held again by a struct embedded", "\tBase\n", "type Base struct{ ID int64 }\n",
			"model.go:11:1: error: Order.Base.ID: orders.id is held by Order.ID already\n" +
				`  leave one of them out with orm:"-"`},
		{"a column that may be NULL in a string", "\tNote string\n", "",
			"model.go:8:1: error: Order.Note: orders.note may be NULL, which a string cannot hold\n  declare it as *string"},
		{"a column that may be NULL in an int64", "\tNote int64\n", "",
			"model.go:8:1: error: Order.Note: orders.note may be NULL, which an int64 cannot hold\n  declare it as *int64"},
		{"a column that may be NULL in a type of the model's package", "\tNote Note\n", "type Note string\n",
			"model.go:8:1: error: Order.Note: orders.note may be NULL, which a Note cannot hold\n  declare it as *Note"},
		{"a column that may be NULL in a type of another package", "\tNote time.Time\n", "",
			"model.go:8:1: error: Order.Note: orders.note may be NULL, which a time.Time cannot hold\n" +
				"  declare it as *time.Time"},
		{"a column that may be NULL in a value type", "\tNote Text\n",
			"type Text struct{ s string }\n\nfunc (t *Text) Scan(any) error { return nil }\n",
			"model.go:8:1: error: Order.Note: orders.note may be NULL, which a Text cannot hold\n  declare it as *Text"},
		{"a string for an integer", "\tCustomerID string\n", "",
			"model.go:8:1: error: Order.CustomerID: orders.customer_id is int8, which a string cannot hold\n" +
				"  declare it as int64, or as a type of your own with Scan and Value methods"},
		{"a time for text", "\tNote *time.Time\n", "",
			"model.go:8:1: error: Order.Note: orders.note is text, which a *time.Time cannot hold\n" +
				"  declare it as *string, or as a type of your own with Scan and Value methods"},
		{"a slice for text", "\tNote []string\n", "",
			"model.go:8:1: error: Order.Note: orders.note is text, which a []string cannot hold\n" +
				"  declare it as string, or as a type of your own with Scan and Value methods"},
		{"an integer for a uuid", "\tToken int64\n", "",
			"model.go:8:1: error: Order.Token: orders.token is uuid, which an int64 cannot hold\n" +
				"  declare it as string, or as a type of your own with Scan and Value methods"},
		{"a type of its own for an integer", "\tCustomerID Customer\n",
			"type Customer struct{ id int64 }\n\nfunc (c *Customer) Scan(any) error { return nil }\n", ""},
		{"a foreign key on a column", "\tCustomerID int64 `orm:\"fk=customer_id\"`\n", "",
			`model.go:8:1: error: Order.CustomerID: orm:"fk=customer_id" is for a relation, and CustomerID holds a column`},
		{"a column on a relation", "\tLines []OrderLine `orm:\"lines\"`\n", "type OrderLine struct{ ID int64 }\n",
			`model.go:8:1: error: Order.Lines: orm:"lines" names a column, but Lines is a relation` + "\n" +
				`  name the foreign key it follows with orm:"fk=<column>"`},
		{"a map", "\tMeta map[string]string\n", "",
			"model.go:8:1: error: Order.Meta: map[string]string is neither a column nor a relation\n" +
				`  hold the column in a type with a Scan method, or leave the field out with orm:"-"`},
		{"a struct of no name", "\tAddress struct{ City string }\n", "",
			"model.go:8:1: error: Order.Address: struct{City string} is neither a column nor a relation\n" +
				`  hold the column in a type with a Scan method, or leave the field out with orm:"-"`},
		{"an interface", "\tAny any\n", "",
			"model.go:8:1: error: Order.Any: any is neither a column nor a relation\n" +
				`  hold the column in a type with a Scan method, or leave the field out with orm:"-"`},
	}
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"orders": {Columns: []snapshot.Column{
			{Name: "id", Type: "int8", NotNull: true},
			{Name: "created_at", Type: "timestamptz", NotNull: true},
			{Name: "customer_id", Type: "int8", NotNull: true},
			{Name: "note", Type: "text"},
			{Name: "token", Type: "uuid", NotNull: true},
		}},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pkg := module(t, orderScope+`
-- model/model.go --
package model

import "time"

type Order struct {
	ID        int64
	CreatedAt time.Time
`+tt.fields+"}\n\n"+tt.decls)
			_, ds := gen.Fields(pkg, s)
			if got := report(ds); got != tt.want {
				t.Errorf("Fields reports\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestFields_MySQLHasNoArrays(t *testing.T) {
	t.Parallel()

	pkg := module(t, orderScope+`
-- model/model.go --
package model

type Order struct {
	ID   int64
	Tags []string
}
`)
	s := snapshot.Snapshot{Dialect: snapshot.MySQL, Tables: map[string]snapshot.Table{
		"orders": {Columns: []snapshot.Column{
			{Name: "id", Type: "bigint unsigned", NotNull: true},
			{Name: "tags", Type: "varchar(255)"},
		}},
	}}
	_, ds := gen.Fields(pkg, s)
	if got, want := report(ds), "model.go:5:1: error: Order.Tags: orders.tags is varchar(255), which a []string "+
		"cannot hold\n  declare it as string, or as a type of your own with Scan and Value methods"; got != want {
		t.Errorf("Fields reports\n%s\nwant\n%s", got, want)
	}
}

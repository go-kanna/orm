package gen_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/orm/internal/gen"
	"github.com/go-kanna/orm/internal/snapshot"
)

// columns makes columns that may not be NULL, of the names given.
// columns are columns of names, which may not be NULL, of a type no field is checked against.
func columns(names ...string) []snapshot.Column {
	cs := make([]snapshot.Column, 0, len(names))
	for _, n := range names {
		cs = append(cs, snapshot.Column{Name: n, NotNull: true})
	}
	return cs
}

func fk(columns []string, references string, referenced ...string) snapshot.ForeignKey {
	return snapshot.ForeignKey{Columns: columns, References: references, ReferencedColumns: referenced}
}

func TestLinks(t *testing.T) {
	t.Parallel()

	pkg := module(t, `
-- model/model.go --
package model

type Customer struct {
	ID      int64
	Profile *Profile
	Orders  []Order
}

type Profile struct {
	ID         int64
	CustomerID int64
}

type Address struct{ ID int64 }

type Order struct {
	ID                int64
	TenantID          int64
	CustomerID        int64
	BillingAddressID  int64
	ShippingAddressID int64
	Customer          *Customer
	BillingAddress    *Address `+"`orm:\"fk=billing_address_id\"`"+`
	Lines             []OrderLine `+"`orm:\"fk=order_lines.order_id\"`"+`
	Refunded          []OrderLine `+"`orm:\"fk=refunds.order_id\"`"+`
	Tags              []Tag
	Products          []*Product
	Invoice           *Invoice
}

type OrderLine struct {
	OrderID   int64
	ProductID int64
	Product   *Product
}

type Product struct{ ID int64 }

type Tag struct{ ID int64 }

type Invoice struct {
	OrderID  int64
	TenantID int64
}

type Category struct {
	ID       int64
	ParentID *int64
	Parent   *Category
	Children []Category
}
-- catalog/catalog.go --
package catalog

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm"
)

//kanna:scope table=products
type Product struct{ q orm.Of[model.Product] }
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm"
)

//kanna:scope table=customers
type Customer struct{ q orm.Of[model.Customer] }

//kanna:scope table=profiles
type Profile struct{ q orm.Of[model.Profile] }

//kanna:scope table=addresses
type Address struct{ q orm.Of[model.Address] }

//kanna:scope table=orders
type Order struct{ q orm.Of[model.Order] }

//kanna:scope table=order_lines
type OrderLine struct{ q orm.Of[model.OrderLine] }

//kanna:scope table=tags
type Tag struct{ q orm.Of[model.Tag] }

//kanna:scope table=invoices
type Invoice struct{ q orm.Of[model.Invoice] }

//kanna:scope table=categories
type Category struct{ q orm.Of[model.Category] }
`)
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"customers": {Columns: columns("id"), PrimaryKey: []string{"id"}},
		"profiles": {
			Columns: columns("id", "customer_id"), PrimaryKey: []string{"id"}, UniqueKeys: [][]string{{"customer_id"}},
			ForeignKeys: []snapshot.ForeignKey{fk([]string{"customer_id"}, "customers", "id")},
		},
		"addresses": {Columns: columns("id"), PrimaryKey: []string{"id"}},
		"orders": {
			Columns:    columns("id", "tenant_id", "customer_id", "billing_address_id", "shipping_address_id"),
			PrimaryKey: []string{"id"}, UniqueKeys: [][]string{{"id", "tenant_id"}},
			ForeignKeys: []snapshot.ForeignKey{
				fk([]string{"customer_id"}, "customers", "id"),
				fk([]string{"billing_address_id"}, "addresses", "id"),
				fk([]string{"shipping_address_id"}, "addresses", "id"),
			},
		},
		"order_lines": {
			Columns: columns("order_id", "product_id"), PrimaryKey: []string{"order_id", "product_id"},
			ForeignKeys: []snapshot.ForeignKey{
				fk([]string{"order_id"}, "orders", "id"),
				fk([]string{"product_id"}, "products", "id"),
			},
		},
		"refunds": {
			Columns: columns("id", "order_id", "product_id"), PrimaryKey: []string{"id"},
			ForeignKeys: []snapshot.ForeignKey{
				fk([]string{"order_id"}, "orders", "id"),
				fk([]string{"order_id", "product_id"}, "order_lines", "order_id", "product_id"),
			},
		},
		"products": {Columns: columns("id"), PrimaryKey: []string{"id"}},
		"tags":     {Columns: columns("id"), PrimaryKey: []string{"id"}},
		"order_tags": {
			Columns: columns("order_id", "tag_id"), PrimaryKey: []string{"order_id", "tag_id"},
			ForeignKeys: []snapshot.ForeignKey{
				fk([]string{"order_id"}, "orders", "id"),
				fk([]string{"tag_id"}, "tags", "id"),
			},
		},
		"invoices": {
			Columns: columns("order_id", "tenant_id"), PrimaryKey: []string{"tenant_id", "order_id"},
			ForeignKeys: []snapshot.ForeignKey{fk([]string{"order_id", "tenant_id"}, "orders", "id", "tenant_id")},
		},
		"categories": {
			Columns:     append(columns("id"), snapshot.Column{Name: "parent_id", Type: "int8"}),
			PrimaryKey:  []string{"id"},
			ForeignKeys: []snapshot.ForeignKey{fk([]string{"parent_id"}, "categories", "id")},
		},
	}}
	got, ds, err := gen.Links(pkg, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) > 0 {
		t.Fatalf("Links:\n%s", report(ds))
	}
	want := []string{
		"Customer.Profile has one scopes.Profile: profiles.customer_id to customers; ID = CustomerID",
		"Customer.Orders has many scopes.Order: orders.customer_id to customers; ID = CustomerID",
		"Order.Customer belongs to scopes.Customer: orders.customer_id to customers; CustomerID = ID",
		"Order.BillingAddress belongs to scopes.Address: orders.billing_address_id to addresses; BillingAddressID = ID",
		"Order.Lines has many scopes.OrderLine: order_lines.order_id to orders; ID = OrderID",
		"Order.Refunded many to many scopes.OrderLine: refunds.order_id to orders, and refunds (order_id, product_id) " +
			"to order_lines; ID = OrderID, ProductID",
		"Order.Tags many to many scopes.Tag: order_tags.order_id to orders, and order_tags.tag_id to tags; ID = ID",
		"Order.Products many to many catalog.Product: order_lines.order_id to orders, and order_lines.product_id to " +
			"products; ID = ID",
		"Order.Invoice has one scopes.Invoice: invoices (order_id, tenant_id) to orders; ID, TenantID = OrderID, TenantID",
		"OrderLine.Product belongs to catalog.Product: order_lines.product_id to products; ProductID = ID",
		"Category.Parent belongs to scopes.Category: categories.parent_id to categories; ParentID = ID",
		"Category.Children has many scopes.Category: categories.parent_id to categories; ID = ParentID",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Links =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestLinks_Mistakes(t *testing.T) {
	t.Parallel()

	// The package model is read from export data, which keeps the line of a field but not its column.
	tests := []struct {
		name, field, want string
	}{
		{"a model of no scope type, whose table a foreign key names", "Product *Product",
			"model.go:8:1: error: Order.Product: no scope type is declared for model.Product\n" +
				"  declare one where products are queried: //kanna:scope table=products"},
		{"a model of no scope type", "Notes []Note",
			"model.go:8:1: error: Order.Notes: no scope type is declared for model.Note\n" +
				"  declare one where its rows are queried: //kanna:scope table=<table>"},
		{"a scope type of a table the snapshot has not", "Gift *Gift",
			"model.go:8:1: error: Order.Gift: the snapshot has no table gifts, of which catalog.Gift is a scope type"},
		{"two foreign keys of the table", "ShippingAddress *Address",
			"model.go:8:1: error: Order.ShippingAddress: orders has two foreign keys to addresses " +
				"(billing_address_id, shipping_address_id)\n" +
				"  orders.billing_address_id to addresses\n" +
				"  orders.shipping_address_id to addresses\n" +
				`  tag the field with the one to follow: orm:"fk=shipping_address_id"`},
		{"a foreign key each way", "Receipt *Receipt",
			"model.go:8:1: error: Order.Receipt: two foreign keys could relate orders and receipts\n" +
				"  orders.receipt_id to receipts\n" +
				"  receipts.order_id to orders\n" +
				`  tag the field with the one to follow: orm:"fk=receipt_id"`},
		{"a tag naming no foreign key", "BillingAddress *Address `orm:\"fk=address_id\"`",
			`model.go:8:1: error: Order.BillingAddress: orm:"fk=address_id" names no foreign key that relates orders ` +
				"and addresses\n" +
				"  one that does: orders.billing_address_id to addresses\n" +
				"  one that does: orders.shipping_address_id to addresses"},
		{"a foreign key, and a table between", "Lines []OrderLine",
			"model.go:8:1: error: Order.Lines: two foreign keys could relate orders and order_lines\n" +
				"  order_lines.order_id to orders\n" +
				"  refunds.order_id to orders, and refunds (order_id, product_id) to order_lines\n" +
				`  tag the field with the one to follow: orm:"fk=order_lines.order_id"`},
		{"a tag whose column alone does not tell", "Lines []OrderLine `orm:\"fk=order_id\"`",
			"model.go:8:1: error: Order.Lines: two foreign keys could relate orders and order_lines\n" +
				"  order_lines.order_id to orders\n" +
				"  refunds.order_id to orders, and refunds (order_id, product_id) to order_lines\n" +
				`  tag the field with the one to follow: orm:"fk=order_lines.order_id"`},
		{"a tag naming a column of another foreign key of the table", "Lines []OrderLine `orm:\"fk=refunds.product_id\"`",
			`model.go:8:1: error: Order.Lines: orm:"fk=refunds.product_id" names no foreign key that relates orders ` +
				"and order_lines\n" +
				"  one that does: order_lines.order_id to orders\n" +
				"  one that does: refunds.order_id to orders, and refunds (order_id, product_id) to order_lines"},
		{"no foreign key", "Tags []Tag",
			"model.go:8:1: error: Order.Tags: no foreign key relates orders and tags\n" +
				"  a slice is loaded by a foreign key of tags to orders, or through a table with foreign keys to both"},
		{"a pointer to what there are many of", "Line *OrderLine",
			"model.go:8:1: error: Order.Line: no foreign key relates orders and order_lines\n" +
				"  order_lines.order_id to orders relates many rows of order_lines to each: declare the field as a slice"},
		{"a slice of what there is one of", "Customers []Customer",
			"model.go:8:1: error: Order.Customers: no foreign key relates orders and customers\n" +
				"  orders.customer_id to customers relates one row of customers to each: declare the field as a pointer"},
		{"a pointer of no foreign key", "Tag *Tag",
			"model.go:8:1: error: Order.Tag: no foreign key relates orders and tags\n" +
				"  a pointer is loaded by a foreign key of orders to tags, or by a unique one of tags to orders"},
		{"a key the model holds no field for", "Coupon *Coupon",
			"model.go:8:1: error: Order.Coupon: no field of Order holds orders.coupon_id, which Coupon is loaded by"},
		{"a key the related model holds no field for", "Items []Item",
			"model.go:8:1: error: Order.Items: no field of model.Item holds items.order_id, which Items is loaded by"},
	}
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"orders": {
			Columns: columns("id", "customer_id", "billing_address_id", "shipping_address_id", "product_id", "coupon_id",
				"receipt_id"),
			PrimaryKey: []string{"id"},
			ForeignKeys: []snapshot.ForeignKey{
				fk([]string{"customer_id"}, "customers", "id"),
				fk([]string{"billing_address_id"}, "addresses", "id"),
				fk([]string{"shipping_address_id"}, "addresses", "id"),
				fk([]string{"product_id"}, "products", "id"),
				fk([]string{"coupon_id"}, "coupons", "id"),
				fk([]string{"receipt_id"}, "receipts", "id"),
			},
		},
		"customers": {Columns: columns("id"), PrimaryKey: []string{"id"}},
		"addresses": {Columns: columns("id"), PrimaryKey: []string{"id"}},
		"order_lines": {
			Columns: columns("order_id", "product_id"), PrimaryKey: []string{"order_id", "product_id"},
			ForeignKeys: []snapshot.ForeignKey{
				fk([]string{"order_id"}, "orders", "id"),
				fk([]string{"product_id"}, "products", "id"),
			},
		},
		"refunds": {
			Columns: columns("id", "order_id", "product_id"), PrimaryKey: []string{"id"},
			ForeignKeys: []snapshot.ForeignKey{
				fk([]string{"order_id"}, "orders", "id"),
				fk([]string{"order_id", "product_id"}, "order_lines", "order_id", "product_id"),
			},
		},
		"products": {Columns: columns("id"), PrimaryKey: []string{"id"}},
		"tags":     {Columns: columns("id"), PrimaryKey: []string{"id"}},
		"items": {
			Columns:     columns("order_id", "product_id"),
			ForeignKeys: []snapshot.ForeignKey{fk([]string{"order_id"}, "orders", "id")},
		},
		"coupons": {Columns: columns("id"), PrimaryKey: []string{"id"}},
		"receipts": {
			Columns: columns("id", "order_id"), PrimaryKey: []string{"id"}, UniqueKeys: [][]string{{"order_id"}},
			ForeignKeys: []snapshot.ForeignKey{fk([]string{"order_id"}, "orders", "id")},
		},
		"notes": {Columns: columns("id"), PrimaryKey: []string{"id"}},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pkg := module(t, `
-- model/model.go --
package model

type Order struct {
	ID                int64
	CustomerID        int64
	BillingAddressID  int64
	ShippingAddressID int64
	`+tt.field+`
}

type Customer struct{ ID int64 }

type Address struct{ ID int64 }

type OrderLine struct {
	OrderID   int64
	ProductID int64
}

type Product struct{ ID int64 }

type Tag struct{ ID int64 }

type Item struct{ ProductID int64 }

type Coupon struct{ ID int64 }

type Receipt struct {
	ID      int64
	OrderID int64
}

type Note struct{ ID int64 }

type Gift struct{ ID int64 }
-- catalog/catalog.go --
package catalog

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm"
)

//kanna:scope table=gifts
type Gift struct{ q orm.Of[model.Gift] }
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm"
)

//kanna:scope table=orders
type Order struct{ q orm.Of[model.Order] }

//kanna:scope table=customers
type Customer struct{ q orm.Of[model.Customer] }

//kanna:scope table=addresses
type Address struct{ q orm.Of[model.Address] }

//kanna:scope table=order_lines
type OrderLine struct{ q orm.Of[model.OrderLine] }

//kanna:scope table=tags
type Tag struct{ q orm.Of[model.Tag] }

//kanna:scope table=items
type Item struct{ q orm.Of[model.Item] }

//kanna:scope table=coupons
type Coupon struct{ q orm.Of[model.Coupon] }

//kanna:scope table=receipts
type Receipt struct{ q orm.Of[model.Receipt] }
`)
			_, ds, err := gen.Links(pkg, s)
			if err != nil {
				t.Fatal(err)
			}
			if got := report(ds); got != tt.want {
				t.Errorf("Links reports\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

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
	BillingAddress    *Address
	Shipping          *Address `+"`orm:\"fk=shipping_address_id\"`"+`
	Lines             []OrderLine
	Tags              []Tag
	Products          []*Product
	Invoice           *Invoice `+"`orm:\"fk=tenant_id,order_id\"`"+`
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
	Children []Category `+"`orm:\"fk=parent_id\"`"+`
}
-- catalog/catalog.go --
package catalog

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=products
type Product struct{ q scope.Of[model.Product] }
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=customers
type Customer struct{ q scope.Of[model.Customer] }

//kanna:scope table=profiles
type Profile struct{ q scope.Of[model.Profile] }

//kanna:scope table=addresses
type Address struct{ q scope.Of[model.Address] }

//kanna:scope table=orders
type Order struct{ q scope.Of[model.Order] }

//kanna:scope table=order_lines
type OrderLine struct{ q scope.Of[model.OrderLine] }

//kanna:scope table=tags
type Tag struct{ q scope.Of[model.Tag] }

//kanna:scope table=invoices
type Invoice struct{ q scope.Of[model.Invoice] }

//kanna:scope table=categories
type Category struct{ q scope.Of[model.Category] }
`)
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		// customers.order_id is no unique key, so Order.Customer reads by orders.customer_id alone.
		"customers": {
			Columns:     append(columns("id"), snapshot.Column{Name: "order_id", Type: "int8"}),
			PrimaryKey:  []string{"id"},
			ForeignKeys: []snapshot.ForeignKey{fk([]string{"order_id"}, "orders", "id")},
		},
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
		"products": {Columns: columns("id"), PrimaryKey: []string{"id"}},
		"tags":     {Columns: columns("id"), PrimaryKey: []string{"id"}},
		"order_tags": {
			Columns: columns("order_id", "tag_id"), PrimaryKey: []string{"order_id", "tag_id"},
			ForeignKeys: []snapshot.ForeignKey{
				fk([]string{"order_id"}, "orders", "id"),
				fk([]string{"tag_id"}, "tags", "id"),
			},
		},
		"order_products": {
			Columns: columns("order_id", "product_id"), PrimaryKey: []string{"order_id", "product_id"},
			ForeignKeys: []snapshot.ForeignKey{
				fk([]string{"order_id"}, "orders", "id"),
				fk([]string{"product_id"}, "products", "id"),
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
		"Order.Shipping belongs to scopes.Address: orders.shipping_address_id to addresses; ShippingAddressID = ID",
		"Order.Lines has many scopes.OrderLine: order_lines.order_id to orders; ID = OrderID",
		"Order.Tags many to many scopes.Tag: order_tags.order_id to orders, and order_tags.tag_id to tags; ID = ID",
		"Order.Products many to many catalog.Product: order_products.order_id to orders, and order_products.product_id " +
			"to products; ID = ID",
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
			"model.go:9:2: error: Order.Product: no scope type is declared for model.Product\n" +
				"  declare one where products are queried: //kanna:scope table=products"},
		{"a model of no scope type", "Notes []Note",
			"model.go:9:2: error: Order.Notes: no scope type is declared for model.Note\n" +
				"  declare one where its rows are queried: //kanna:scope table=<table>"},
		{"a model of no scope type, named after a table the snapshot has not", "Refunds []Refund",
			"model.go:9:2: error: Order.Refunds: no scope type is declared for model.Refund, and the snapshot has no " +
				"table refunds, which it is named after\n" +
				"  if it is new, describe the database again: go tool kanna-orm describe\n" +
				"  or declare one where its rows are queried: //kanna:scope table=<table>"},
		{"a scope type of a table the snapshot has not", "Gift *Gift",
			"model.go:9:2: error: Order.Gift: the snapshot has no table gifts, of which catalog.Gift is a scope type"},
		{"no foreign key of the name, and two others", "Address *Address",
			"model.go:9:2: error: Order.Address: no foreign key of orders.address_id to addresses reads it, nor a " +
				"unique one of addresses.order_id to orders\n" +
				`  orders.billing_address_id to addresses reads it: tag the field orm:"fk=billing_address_id"` + "\n" +
				`  orders.shipping_address_id to addresses reads it: tag the field orm:"fk=shipping_address_id"`},
		{"a tag naming no foreign key", "BillingAddress *Address `orm:\"fk=address_id\"`",
			"model.go:9:2: error: Order.BillingAddress: no foreign key of orders.address_id to addresses reads it, " +
				"nor a unique one of addresses.address_id to orders\n" +
				`  orders.billing_address_id to addresses reads it: tag the field orm:"fk=billing_address_id"` + "\n" +
				`  orders.shipping_address_id to addresses reads it: tag the field orm:"fk=shipping_address_id"`},
		{"a belongs to and a has one", "Receipt *Receipt",
			"model.go:9:2: error: Order.Receipt: its names read it both as a belongs to, by orders.receipt_id to " +
				"receipts, and as a has one, by receipts.order_id to orders\n" +
				"  a relation its names cannot tell is read by SQL written by hand: a join returning a struct that embeds the " +
				"model, or a query of the related rows by a slice of keys, put together in Go"},
		{"two foreign keys of the column", "Buyer *Customer",
			"model.go:9:2: error: Order.Buyer: orders has two foreign keys of buyer_id to customers\n" +
				"  a relation its names cannot tell is read by SQL written by hand: a join returning a struct that embeds the " +
				"model, or a query of the related rows by a slice of keys, put together in Go"},
		{"a has many and a many to many", "Parts []Part",
			"model.go:9:2: error: Order.Parts: its names read it both as a has many, by parts.order_id to orders, " +
				"and as a many to many, through order_parts\n" +
				`  for the has many, tag the field orm:"fk=order_id"` + "\n" +
				"  for the many to many, read order_parts as a model of its own, by a has many and a belongs to"},
		{"a table between of other names", "Coupons []Coupon",
			"model.go:9:2: error: Order.Coupons: no foreign key of coupons.order_id to orders reads it, nor a table " +
				"between, order_coupons, with foreign keys of order_id to orders and of coupon_id to coupons\n" +
				"  orders.coupon_id to coupons relates one row of coupons to each: declare the field as a pointer, tagged " +
				`orm:"fk=coupon_id"` + "\n" +
				"  a table between, as coupon_uses, whose names are not order_coupons, order_id, and coupon_id, is read as " +
				"a model of its own, by a has many and a belongs to, or by SQL written by hand"},
		{"a model related to itself through a table", "Bundles []Order",
			"model.go:9:2: error: Order.Bundles: no foreign key of orders.order_id to orders reads it\n" +
				"  a table between rows of orders, whose two columns would both be order_id, is read as a model of its " +
				"own, by a has many and a belongs to, or by SQL written by hand"},
		{"a pointer to what there are many of", "Line *OrderLine",
			"model.go:9:2: error: Order.Line: no foreign key of orders.line_id to order_lines reads it, nor a unique " +
				"one of order_lines.order_id to orders\n" +
				"  order_lines.order_id to orders relates many rows of order_lines to each: declare the field as a slice"},
		{"a slice of what there is one of", "Customers []Customer",
			"model.go:9:2: error: Order.Customers: no foreign key of customers.order_id to orders reads it, nor a " +
				"table between, order_customers, with foreign keys of order_id to orders and of customer_id to customers\n" +
				"  orders.customer_id to customers relates one row of customers to each: declare the field as a pointer, " +
				`tagged orm:"fk=customer_id"`},
		{"a foreign key of more columns than the name", "Invoice *Invoice",
			"model.go:9:2: error: Order.Invoice: no foreign key of orders.invoice_id to invoices reads it, nor a " +
				"unique one of invoices.order_id to orders\n" +
				`  invoices (order_id, tenant_id) to orders reads it: tag the field orm:"fk=order_id,tenant_id"`},
		{"a pointer of no foreign key", "Tag *Tag",
			"model.go:9:2: error: Order.Tag: no foreign key of orders.tag_id to tags reads it, nor a unique one of " +
				"tags.order_id to orders"},
		{"a key the model holds no field for", "Coupon *Coupon",
			"model.go:9:2: error: Order.Coupon: no field of Order holds orders.coupon_id, which Coupon is loaded by"},
		{"a key the related model holds no field for", "Items []Item",
			"model.go:9:2: error: Order.Items: no field of model.Item holds items.order_id, which Items is loaded by"},
	}
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Tables: map[string]snapshot.Table{
		"orders": {
			Columns: columns("id", "tenant_id", "customer_id", "billing_address_id", "shipping_address_id", "product_id",
				"coupon_id", "receipt_id", "buyer_id"),
			PrimaryKey: []string{"id"}, UniqueKeys: [][]string{{"id", "tenant_id"}},
			ForeignKeys: []snapshot.ForeignKey{
				fk([]string{"customer_id"}, "customers", "id"),
				fk([]string{"billing_address_id"}, "addresses", "id"),
				fk([]string{"shipping_address_id"}, "addresses", "id"),
				fk([]string{"product_id"}, "products", "id"),
				fk([]string{"coupon_id"}, "coupons", "id"),
				fk([]string{"receipt_id"}, "receipts", "id"),
				fk([]string{"buyer_id"}, "customers", "id"),
				fk([]string{"buyer_id"}, "customers", "number"),
			},
		},
		"customers": {Columns: columns("id", "number"), PrimaryKey: []string{"id"}, UniqueKeys: [][]string{{"number"}}},
		"addresses": {Columns: columns("id"), PrimaryKey: []string{"id"}},
		"order_lines": {
			Columns: columns("order_id", "product_id"), PrimaryKey: []string{"order_id", "product_id"},
			ForeignKeys: []snapshot.ForeignKey{
				fk([]string{"order_id"}, "orders", "id"),
				fk([]string{"product_id"}, "products", "id"),
			},
		},
		"products": {Columns: columns("id"), PrimaryKey: []string{"id"}},
		"tags":     {Columns: columns("id"), PrimaryKey: []string{"id"}},
		"items": {
			Columns:     columns("order_id", "product_id"),
			ForeignKeys: []snapshot.ForeignKey{fk([]string{"order_id"}, "orders", "id")},
		},
		"parts": {
			Columns: columns("id", "order_id"), PrimaryKey: []string{"id"},
			ForeignKeys: []snapshot.ForeignKey{fk([]string{"order_id"}, "orders", "id")},
		},
		"order_parts": {
			Columns: columns("order_id", "part_id"), PrimaryKey: []string{"order_id", "part_id"},
			ForeignKeys: []snapshot.ForeignKey{
				fk([]string{"order_id"}, "orders", "id"),
				fk([]string{"part_id"}, "parts", "id"),
			},
		},
		"coupons": {Columns: columns("id"), PrimaryKey: []string{"id"}},
		"coupon_uses": {
			Columns: columns("order_id", "coupon_id"), PrimaryKey: []string{"order_id", "coupon_id"},
			ForeignKeys: []snapshot.ForeignKey{
				fk([]string{"order_id"}, "orders", "id"),
				fk([]string{"coupon_id"}, "coupons", "id"),
			},
		},
		"order_bundles": {
			Columns: columns("order_id", "bundled_id"), PrimaryKey: []string{"order_id", "bundled_id"},
			ForeignKeys: []snapshot.ForeignKey{
				fk([]string{"order_id"}, "orders", "id"),
				fk([]string{"bundled_id"}, "orders", "id"),
			},
		},
		"receipts": {
			Columns: columns("id", "order_id"), PrimaryKey: []string{"id"}, UniqueKeys: [][]string{{"order_id"}},
			ForeignKeys: []snapshot.ForeignKey{fk([]string{"order_id"}, "orders", "id")},
		},
		"invoices": {
			Columns: columns("order_id", "tenant_id"), PrimaryKey: []string{"tenant_id", "order_id"},
			ForeignKeys: []snapshot.ForeignKey{fk([]string{"order_id", "tenant_id"}, "orders", "id", "tenant_id")},
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
	TenantID          int64
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

type Part struct {
	ID      int64
	OrderID int64
}

type Coupon struct{ ID int64 }

type Receipt struct {
	ID      int64
	OrderID int64
}

type Invoice struct {
	OrderID  int64
	TenantID int64
}

type Note struct{ ID int64 }

type Refund struct{ ID int64 }

type Gift struct{ ID int64 }
-- catalog/catalog.go --
package catalog

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=gifts
type Gift struct{ q scope.Of[model.Gift] }
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=orders
type Order struct{ q scope.Of[model.Order] }

//kanna:scope table=customers
type Customer struct{ q scope.Of[model.Customer] }

//kanna:scope table=addresses
type Address struct{ q scope.Of[model.Address] }

//kanna:scope table=order_lines
type OrderLine struct{ q scope.Of[model.OrderLine] }

//kanna:scope table=tags
type Tag struct{ q scope.Of[model.Tag] }

//kanna:scope table=items
type Item struct{ q scope.Of[model.Item] }

//kanna:scope table=parts
type Part struct{ q scope.Of[model.Part] }

//kanna:scope table=coupons
type Coupon struct{ q scope.Of[model.Coupon] }

//kanna:scope table=receipts
type Receipt struct{ q scope.Of[model.Receipt] }

//kanna:scope table=invoices
type Invoice struct{ q scope.Of[model.Invoice] }
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

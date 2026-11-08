package gen_test

import (
	"strings"
	"testing"

	"github.com/go-kanna/orm/internal/gen"
	"github.com/go-kanna/orm/internal/snapshot"
	"github.com/go-kanna/orm/internal/sqlfile"
)

func TestEmit_SQLScopeMistakes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// sql is the .sql file of the scope types, and more another.
		name, sql, more string
		// undescribed leaves the scopes out of the snapshot, and stale records them as other text.
		undescribed, stale bool
		want               string
	}{
		{name: "a scope of no scope type", sql: "-- scope: Ordr.Open()\nstatus <> 'closed'\n",
			want: "order_scopes.sql:1:1: error: -- scope: Ordr.Open: Ordr is no //kanna:scope type of this package\n" +
				"  did you mean Order?"},
		{name: "a scope the snapshot has not described", sql: "-- scope: Order.Open()\nstatus <> 'closed'\n",
			undescribed: true,
			want: "order_scopes.sql:1:1: error: -- scope: Order.Open: the snapshot has not described it\n" +
				"  run go tool kanna-orm describe to describe it"},
		{name: "a scope changed since", sql: "-- scope: Order.Open()\nstatus <> 'closed'\n", stale: true,
			want: "order_scopes.sql:1:1: error: -- scope: Order.Open: it has changed since the snapshot described it\n" +
				"  run go tool kanna-orm describe to describe it again"},
		{name: "parameters that do not read as Go's", sql: "-- scope: Order.Since(at time.)\nplaced_at >= :at\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.Since: (at time.) does not read as the parameters of a " +
				"Go function\n  as in -- scope: Order.PlacedInMonth(at time.Time)"},
		{name: "a parameter without a name", sql: "-- scope: Order.Since(time.Time)\nplaced_at >= now()\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.Since: its parameter 1 has no name, which the " +
				"statement takes it by"},
		{name: "a name of two parameters", sql: "-- scope: Order.By(v int64, v int64)\nid = :v\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.By: two of its parameters are named v"},
		{name: "a name of two parameters of one type", sql: "-- scope: Order.By(v, v int64)\nid = :v\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.By: two of its parameters are named v"},
		{name: "a type that is none", sql: "-- scope: Order.Since(at Time)\nplaced_at >= :at\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.Since: Time names no type"},
		{name: "a package nothing imports", sql: "-- scope: Order.Above(n big.Int)\nid > :n\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.Above: big names no package that package scopes " +
				"imports, nor model.go, which declares model.Order\n" +
				"  import it in a Go file of the package, as in import _ \"<the import path of big>\""},
		{name: "a package imported by one the scopes import", sql: "-- scope: Order.From(addr netip.Addr)\nip = :addr\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.From: netip names no package that package scopes " +
				"imports, nor model.go, which declares model.Order\n" +
				"  import it in a Go file of the package, as in import _ \"net/netip\""},
		{name: "a package the model's file imports for its code alone",
			sql: "-- scope: Order.Named(b text.Builder)\nmemo = :b\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.Named: text names example.com/shop/text, which " +
				"model.go imports, but kanna-orm has not loaded it\n" +
				"  import it in a Go file of the package, as in import _ \"example.com/shop/text\""},
		{name: "a type a package has not", sql: "-- scope: Order.Since(at time.Moment)\nplaced_at >= :at\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.Since: time.Moment names no type of time"},
		{name: "a parameter not declared", sql: "-- scope: Order.Since()\nplaced_at >= :at\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.Since: :at is none of its parameters\n" +
				"  declare it on the -- scope: line"},
		{name: "a parameter not used", sql: "-- scope: Order.Since(at time.Time)\nplaced_at >= now()\n",
			want: "order_scopes.sql:1:1: warning: -- scope: Order.Since: the statement does not use at"},
		{name: "a placeholder of the database's own", sql: "-- scope: Order.Since()\n\nplaced_at >= $1\n",
			want: "order_scopes.sql:3:14: error: $1 is a placeholder of PostgreSQL's own: write the parameter as :name"},
		{name: "a placeholder of the database's own in a statement",
			sql:  "-- scope: Order.Since()\nSELECT 1 FROM orders WHERE placed_at >= $1;\n",
			want: "order_scopes.sql:2:41: error: $1 is a placeholder of PostgreSQL's own: write the parameter as :name"},
		{name: "a statement of another table", sql: "-- scope: Order.Open()\nSELECT 1 FROM order_lines WHERE true\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.Open: the statement selects from order_lines, not from " +
				"its table orders\n  write it as SELECT 1 FROM orders WHERE <condition>, or as the condition alone"},
		{name: "a statement naming its table by an alias",
			sql: "-- scope: Order.Open()\nSELECT 1 FROM orders o WHERE o.status <> 'closed'\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.Open: the statement gives orders an alias, which means " +
				"nothing where kanna-orm puts its condition\n" +
				"  write it as SELECT 1 FROM orders WHERE <condition>, or as the condition alone"},
		{name: "a statement with more than a condition",
			sql: "-- scope: Order.Recent()\nSELECT 1 FROM orders WHERE true ORDER BY placed_at DESC LIMIT 10\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.Recent: the statement holds more than a condition " +
				"after WHERE\n  write it as SELECT 1 FROM orders WHERE <condition>, or as the condition alone"},
		{name: "a scope written twice", sql: "-- scope: Order.Open()\nstatus <> 'closed'\n\n" +
			"-- scope: Order.Open()\nstatus = 'open'\n",
			want: "order_scopes.sql:4:1: error: -- scope: Order.Open is written a second time"},
		{name: "a scope written in two files", sql: "-- scope: Order.Open()\nstatus <> 'closed'\n",
			more: "-- scope: Order.Open()\nstatus = 'open'\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.Open: it is written a second time\n" +
				"  it is also at more_scopes.sql:1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pkg := module(t, orderScope+`
-- scopes/imports.go --
package scopes

import _ "time"
-- model/model.go --
package model

import (
	"time"

	"example.com/shop/text"
)

type Order struct {
	ID       int64
	PlacedAt time.Time
}

// trim uses text in its body alone, which the export data of the package need not tell of.
func trim(s string) string {
	var b text.Builder
	for range 3 {
		b.Add(s)
	}
	return b.String()
}
-- text/text.go --
package text

type Builder struct{ s string }

func (b *Builder) Add(s string) { b.s += s }

func (b *Builder) String() string { return b.s }
-- model/origin.go --
package model

import "net/netip"

// Origin has the model package import netip as its API shows.
var Origin netip.Addr
-- scopes/order_scopes.sql --
`+tt.sql+`
-- scopes/more_scopes.sql --
`+tt.more)
			// The files are read in the order of their names, so more_scopes.sql has the scope checked against the snapshot.
			stmts, _ := sqlfile.Parse("more_scopes.sql", []byte(tt.more))
			first, _ := sqlfile.Parse("order_scopes.sql", []byte(tt.sql))
			described := map[string]snapshot.Statement{}
			for _, st := range append(stmts, first...) {
				key, text := "example.com/shop/scopes."+st.Type+"."+st.Name, st.Text
				if tt.stale {
					text += " "
				}
				if _, ok := described[key]; !ok && !tt.undescribed {
					described[key] = snapshot.Statement{Digest: snapshot.Digest(text)}
				}
			}
			s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL, Statements: described, Tables: map[string]snapshot.Table{
				"orders": {Columns: columns("id", "placed_at"), PrimaryKey: []string{"id"}},
			}}
			_, ds, err := gen.Emit(pkg, s)
			if err != nil {
				t.Fatal(err)
			}
			if got := report(ds); got != tt.want {
				t.Errorf("Emit reports\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestEmit_SQLScopeTypes(t *testing.T) {
	t.Parallel()

	const sql = "-- scope: Order.Odd(a *int64, b []string, c map[string]int, d model.Status, e other.Kind, " +
		"f time.Duration)\na = :a AND b = :b AND c = :c AND d = :d AND e = :e AND f = :f\n"
	pkg := module(t, `
-- model/model.go --
package model

import "time"

type Order struct {
	ID int64
	At time.Time
}

type Status string
-- other/model/model.go --
package model

type Kind int
-- scopes/scopes.go --
package scopes

import (
	_ "time"

	"example.com/shop/model"
	other "example.com/shop/other/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=orders
type Order struct{ q scope.Of[model.Order] }

var _ other.Kind
-- scopes/order_scopes.sql --
`+sql)
	stmts, _ := sqlfile.Parse("order_scopes.sql", []byte(sql))
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL,
		Statements: map[string]snapshot.Statement{
			"example.com/shop/scopes.Order.Odd": {Digest: snapshot.Digest(stmts[0].Text)},
		},
		Tables: map[string]snapshot.Table{"orders": {Columns: columns("id", "at"), PrimaryKey: []string{"id"}}},
	}
	src, ds, err := gen.Emit(pkg, s)
	if err != nil || len(ds) > 0 {
		t.Fatalf("Emit: %v\n%s", err, report(ds))
	}
	const want = "func (s Order) Odd(a *int64, b []string, c map[string]int, d model.Status, e model2.Kind, " +
		"f time.Duration) Order {"
	if !strings.Contains(string(src), want) {
		t.Errorf("Emit wrote no %q:\n%s", want, src)
	}
}

func TestEmit_SQLScopeStatement(t *testing.T) {
	t.Parallel()

	emit := func(t *testing.T, body string) string {
		t.Helper()

		sql := "-- Since narrows to the orders placed at since or later.\n-- scope: Order.Since(since time.Time)\n" + body
		pkg := module(t, orderScope+`
-- scopes/imports.go --
package scopes

import _ "time"
-- model/model.go --
package model

import "time"

type Order struct {
	ID       int64
	PlacedAt time.Time
}
-- scopes/order_scopes.sql --
`+sql)
		stmts, _ := sqlfile.Parse("order_scopes.sql", []byte(sql))
		s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL,
			Statements: map[string]snapshot.Statement{
				"example.com/shop/scopes.Order.Since": {Digest: snapshot.Digest(stmts[0].Text)},
			},
			Tables: map[string]snapshot.Table{"orders": {Columns: columns("id", "placed_at"), PrimaryKey: []string{"id"}}},
		}
		src, ds, err := gen.Emit(pkg, s)
		if err != nil || len(ds) > 0 {
			t.Fatalf("Emit: %v\n%s", err, report(ds))
		}
		return string(src)
	}

	bare := emit(t, "placed_at >= :since\n")
	if !strings.Contains(bare, "func (s Order) Since(since time.Time) Order {") {
		t.Fatalf("Emit wrote no Since:\n%s", bare)
	}
	if got := emit(t, "select 1\nfrom orders\nwhere placed_at >= :since;\n"); got != bare {
		t.Errorf("Emit of the statement wrote\n%s\nwant what it writes of the condition\n%s", got, bare)
	}
}

func TestEmit_SQLScopeTypesOfTheModelsFile(t *testing.T) {
	t.Parallel()

	// The scope package imports kind as b/kind does, and the model's file as a/kind does: the package's own import
	// wins, as in Go. time and the id of u are imported by the model's file alone.
	const sql = "-- scope: Order.Odd(at time.Time, id u.ID, k kind.Kind)\nat = :at AND id = :id AND k = :k\n"
	pkg := module(t, `
-- model/model.go --
package model

import (
	"time"

	"example.com/shop/a/kind"
	u "example.com/shop/uid"
)

type Order struct {
	ID   u.ID
	At   time.Time
	Kind kind.Kind
}
-- uid/uid.go --
package uid

type ID string
-- a/kind/kind.go --
package kind

type Kind int
-- b/kind/kind.go --
package kind

type Kind string
-- scopes/scopes.go --
package scopes

import (
	"example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=orders
type Order struct{ q scope.Of[model.Order] }
-- scopes/kind.go --
package scopes

import _ "example.com/shop/b/kind"
-- scopes/order_scopes.sql --
`+sql)
	stmts, _ := sqlfile.Parse("order_scopes.sql", []byte(sql))
	s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL,
		Statements: map[string]snapshot.Statement{
			"example.com/shop/scopes.Order.Odd": {Digest: snapshot.Digest(stmts[0].Text)},
		},
		Tables: map[string]snapshot.Table{"orders": {Columns: columns("id", "at", "kind"), PrimaryKey: []string{"id"}}},
	}
	src, ds, err := gen.Emit(pkg, s)
	if err != nil || len(ds) > 0 {
		t.Fatalf("Emit: %v\n%s", err, report(ds))
	}
	// The methods of the column Kind take the a/kind of the model.
	for _, want := range []string{
		"func (s Order) Odd(at time.Time, id uid.ID, k kind2.Kind) Order {",
		`kind2 "example.com/shop/b/kind"`,
		`"example.com/shop/uid"`,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("Emit wrote no %q:\n%s", want, src)
		}
	}
}

func TestEmit_SQLScopeTypesOfTheModelsPackage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// more is a Go file of the scope package more, and want the parameter the scope's method is written with.
		name, more, want string
	}{
		{"by the name of the model's package", "", "st model.Status"},
		{"by an import of the scope package of the same name", "import _ \"example.com/shop/other/model\"\n",
			"st model2.Status"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// The scope package imports the model's package by another name than its own.
			const sql = "-- scope: Order.In(st model.Status)\nstatus = :st\n"
			pkg := module(t, `
-- model/model.go --
package model

type Order struct {
	ID     int64
	Status Status
}

type Status string
-- other/model/model.go --
package model

type Status int
-- scopes/scopes.go --
package scopes

import (
	m "example.com/shop/model"
	"github.com/go-kanna/orm/scope"
)

//kanna:scope table=orders
type Order struct{ q scope.Of[m.Order] }
-- scopes/more.go --
package scopes

`+tt.more+`
-- scopes/order_scopes.sql --
`+sql)
			stmts, _ := sqlfile.Parse("order_scopes.sql", []byte(sql))
			s := snapshot.Snapshot{Dialect: snapshot.PostgreSQL,
				Statements: map[string]snapshot.Statement{
					"example.com/shop/scopes.Order.In": {Digest: snapshot.Digest(stmts[0].Text)},
				},
				Tables: map[string]snapshot.Table{"orders": {Columns: columns("id", "status"), PrimaryKey: []string{"id"}}},
			}
			src, ds, err := gen.Emit(pkg, s)
			if err != nil || len(ds) > 0 {
				t.Fatalf("Emit: %v\n%s", err, report(ds))
			}
			if want := "func (s Order) In(" + tt.want + ") Order {"; !strings.Contains(string(src), want) {
				t.Errorf("Emit wrote no %q:\n%s", want, src)
			}
		})
	}
}

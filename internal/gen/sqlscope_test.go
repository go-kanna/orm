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
		{name: "a type that is none", sql: "-- scope: Order.Since(at Time)\nplaced_at >= :at\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.Since: Time names no type"},
		{name: "a package nothing imports", sql: "-- scope: Order.From(addr netip.Addr)\nip = :addr\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.From: netip names no package that package scopes " +
				"imports"},
		{name: "a type a package has not", sql: "-- scope: Order.Since(at time.Moment)\nplaced_at >= :at\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.Since: time.Moment names no type of time"},
		{name: "a parameter not declared", sql: "-- scope: Order.Since()\nplaced_at >= :at\n",
			want: "order_scopes.sql:1:1: error: -- scope: Order.Since: :at is none of its parameters\n" +
				"  declare it on the -- scope: line"},
		{name: "a parameter not used", sql: "-- scope: Order.Since(at time.Time)\nplaced_at >= now()\n",
			want: "order_scopes.sql:1:1: warning: -- scope: Order.Since: the statement does not use at"},
		{name: "a placeholder of the database's own", sql: "-- scope: Order.Since()\n\nplaced_at >= $1\n",
			want: "order_scopes.sql:3:14: error: $1 is a placeholder of PostgreSQL's own: write the parameter as :name"},
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
-- model/model.go --
package model

import "time"

type Order struct {
	ID       int64
	PlacedAt time.Time
}
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
	"example.com/shop/model"
	other "example.com/shop/other/model"
	"github.com/go-kanna/orm"
)

//kanna:scope table=orders
type Order struct{ q orm.Of[model.Order] }

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

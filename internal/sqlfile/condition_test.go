package sqlfile_test

import (
	"cmp"
	"go/token"
	"testing"

	"github.com/go-kanna/orm/internal/snapshot"
	"github.com/go-kanna/orm/internal/sqlfile"
)

// scope is the one scope of data, a .sql file.
func scope(t *testing.T, data string) sqlfile.Statement {
	t.Helper()

	stmts, ds := sqlfile.Parse(path, []byte(data))
	if len(ds) > 0 || len(stmts) != 1 {
		t.Fatalf("Parse = %+v, %v", stmts, ds)
	}
	return stmts[0]
}

func TestStatement_Condition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, body string
		dialect    snapshot.Dialect
		table      string
		want       string
		// line and column are where the condition starts in the file, the header being on line 1.
		line, column int
	}{
		{name: "a condition", body: "status <> 'closed'", want: "status <> 'closed'", line: 2, column: 1},
		{name: "a condition with a subquery", body: "session_id IN (SELECT id FROM sessions WHERE user_id = :userID)",
			want: "session_id IN (SELECT id FROM sessions WHERE user_id = :userID)", line: 2, column: 1},
		{name: "a condition starting with a subquery", body: "(SELECT count(*) FROM lines l WHERE l.order_id = id) > 0",
			want: "(SELECT count(*) FROM lines l WHERE l.order_id = id) > 0", line: 2, column: 1},
		{name: "a condition after a comment", body: "-- select the open ones\nstatus <> 'closed'",
			want: "-- select the open ones\nstatus <> 'closed'", line: 2, column: 1},
		{name: "a statement", body: "SELECT 1 FROM orders WHERE status <> 'closed'", want: "status <> 'closed'",
			line: 2, column: 28},
		{name: "a statement ended by a semicolon", body: "SELECT 1 FROM orders WHERE status <> 'closed'; -- open",
			want: "status <> 'closed'", line: 2, column: 28},
		{name: "a statement in lower case over lines",
			body: "select 1\nfrom orders\nwhere\n  session_id IN (\n    SELECT id FROM sessions ORDER BY id LIMIT 1\n  )\n;",
			want: "session_id IN (\n    SELECT id FROM sessions ORDER BY id LIMIT 1\n  )", line: 5, column: 3},
		{name: "a statement with comments between its words",
			body: "SELECT 1 /* one */ FROM orders -- the table\nWHERE -- open\nstatus <> 'closed'",
			want: "-- open\nstatus <> 'closed'", line: 3, column: 7},
		{name: "a statement with keywords in strings and names",
			body: `SELECT 1 FROM orders WHERE note = 'ORDER BY 1; LIMIT 2' AND "limit" > 0`,
			want: `note = 'ORDER BY 1; LIMIT 2' AND "limit" > 0`, line: 2, column: 28},
		{name: "a statement of a quoted table", body: `SELECT 1 FROM "orders" WHERE true`, want: "true", line: 2,
			column: 30},
		{name: "a statement of a table of a schema", body: `SELECT 1 FROM audit."events" WHERE true`, table: "audit.events",
			want: "true", line: 2, column: 36},
		{name: "a statement of a table quoted on MySQL", body: "SELECT 1 FROM `orders` WHERE `offset` > 0 AND offset < 9",
			dialect: snapshot.MySQL, want: "`offset` > 0 AND offset < 9", line: 2, column: 30},
		{name: "a statement with a column named lock on PostgreSQL", body: "SELECT 1 FROM orders WHERE lock IS NULL",
			want: "lock IS NULL", line: 2, column: 28},
		{name: "a statement with a dollar-quoted body",
			body: "SELECT 1 FROM orders WHERE note = $$ LIMIT 1; $$ AND kind = E'it\\'s; LIMIT'",
			want: "note = $$ LIMIT 1; $$ AND kind = E'it\\'s; LIMIT'", line: 2, column: 28},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			table, dialect := cmp.Or(tt.table, "orders"), cmp.Or(tt.dialect, snapshot.PostgreSQL)
			st := scope(t, "-- scope: Order.Open()\n"+tt.body+"\n")
			got, d := st.Condition(table, dialect)
			if d != nil {
				t.Fatalf("Condition: %v", d)
			}
			want := token.Position{Filename: path, Line: tt.line, Column: tt.column}
			if got.Text != tt.want || got.Pos != want {
				t.Errorf("Condition = %q at %v, want %q at %v", got.Text, got.Pos, tt.want, want)
			}
		})
	}
}

func TestStatement_ConditionTableName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, table, written string
		dialect              snapshot.Dialect
		same                 bool
	}{
		{"unquoted, folded to lower case on PostgreSQL", "orders", "Orders", snapshot.PostgreSQL, true},
		{"unquoted, of a table of upper case on PostgreSQL", "Orders", "Orders", snapshot.PostgreSQL, false},
		{"quoted, as written on PostgreSQL", "Orders", `"Orders"`, snapshot.PostgreSQL, true},
		{"quoted, in other case on PostgreSQL", "orders", `"Orders"`, snapshot.PostgreSQL, false},
		{"of a schema, unquoted on PostgreSQL", "audit.events", "Audit.EVENTS", snapshot.PostgreSQL, true},
		{"unquoted, in any case on MySQL", "Orders", "ORDERS", snapshot.MySQL, true},
		{"quoted, as written on MySQL", "Orders", "`Orders`", snapshot.MySQL, true},
		{"quoted, in other case on MySQL", "orders", "`ORDERS`", snapshot.MySQL, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st := scope(t, "-- scope: Order.Open()\nSELECT 1 FROM "+tt.written+" WHERE true\n")
			c, d := st.Condition(tt.table, tt.dialect)
			want := "order_repository.sql:1:1: error: -- scope: Order.Open: the statement selects from " + tt.written +
				", not from its table " + tt.table + "\n  write it as SELECT 1 FROM " + tt.table +
				" WHERE <condition>, or as the condition alone"
			switch {
			case tt.same && (d != nil || c.Text != "true"):
				t.Errorf("Condition = %q, %v; want true", c.Text, d)
			case !tt.same && (d == nil || d.String() != want):
				t.Errorf("Condition reports\n%v\nwant\n%s", d, want)
			}
		})
	}
}

func TestStatement_ConditionMistakes(t *testing.T) {
	t.Parallel()

	const (
		hint  = "\n  write it as SELECT 1 FROM orders WHERE <condition>, or as the condition alone"
		pg    = snapshot.PostgreSQL
		alias = "the statement gives orders an alias, which means nothing where kanna-orm puts its condition"
		shape = "the statement is not of the shape a scope takes"
		list  = "the statement selects other than 1"
		none  = "the statement has no WHERE condition"
		more  = "the statement holds more than a condition after WHERE"
	)
	tests := []struct {
		name, body string
		dialect    snapshot.Dialect
		want       string
	}{
		{"an alias", "SELECT 1 FROM orders AS o WHERE o.status <> 'closed'", pg, alias},
		{"an alias without AS", "SELECT 1 FROM orders o WHERE o.status <> 'closed'", pg, alias},
		{"another table", "SELECT 1 FROM order_lines WHERE true", pg,
			"the statement selects from order_lines, not from its table orders"},
		{"the table quoted as MySQL quotes it, on PostgreSQL", "SELECT 1 FROM `orders` WHERE true", pg, shape},
		{"a column", "SELECT id FROM orders WHERE true", pg, list},
		{"everything", "SELECT * FROM orders WHERE true", pg, list},
		{"two values", "SELECT 1, 2 FROM orders WHERE true", pg, list},
		{"a named value", "SELECT 1 AS one FROM orders WHERE true", pg, list},
		{"no table", "SELECT 1 WHERE true", pg, shape},
		{"a join", "SELECT 1 FROM orders JOIN order_lines ON order_id = id WHERE true", pg, shape},
		{"no WHERE", "SELECT 1 FROM orders;", pg, none},
		{"no condition", "SELECT 1 FROM orders WHERE -- none\n;", pg, none},
		{"a GROUP BY", "SELECT 1 FROM orders WHERE true GROUP BY status", pg, more},
		{"an ORDER BY", "SELECT 1 FROM orders WHERE (status <> 'closed') ORDER BY id", pg, more},
		{"a LIMIT", "SELECT 1 FROM orders WHERE true limit 1", pg, more},
		{"an OFFSET on PostgreSQL", "SELECT 1 FROM orders WHERE true OFFSET 1", pg, more},
		{"a lock", "SELECT 1 FROM orders WHERE true FOR UPDATE", pg, more},
		{"a lock on MySQL", "SELECT 1 FROM orders WHERE true LOCK IN SHARE MODE", snapshot.MySQL, more},
		{"a second statement", "SELECT 1 FROM orders WHERE true; DELETE FROM orders", pg, more},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st := scope(t, "-- scope: Order.Open()\n"+tt.body+"\n")
			_, d := st.Condition("orders", tt.dialect)
			want := "order_repository.sql:1:1: error: -- scope: Order.Open: " + tt.want + hint
			if d == nil || d.String() != want {
				t.Errorf("Condition reports\n%v\nwant\n%s", d, want)
			}
		})
	}
}

func TestCondition_At(t *testing.T) {
	t.Parallel()

	c := sqlfile.Condition{Text: "status <> 'closed'\n  AND kind = $2", Pos: token.Position{Filename: path, Line: 4,
		Column: 28}}
	for _, tt := range []struct{ line, column, wantLine, wantColumn int }{{1, 11, 4, 38}, {2, 14, 5, 14}} {
		got := c.At(tt.line, tt.column)
		if want := (token.Position{Filename: path, Line: tt.wantLine, Column: tt.wantColumn}); got != want {
			t.Errorf("At(%d, %d) = %v, want %v", tt.line, tt.column, got, want)
		}
	}
}

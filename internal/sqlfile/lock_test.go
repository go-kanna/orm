package sqlfile_test

import (
	"cmp"
	"go/token"
	"testing"

	"github.com/go-kanna/orm/internal/snapshot"
)

func TestStatement_Lock(t *testing.T) {
	t.Parallel()

	const my = snapshot.MySQL
	tests := []struct {
		name, body string
		dialect    snapshot.Dialect
		want       string
		// line and column are where the clause starts in the file, the header being on line 1.
		line, column int
	}{
		{name: "none", body: "SELECT id FROM orders"},
		{name: "FOR UPDATE", body: "SELECT id FROM orders\nFOR UPDATE SKIP LOCKED", want: "FOR UPDATE", line: 3,
			column: 1},
		{name: "FOR NO KEY UPDATE", body: "SELECT id FROM orders for no key update", want: "FOR NO KEY UPDATE", line: 2,
			column: 23},
		{name: "FOR SHARE", body: "SELECT id FROM orders FOR SHARE OF orders", want: "FOR SHARE", line: 2, column: 23},
		{name: "FOR KEY SHARE", body: "SELECT id FROM orders FOR KEY SHARE NOWAIT", want: "FOR KEY SHARE", line: 2,
			column: 23},
		{name: "in a subquery", body: "SELECT id FROM orders WHERE id IN (SELECT id FROM orders FOR UPDATE LIMIT 1)",
			want: "FOR UPDATE", line: 2, column: 58},
		{name: "FOR UPDATE on MySQL", body: "SELECT id FROM orders FOR UPDATE", dialect: my, want: "FOR UPDATE", line: 2,
			column: 23},
		{name: "FOR SHARE on MySQL", body: "SELECT id FROM orders FOR SHARE", dialect: my, want: "FOR SHARE", line: 2,
			column: 23},
		{name: "LOCK IN SHARE MODE on MySQL", body: "SELECT id FROM orders LOCK IN SHARE MODE", dialect: my,
			want: "LOCK IN SHARE MODE", line: 2, column: 23},
		{name: "LOCK IN SHARE MODE on PostgreSQL", body: "SELECT lock FROM orders"},
		{name: "FOR KEY SHARE on MySQL", body: "SELECT id FROM orders FOR KEY SHARE", dialect: my},
		{name: "in a string", body: "SELECT id FROM orders WHERE note = 'FOR UPDATE'"},
		{name: "in a quoted name", body: `SELECT "for" AS "update" FROM orders`},
		{name: "in a line comment", body: "SELECT id FROM orders -- FOR UPDATE\nWHERE id > 0"},
		{name: "in a block comment", body: "SELECT id FROM orders /* FOR UPDATE */"},
		{name: "in a dollar-quoted body", body: "SELECT id FROM orders WHERE note = $$FOR UPDATE$$"},
		{name: "in a MySQL comment", body: "SELECT id FROM orders # FOR UPDATE\nWHERE id > 0", dialect: my},
		{name: "FOR of SUBSTRING", body: "SELECT substring(note FROM 1 FOR 2) FROM orders"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st := scope(t, "-- query: ListOrders\n"+tt.body+"\n")
			pos, got, ok := st.Lock(cmp.Or(tt.dialect, snapshot.PostgreSQL))
			if tt.want == "" {
				if ok {
					t.Errorf("Lock = %q at %v, want none", got, pos)
				}
				return
			}
			want := token.Position{Filename: path, Line: tt.line, Column: tt.column}
			if !ok || got != tt.want || pos != want {
				t.Errorf("Lock = %q at %v, %t, want %q at %v", got, pos, ok, tt.want, want)
			}
		})
	}
}

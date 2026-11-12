package sqlfile_test

import (
	"cmp"
	"testing"

	"github.com/go-kanna/orm/internal/snapshot"
)

func TestStatement_OrderWithoutLimit(t *testing.T) {
	t.Parallel()

	const my = snapshot.MySQL
	tests := []struct {
		name, body string
		dialect    snapshot.Dialect
		want       bool
	}{
		{name: "none", body: "SELECT id FROM orders"},
		{name: "ORDER BY", body: "SELECT id FROM orders ORDER BY id", want: true},
		{name: "LIMIT", body: "SELECT id FROM orders ORDER BY id LIMIT 5"},
		{name: "FETCH FIRST", body: "SELECT id FROM orders ORDER BY id FETCH FIRST 5 ROWS ONLY"},
		{name: "FETCH NEXT", body: "SELECT id FROM orders ORDER BY id OFFSET 5 ROWS FETCH NEXT 5 ROWS ONLY"},
		{name: "LIMIT on MySQL", body: "SELECT id FROM orders ORDER BY id LIMIT 5, 10", dialect: my},
		{name: "ORDER BY on MySQL", body: "SELECT id FROM orders ORDER BY id", dialect: my, want: true},
		{name: "a column named order_id", body: "SELECT id, order_id FROM order_lines"},
		{name: "a column named sort_order", body: "SELECT id, sort_order FROM order_lines"},
		{name: "ORDER BY a column named limit_count", body: "SELECT id FROM quotas ORDER BY limit_count", want: true},
		{name: "ORDER BY a column named fetch", body: "SELECT id FROM jobs ORDER BY fetch", want: true},
		{name: "a quoted name", body: `SELECT "order" FROM order_lines`},
		{name: "a quoted name on MySQL", body: "SELECT `order` FROM order_lines", dialect: my},
		{name: "in a subquery", body: "SELECT id FROM (SELECT id FROM orders ORDER BY id) AS o"},
		{name: "in a string", body: "SELECT id FROM orders WHERE note = 'ORDER BY'"},
		{name: "in a line comment", body: "SELECT id FROM orders -- ORDER BY id\nWHERE id > 0"},
		{name: "in a block comment", body: "SELECT id FROM orders /* ORDER BY id */"},
		{name: "in a window", body: "SELECT max(id) OVER (ORDER BY placed_at) FROM orders"},
		{name: "a LIMIT in a subquery", body: "SELECT id FROM orders ORDER BY (SELECT 1 LIMIT 1)", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st := scope(t, "-- query: ListOrders\n"+tt.body+"\n")
			if got := st.OrderWithoutLimit(cmp.Or(tt.dialect, snapshot.PostgreSQL)); got != tt.want {
				t.Errorf("OrderWithoutLimit = %t, want %t", got, tt.want)
			}
		})
	}
}

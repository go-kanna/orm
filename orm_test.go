package orm_test

import (
	"slices"
	"testing"

	"github.com/go-kanna/orm"
)

// order is the model the scopes of these tests read.
type order struct{}

// query renders q into a query of its own, as a repository would.
func query(t *testing.T, q orm.Of[order], d orm.Dialect, first int) (string, []any) {
	t.Helper()

	r, err := q.Render(d, first)
	if err != nil {
		t.Fatal(err)
	}
	return "SELECT id FROM orders" + r.Where() + r.OrderBy() + r.Limit(), r.Args()
}

func TestRender(t *testing.T) {
	t.Parallel()

	var q orm.Of[order]
	page := q.Where("status <> ", "closed").Where("placed_at >= ", 3).OrderBy("placed_at", true).OrderBy("id", false).
		Limit(20).Offset(40)
	tests := []struct {
		name    string
		q       orm.Of[order]
		dialect orm.Dialect
		first   int
		sql     string
		args    []any
	}{
		{"nothing", q, orm.Postgres, 1, "SELECT id FROM orders", nil},
		{"conditions, an order, and a page", page, orm.Postgres, 1,
			"SELECT id FROM orders WHERE (status <> $1) AND (placed_at >= $2) ORDER BY placed_at DESC, id LIMIT 20 OFFSET 40",
			[]any{"closed", 3}},
		{"the same on MySQL", page, orm.MySQL, 1,
			"SELECT id FROM orders WHERE (status <> ?) AND (placed_at >= ?) ORDER BY placed_at DESC, id LIMIT 20 OFFSET 40",
			[]any{"closed", 3}},
		{"placeholders after those of the query", q.Where("status = ", "open"), orm.Postgres, 2,
			"SELECT id FROM orders WHERE (status = $2)", []any{"open"}},
		{"SQL after the argument", q.Where("date_trunc('month', placed_at) = date_trunc('month', ", 7, ")"),
			orm.Postgres, 1, "SELECT id FROM orders WHERE (date_trunc('month', placed_at) = date_trunc('month', $1))",
			[]any{7}},
		{"two arguments", q.Where("a = ", 1, " AND b = ", 2), orm.Postgres, 1,
			"SELECT id FROM orders WHERE (a = $1 AND b = $2)", []any{1, 2}},
		{"a list", q.Where("status IN (", orm.List([]string{"open", "paid"}), ")"), orm.MySQL, 1,
			"SELECT id FROM orders WHERE (status IN (?, ?))", []any{"open", "paid"}},
		{"a list on PostgreSQL", q.Where("status IN (", orm.List([]string{"open", "paid"}), ")"), orm.Postgres, 1,
			"SELECT id FROM orders WHERE (status IN ($1, $2))", []any{"open", "paid"}},
		{"an empty list", q.Where("status IN (", orm.List([]string(nil)), ")"), orm.MySQL, 1,
			"SELECT id FROM orders WHERE (status IN (NULL))", nil},
		{"an offset without a limit", q.Offset(10), orm.Postgres, 1, "SELECT id FROM orders OFFSET 10", nil},
		{"an offset without a limit on MySQL", q.Offset(10), orm.MySQL, 1,
			"SELECT id FROM orders LIMIT 18446744073709551615 OFFSET 10", nil},
		{"or", q.Where("a = ", 1).Or(q.Where("b = ", 2), q.Where("c = ", 3).Where("d = ", 4)), orm.Postgres, 1,
			"SELECT id FROM orders WHERE (a = $1) AND ((b = $2) OR ((c = $3) AND (d = $4)))", []any{1, 2, 3, 4}},
		{"or of no alternative", q.Or(), orm.Postgres, 1, "SELECT id FROM orders WHERE (1 = 0)", nil},
		{"or of an alternative matching every row", q.Or(q.Where("b = ", 2), q), orm.Postgres, 1,
			"SELECT id FROM orders", nil},
		{"not", q.Not(q.Where("b = ", 2).Where("c = ", 3)), orm.Postgres, 1,
			"SELECT id FROM orders WHERE (NOT ((b = $1) AND (c = $2)))", []any{2, 3}},
		{"not of every row", q.Not(q), orm.Postgres, 1, "SELECT id FROM orders WHERE (1 = 0)", nil},
		{"scopes joined", orm.Join(q.Where("a = ", 1).Limit(5), q.OrderBy("id", false).Limit(10).Offset(20)),
			orm.Postgres, 1, "SELECT id FROM orders WHERE (a = $1) ORDER BY id LIMIT 10 OFFSET 20", []any{1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sql, args := query(t, tt.q, tt.dialect, tt.first)
			if sql != tt.sql || !slices.Equal(args, tt.args) {
				t.Errorf("Render =\n%s %v\nwant\n%s %v", sql, args, tt.sql, tt.args)
			}
		})
	}
}

func TestRender_Errors(t *testing.T) {
	t.Parallel()

	var q orm.Of[order]
	tests := []struct {
		name string
		q    orm.Of[order]
		want string
	}{
		{"an order in or", q.Or(q.OrderBy("id", false)), "orm: Or takes conditions alone, but its scope orders the rows"},
		{"a limit in not", q.Not(q.Limit(1)), "orm: Not takes conditions alone, but its scope keeps only some of the rows"},
		{"an offset in or", q.Or(q.Where("a = ", 1), q.Offset(1)),
			"orm: Or takes conditions alone, but its scope keeps only some of the rows"},
		{"a negative limit", q.Limit(-1), "orm: a limit of -1 rows"},
		{"a negative offset", q.Offset(-1), "orm: an offset of -1 rows"},
		{"an error joined", orm.Join(q.Where("a = ", 1), q.Limit(-1)), "orm: a limit of -1 rows"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := tt.q.Render(orm.Postgres, 1); err == nil || err.Error() != tt.want {
				t.Errorf("Render error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestOf_LeavesItsReceiverAlone(t *testing.T) {
	t.Parallel()

	var q orm.Of[order]
	base := q.Where("a = ", 1).OrderBy("id", false)
	x := base.Where("b = ", 2).OrderBy("placed_at", true).Limit(10)
	y := base.Where("c = ", 3).Limit(20)

	for _, tt := range []struct {
		q   orm.Of[order]
		sql string
	}{
		{base, "SELECT id FROM orders WHERE (a = $1) ORDER BY id"},
		{x, "SELECT id FROM orders WHERE (a = $1) AND (b = $2) ORDER BY id, placed_at DESC LIMIT 10"},
		{y, "SELECT id FROM orders WHERE (a = $1) AND (c = $2) ORDER BY id LIMIT 20"},
	} {
		if sql, _ := query(t, tt.q, orm.Postgres, 1); sql != tt.sql {
			t.Errorf("Render =\n%s\nwant\n%s", sql, tt.sql)
		}
	}
}

func TestWhere_Panics(t *testing.T) {
	t.Parallel()

	for _, parts := range [][]any{nil, {1}, {"a = ", 1, 2}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Where%v did not panic", parts)
				}
			}()
			var q orm.Of[order]
			q.Where(parts...)
		}()
	}
}

func TestConditionsOnly(t *testing.T) {
	t.Parallel()

	var q orm.Of[order]
	for _, tt := range []struct {
		q    orm.Of[order]
		want string
	}{
		{q.Where("status <> ", "closed"), ""},
		{q.OrderBy("id", false), "orm: Count takes conditions alone, but its scope orders the rows"},
		{q.Limit(1), "orm: Count takes conditions alone, but its scope keeps only some of the rows"},
		{orm.Preload(q, "Lines", orm.Of[order]{}), "orm: Count takes conditions alone, but its scope loads Lines"},
	} {
		got := ""
		if err := tt.q.ConditionsOnly("Count"); err != nil {
			got = err.Error()
		}
		if got != tt.want {
			t.Errorf("ConditionsOnly = %q, want %q", got, tt.want)
		}
	}
}

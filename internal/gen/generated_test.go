package gen_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/go-kanna/orm/impl"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
	mysqlscopes "github.com/go-kanna/orm/internal/gen/testdata/mysql/scopes"
	pgscopes "github.com/go-kanna/orm/internal/gen/testdata/postgres/scopes"
)

// rendered is a scope as SQL: its clauses, and the arguments of their placeholders.
type rendered struct {
	sql  string
	args []any
}

func render(t *testing.T, q impl.Of[model.Order], d impl.Dialect) rendered {
	t.Helper()

	r, err := q.Render(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	return rendered{r.Where() + r.OrderBy() + r.Limit(), r.Args()}
}

func TestGenerated_Postgres(t *testing.T) {
	t.Parallel()

	email, _ := model.NewEmail("ann@example.com")
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		scope pgscopes.Order
		want  rendered
	}{
		{"every row", pgscopes.Orders(), rendered{}},
		{"Eq", pgscopes.Orders().StatusEq("open"), rendered{" WHERE (status = $1)", []any{model.OrderStatus("open")}}},
		{"Eq of a column that may be NULL", pgscopes.Orders().MemoEq("gift"),
			rendered{" WHERE (note = $1)", []any{"gift"}}},
		{"Eq of a value type", pgscopes.Orders().ContactEq(email), rendered{" WHERE (contact = $1)", []any{email}}},
		{"Eq of a reserved word", pgscopes.Orders().PositionEq(1), rendered{` WHERE ("order" = $1)`, []any{1}}},
		{"In", pgscopes.Orders().IDIn(1, 2),
			rendered{" WHERE (id = ANY($1))", []any{[]model.OrderID{1, 2}}}},
		{"NotIn", pgscopes.Orders().IDNotIn(1, 2),
			rendered{" WHERE (id <> ALL($1))", []any{[]model.OrderID{1, 2}}}},
		{"NotIn none", pgscopes.Orders().IDNotIn(), rendered{}},
		{"Between", pgscopes.Orders().PlacedAtBetween(day, day.AddDate(0, 1, 0)),
			rendered{" WHERE (placed_at BETWEEN $1 AND $2)", []any{day, day.AddDate(0, 1, 0)}}},
		{"ILike", pgscopes.Orders().MemoILike("gift%"), rendered{" WHERE (note ILIKE $1)", []any{"gift%"}}},
		{"HasPrefix", pgscopes.Orders().MemoHasPrefix("50%_off!"),
			rendered{" WHERE (note LIKE $1 ESCAPE '!')", []any{"50!%!_off!!%"}}},
		{"Contains", pgscopes.Orders().ContactContains("@example"),
			rendered{" WHERE (contact LIKE $1 ESCAPE '!')", []any{"%@example%"}}},
		{"IsNull", pgscopes.Orders().MemoIsNull().ContactIsNotNull(),
			rendered{" WHERE (note IS NULL) AND (contact IS NOT NULL)", nil}},
		{"order", pgscopes.Orders().PlacedAtDesc().IDAsc(), rendered{" ORDER BY placed_at DESC, id", nil}},
		{"Limit and Offset", pgscopes.Orders().Limit(20).Offset(40), rendered{" LIMIT 20 OFFSET 40", nil}},
		{"Or", pgscopes.Orders().Or(pgscopes.Orders().StatusEq("open"), pgscopes.Orders().TotalGt(100)),
			rendered{" WHERE ((status = $1) OR (total > $2))", []any{model.OrderStatus("open"), int64(100)}}},
		{"Not", pgscopes.Orders().Not(pgscopes.Orders().PaidEq(true)),
			rendered{" WHERE (NOT (paid = $1))", []any{true}}},
		{"a scope written by hand", pgscopes.Orders().PlacedInMonth(day),
			rendered{" WHERE (date_trunc('month', placed_at) = date_trunc('month', $1))", []any{day}}},
		{"a scope written by hand without parameters", pgscopes.Orders().Open(),
			rendered{" WHERE (status <> 'closed')", nil}},
		{"a scope written by hand with parameters used out of order", pgscopes.Orders().Busy(day, 3), rendered{
			" WHERE (placed_at >= $1\n  AND id IN (SELECT order_id FROM order_lines GROUP BY order_id HAVING count(*) >= $2))",
			[]any{day, int64(3)},
		}},
		{"a scope written by hand with a variadic parameter", pgscopes.Orders().Tagged("gift", "rush"), rendered{
			" WHERE (id IN (SELECT order_id FROM order_tags WHERE tag = ANY($1)))", []any{[]string{"gift", "rush"}},
		}},
	}
	for _, tt := range tests {
		if got := render(t, tt.scope.Of(), impl.Postgres); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestGenerated_MySQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		scope mysqlscopes.Order
		want  rendered
	}{
		{"Eq of a reserved word", mysqlscopes.Orders().PositionEq(1), rendered{" WHERE (`order` = ?)", []any{1}}},
		{"In", mysqlscopes.Orders().IDIn(1, 2), rendered{" WHERE (id IN (?, ?))", []any{model.OrderID(1), model.OrderID(2)}}},
		{"In none", mysqlscopes.Orders().IDIn(), rendered{" WHERE (id IN (NULL))", nil}},
		{"NotIn", mysqlscopes.Orders().StatusNotIn("open"),
			rendered{" WHERE (status NOT IN (?))", []any{model.OrderStatus("open")}}},
		{"HasPrefix", mysqlscopes.Orders().MemoHasPrefix("a_b"), rendered{" WHERE (note LIKE ? ESCAPE '!')", []any{"a!_b%"}}},
	}
	for _, tt := range tests {
		if got := render(t, tt.scope.Of(), impl.MySQL); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestGenerated_With(t *testing.T) {
	t.Parallel()

	s := pgscopes.Orders().WithCustomer().WithLines(pgscopes.OrderLines().SKUAsc()).WithTags()
	lines, ok := impl.Preloaded[model.OrderLine](s.Of(), "Lines")
	if !ok {
		t.Fatal("WithLines does not preload Lines")
	}
	r, err := lines.Render(impl.Postgres, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.OrderBy(); got != " ORDER BY sku" {
		t.Errorf("the lines are ordered by %q, want by sku", got)
	}
	if _, ok := impl.Preloaded[model.Customer](s.Of(), "Customer"); !ok {
		t.Error("WithCustomer does not preload Customer")
	}
	if _, ok := impl.Preloaded[model.Tag](s.Of(), "Tags"); !ok {
		t.Error("WithTags does not preload Tags")
	}
}

func TestGenerated_Paging(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	row := model.Order{ID: 7, PlacedAt: day}
	line := model.OrderLine{OrderID: 7, LineNo: 2}
	page := pgscopes.Orders().PlacedAtDesc()
	tests := []struct {
		name  string
		scope impl.Of[model.Order]
		d     impl.Dialect
		want  rendered
	}{
		{"After", page.After(row).Of(), impl.Postgres, rendered{
			" WHERE ((placed_at, id) < ($1, $2)) ORDER BY placed_at DESC, id DESC", []any{day, model.OrderID(7)},
		}},
		{"Before", page.Before(row).Of(), impl.Postgres, rendered{
			" WHERE ((placed_at, id) > ($1, $2)) ORDER BY placed_at, id", []any{day, model.OrderID(7)},
		}},
		{"After on MySQL", mysqlscopes.Orders().PlacedAtDesc().After(row).Of(), impl.MySQL, rendered{
			" WHERE ((placed_at < ?) OR (placed_at = ? AND id < ?)) ORDER BY placed_at DESC, id DESC",
			[]any{day, day, model.OrderID(7)},
		}},
	}
	for _, tt := range tests {
		if got := render(t, tt.scope, tt.d); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
	}

	r, err := pgscopes.OrderLines().After(line).Of().Render(impl.Postgres, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Where() + r.OrderBy(); got != " WHERE ((order_id, line_no) > ($1, $2)) ORDER BY order_id, line_no" {
		t.Errorf("After by a primary key of two columns: %s", got)
	}

	if _, err := pgscopes.Orders().MemoAsc().After(row).Of().Render(impl.Postgres, 1); err == nil {
		t.Error("After in the order of a column that may be NULL renders")
	}
}

func TestGenerated_Cursor(t *testing.T) {
	t.Parallel()

	row := model.Order{ID: 7, PlacedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Position: 3}
	page := pgscopes.Orders().PositionAsc().PlacedAtDesc()
	cursor, err := page.Cursor(row)
	if err != nil {
		t.Fatal(err)
	}
	next, err := page.AfterCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := render(t, next.Of(), impl.Postgres),
		render(t, page.After(row).Of(), impl.Postgres); !reflect.DeepEqual(got, want) {
		t.Errorf("AfterCursor(Cursor(row)) = %+v, want After(row), %+v", got, want)
	}

	if start, err := page.AfterCursor(""); err != nil || !reflect.DeepEqual(render(t, start.Of(), impl.Postgres),
		render(t, page.Of(), impl.Postgres)) {
		t.Errorf("AfterCursor of no cursor = %+v, %v; want the scope itself", start, err)
	}
	if _, err := pgscopes.Orders().PlacedAtAsc().AfterCursor(cursor); err == nil {
		t.Error("AfterCursor takes a cursor made in another order")
	}
}

package gen_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/go-kanna/orm/impl"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
	mscopes "github.com/go-kanna/orm/internal/gen/testdata/mysql/scopes"
	pscopes "github.com/go-kanna/orm/internal/gen/testdata/postgres/scopes"
)

// rendered is a scope as SQL: its clauses, and the arguments of their placeholders.
type rendered struct {
	sql  string
	args []any
}

func render(t *testing.T, q impl.Of[model.Order], d impl.Dialect) rendered {
	t.Helper()

	r, err := q.Render(d, 1, []string{"id"})
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
		scope pscopes.Order
		want  rendered
	}{
		{"every row", pscopes.Orders(), rendered{}},
		{"Eq", pscopes.Orders().StatusEq("open"), rendered{" WHERE (status = $1)", []any{model.OrderStatus("open")}}},
		{"Eq of a column that may be NULL", pscopes.Orders().MemoEq("gift"),
			rendered{" WHERE (note = $1)", []any{"gift"}}},
		{"Eq of a value type", pscopes.Orders().ContactEq(email), rendered{" WHERE (contact = $1)", []any{email}}},
		{"Eq of a reserved word", pscopes.Orders().PositionEq(1), rendered{` WHERE ("order" = $1)`, []any{1}}},
		{"In", pscopes.Orders().IDIn(1, 2),
			rendered{" WHERE (id = ANY($1))", []any{[]model.OrderID{1, 2}}}},
		{"NotIn", pscopes.Orders().IDNotIn(1, 2),
			rendered{" WHERE (id <> ALL($1))", []any{[]model.OrderID{1, 2}}}},
		{"NotIn none", pscopes.Orders().IDNotIn(), rendered{}},
		{"Between", pscopes.Orders().PlacedAtBetween(day, day.AddDate(0, 1, 0)),
			rendered{" WHERE (placed_at BETWEEN $1 AND $2)", []any{day, day.AddDate(0, 1, 0)}}},
		{"ILike", pscopes.Orders().MemoILike("gift%"), rendered{" WHERE (note ILIKE $1)", []any{"gift%"}}},
		{"HasPrefix", pscopes.Orders().MemoHasPrefix("50%_off!"),
			rendered{" WHERE (note LIKE $1 ESCAPE '!')", []any{"50!%!_off!!%"}}},
		{"Contains", pscopes.Orders().ContactContains("@example"),
			rendered{" WHERE (contact LIKE $1 ESCAPE '!')", []any{"%@example%"}}},
		{"IsNull", pscopes.Orders().MemoIsNull().ContactIsNotNull(),
			rendered{" WHERE (note IS NULL) AND (contact IS NOT NULL)", nil}},
		{"order", pscopes.Orders().PlacedAtDesc().IDAsc(), rendered{" ORDER BY placed_at DESC, id", nil}},
		{"Limit and Offset", pscopes.Orders().Limit(20).Offset(40), rendered{" LIMIT 20 OFFSET 40", nil}},
		{"Or", pscopes.Orders().Or(pscopes.Orders().StatusEq("open"), pscopes.Orders().TotalGt(100)),
			rendered{" WHERE ((status = $1) OR (total > $2))", []any{model.OrderStatus("open"), int64(100)}}},
		{"Not", pscopes.Orders().Not(pscopes.Orders().PaidEq(true)),
			rendered{" WHERE (NOT (paid = $1))", []any{true}}},
		{"a scope written by hand", pscopes.Orders().PlacedInMonth(day),
			rendered{" WHERE (date_trunc('month', placed_at) = date_trunc('month', $1))", []any{day}}},
		{"a scope written by hand without parameters", pscopes.Orders().Open(),
			rendered{" WHERE (status <> 'closed')", nil}},
		{"a scope written by hand with parameters used out of order", pscopes.Orders().Busy(day, 3), rendered{
			" WHERE (placed_at >= $1\n  AND id IN (SELECT order_id FROM order_lines GROUP BY order_id HAVING count(*) >= $2))",
			[]any{day, int64(3)},
		}},
		{"a scope written by hand with a variadic parameter", pscopes.Orders().Tagged("gift", "rush"), rendered{
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
		scope mscopes.Order
		want  rendered
	}{
		{"Eq of a reserved word", mscopes.Orders().PositionEq(1), rendered{" WHERE (`order` = ?)", []any{1}}},
		{"In", mscopes.Orders().IDIn(1, 2), rendered{" WHERE (id IN (?, ?))", []any{model.OrderID(1), model.OrderID(2)}}},
		{"In none", mscopes.Orders().IDIn(), rendered{" WHERE (id IN (NULL))", nil}},
		{"NotIn", mscopes.Orders().StatusNotIn("open"),
			rendered{" WHERE (status NOT IN (?))", []any{model.OrderStatus("open")}}},
		{"HasPrefix", mscopes.Orders().MemoHasPrefix("a_b"), rendered{" WHERE (note LIKE ? ESCAPE '!')", []any{"a!_b%"}}},
		{"a scope written by hand with a variadic parameter", mscopes.Orders().Tagged("gift", "rush"), rendered{
			" WHERE (id IN (SELECT order_id FROM order_tags WHERE tag IN (?, ?)))", []any{"gift", "rush"},
		}},
		{"a scope written by hand with a variadic parameter of none", mscopes.Orders().Tagged(), rendered{
			" WHERE (id IN (SELECT order_id FROM order_tags WHERE tag IN (SELECT NULL FROM DUAL WHERE FALSE)))", nil,
		}},
	}
	for _, tt := range tests {
		if got := render(t, tt.scope.Of(), impl.MySQL); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestGenerated_With(t *testing.T) {
	t.Parallel()

	s := pscopes.Orders().WithCustomer().WithLines(pscopes.OrderLines().SKUAsc()).WithTags()
	lines, ok := impl.Preloaded[model.OrderLine](s.Of(), "Lines")
	if !ok {
		t.Fatal("WithLines does not preload Lines")
	}
	r, err := lines.Render(impl.Postgres, 1, nil)
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
	page := pscopes.Orders().PlacedAtDesc()
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
		{"After on MySQL", mscopes.Orders().PlacedAtDesc().After(row).Of(), impl.MySQL, rendered{
			" WHERE ((placed_at < ?) OR (placed_at = ? AND id < ?)) ORDER BY placed_at DESC, id DESC",
			[]any{day, day, model.OrderID(7)},
		}},
	}
	for _, tt := range tests {
		if got := render(t, tt.scope, tt.d); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
	}

	r, err := pscopes.OrderLines().After(line).Of().Render(impl.Postgres, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Where() + r.OrderBy(); got != " WHERE ((order_id, line_no) > ($1, $2)) ORDER BY order_id, line_no" {
		t.Errorf("After by a primary key of two columns: %s", got)
	}

	if _, err := pscopes.Orders().MemoAsc().After(row).Of().Render(impl.Postgres, 1, nil); err == nil {
		t.Error("After in the order of a column that may be NULL renders")
	}
}

func TestGenerated_Cursor(t *testing.T) {
	t.Parallel()

	row := model.Order{ID: 7, PlacedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Position: 3}
	page := pscopes.Orders().PositionAsc().PlacedAtDesc()
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
	if _, err := pscopes.Orders().PlacedAtAsc().AfterCursor(cursor); err == nil {
		t.Error("AfterCursor takes a cursor made in another order")
	}
}

package gen_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/internal/gen/testdata/model"
	"github.com/go-kanna/orm/internal/gen/testdata/mysql"
	"github.com/go-kanna/orm/internal/gen/testdata/postgres"
)

// rendered is a scope as SQL: its clauses, and the arguments of their placeholders.
type rendered struct {
	sql  string
	args []any
}

func render(t *testing.T, q orm.Of[model.Order], d orm.Dialect) rendered {
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
		scope postgres.Order
		want  rendered
	}{
		{"every row", postgres.Orders(), rendered{}},
		{"Eq", postgres.Orders().StatusEq("open"), rendered{" WHERE (status = $1)", []any{model.OrderStatus("open")}}},
		{"Eq of a column that may be NULL", postgres.Orders().MemoEq("gift"),
			rendered{" WHERE (note = $1)", []any{"gift"}}},
		{"Eq of a value type", postgres.Orders().ContactEq(email), rendered{" WHERE (contact = $1)", []any{email}}},
		{"Eq of a reserved word", postgres.Orders().PositionEq(1), rendered{` WHERE ("order" = $1)`, []any{1}}},
		{"In", postgres.Orders().IDIn(1, 2),
			rendered{" WHERE (id = ANY($1))", []any{[]model.OrderID{1, 2}}}},
		{"NotIn", postgres.Orders().IDNotIn(1, 2),
			rendered{" WHERE (id <> ALL($1))", []any{[]model.OrderID{1, 2}}}},
		{"NotIn none", postgres.Orders().IDNotIn(), rendered{}},
		{"Between", postgres.Orders().PlacedAtBetween(day, day.AddDate(0, 1, 0)),
			rendered{" WHERE (placed_at BETWEEN $1 AND $2)", []any{day, day.AddDate(0, 1, 0)}}},
		{"ILike", postgres.Orders().MemoILike("gift%"), rendered{" WHERE (note ILIKE $1)", []any{"gift%"}}},
		{"HasPrefix", postgres.Orders().MemoHasPrefix("50%_off!"),
			rendered{" WHERE (note LIKE $1 ESCAPE '!')", []any{"50!%!_off!!%"}}},
		{"Contains", postgres.Orders().ContactContains("@example"),
			rendered{" WHERE (contact LIKE $1 ESCAPE '!')", []any{"%@example%"}}},
		{"IsNull", postgres.Orders().MemoIsNull().TagsIsNotNull(),
			rendered{" WHERE (note IS NULL) AND (tags IS NOT NULL)", nil}},
		{"order", postgres.Orders().PlacedAtDesc().IDAsc(), rendered{" ORDER BY placed_at DESC, id", nil}},
		{"Limit and Offset", postgres.Orders().Limit(20).Offset(40), rendered{" LIMIT 20 OFFSET 40", nil}},
		{"Or", postgres.Orders().Or(postgres.Orders().StatusEq("open"), postgres.Orders().TotalGt(100)),
			rendered{" WHERE ((status = $1) OR (total > $2))", []any{model.OrderStatus("open"), int64(100)}}},
		{"Not", postgres.Orders().Not(postgres.Orders().PaidEq(true)),
			rendered{" WHERE (NOT (paid = $1))", []any{true}}},
	}
	for _, tt := range tests {
		if got := render(t, tt.scope.Of(), orm.Postgres); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestGenerated_MySQL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		scope mysql.Order
		want  rendered
	}{
		{"Eq of a reserved word", mysql.Orders().PositionEq(1), rendered{" WHERE (`order` = ?)", []any{1}}},
		{"In", mysql.Orders().IDIn(1, 2), rendered{" WHERE (id IN (?, ?))", []any{model.OrderID(1), model.OrderID(2)}}},
		{"In none", mysql.Orders().IDIn(), rendered{" WHERE (id IN (NULL))", nil}},
		{"NotIn", mysql.Orders().StatusNotIn("open"),
			rendered{" WHERE (status NOT IN (?))", []any{model.OrderStatus("open")}}},
		{"HasPrefix", mysql.Orders().MemoHasPrefix("a_b"), rendered{" WHERE (note LIKE ? ESCAPE '!')", []any{"a!_b%"}}},
	}
	for _, tt := range tests {
		if got := render(t, tt.scope.Of(), orm.MySQL); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

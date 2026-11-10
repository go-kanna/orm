package scope_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/orm/impl"
	"github.com/go-kanna/orm/scope"
)

// What Raw writes adds to the scope, and loses nothing it held before.
func TestRaw(t *testing.T) {
	t.Parallel()

	held := scope.Of[int](impl.Of[int]{}.Where("a = ", 1).OrderBy("a", false).Limit(5))
	q := held.Raw().Where(scope.SQL("b > "), 2).Raw().OrderBy("lower(c)", true)
	r, err := impl.Of[int](q).Render(impl.Postgres, 1, []string{"id"})
	if err != nil {
		t.Fatal(err)
	}
	got := []any{r.Where(), r.OrderBy(), r.Limit(), r.Args()}
	want := []any{" WHERE (a = $1) AND (b > $2)", " ORDER BY a, lower(c) DESC, id DESC", " LIMIT 5", []any{1, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Raw = %q, want %q", got, want)
	}
}

// Raw's Where reads a part as SQL only when it is marked as such: a string is a value like any other.
func TestRawWhere(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name  string
		parts []any
		where string
		args  []any
	}{
		{"SQL alone", []any{scope.SQL("a IS NULL")}, " WHERE (a IS NULL)", nil},
		{"a string value", []any{scope.SQL("status = "), ") OR true OR status = ("},
			" WHERE (status = $1)", []any{") OR true OR status = ("}},
		{"values between SQL", []any{scope.SQL("a IN ("), 1, scope.SQL(", "), 2, scope.SQL(")")},
			" WHERE (a IN ($1, $2))", []any{1, 2}},
		{"a value first", []any{1, scope.SQL(" = a")}, " WHERE ($1 = a)", []any{1}},
		{"SQL in a row", []any{scope.SQL("a = "), scope.SQL("b")}, " WHERE (a = b)", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			q := scope.Of[int]{}.Raw().Where(c.parts...)
			r, err := impl.Of[int](q).Render(impl.Postgres, 1, []string{"id"})
			if err != nil {
				t.Fatal(err)
			}
			if got := r.Where(); got != c.where {
				t.Errorf("Where = %q, want %q", got, c.where)
			}
			if got := r.Args(); !reflect.DeepEqual(got, c.args) {
				t.Errorf("Args = %q, want %q", got, c.args)
			}
		})
	}
}

// A condition whose values Raw's Where cannot tell apart from its SQL is an error, which rendering the scope returns.
func TestRawWhereError(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name  string
		parts []any
		want  string
	}{
		{"no part", nil, "without SQL"},
		{"a string unmarked", []any{"a IS NULL"}, "without SQL"},
		{"SQL unmarked", []any{"status IN (", "a", ", ", "b", ")"}, "parts 0 and 1 are values"},
		{"values in a row", []any{scope.SQL("a IN ("), 1, 2, scope.SQL(")")}, "parts 1 and 2 are values"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			q := scope.Of[int]{}.Raw().Where(c.parts...).Raw().Where(scope.SQL("b = "), 1)
			_, err := impl.Of[int](q).Render(impl.Postgres, 1, []string{"id"})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Render = %v, want an error of %q", err, c.want)
			}
		})
	}
}

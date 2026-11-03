package scope_test

import (
	"reflect"
	"testing"

	"github.com/go-kanna/orm/impl"
	"github.com/go-kanna/orm/scope"
)

// What Raw writes adds to the scope, and loses nothing it held before.
func TestRaw(t *testing.T) {
	t.Parallel()

	held := scope.Of[int](impl.Of[int]{}.Where("a = ", 1).OrderBy("a", false).Limit(5))
	q := held.Raw().Where("b > ", 2).Raw().OrderBy("lower(c)", true)
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

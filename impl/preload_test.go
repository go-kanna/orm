package impl_test

import (
	"slices"
	"testing"

	"github.com/go-kanna/orm/impl"
)

// line and product are the models orders reach.
type (
	line    struct{}
	product struct{}
)

// sub renders the scope relation is preloaded by, as a loader does after a condition of its own.
func sub[C any](t *testing.T, q impl.Of[order], relation string) (string, []any) {
	t.Helper()

	s, ok := impl.Preloaded[C](q, relation)
	if !ok {
		t.Fatalf("%s is not preloaded", relation)
	}
	r, err := s.Render(impl.Postgres, 2)
	if err != nil {
		t.Fatal(err)
	}
	return "WHERE order_id = ANY($1)" + r.And() + r.OrderBy(), r.Args()
}

func TestPreload(t *testing.T) {
	t.Parallel()

	var (
		q impl.Of[order]
		l impl.Of[line]
		p impl.Of[product]
	)
	s := impl.Preload(q.Where("status = ", "open"), "Lines",
		impl.Preload(l.Where("quantity > ", 0).OrderBy("sku", false), "Product", p.Where("on_sale")))

	if sql, args := sub[line](t, s, "Lines"); sql != "WHERE order_id = ANY($1) AND (quantity > $2) ORDER BY sku" ||
		!slices.Equal(args, []any{0}) {
		t.Errorf("the scope of Lines renders as %s %v", sql, args)
	}
	lines, _ := impl.Preloaded[line](s, "Lines")
	if _, ok := impl.Preloaded[product](lines, "Product"); !ok {
		t.Error("Product is not preloaded along with Lines")
	}
	if _, ok := impl.Preloaded[product](s, "Customer"); ok {
		t.Error("Customer is preloaded, though nothing asked for it")
	}
	if sql, _ := query(t, s, impl.Postgres, 1); sql != "SELECT id FROM orders WHERE (status = $1)" {
		t.Errorf("the scope renders as %s, its preloads in it", sql)
	}
}

func TestPreload_JoinsTheScopesOfARelation(t *testing.T) {
	t.Parallel()

	var (
		q impl.Of[order]
		l impl.Of[line]
		p impl.Of[product]
	)
	const want = "WHERE order_id = ANY($1) AND (a = $2) AND (b = $3)"

	again := impl.Preload(impl.Preload(q, "Lines", l.Where("a = ", 1)), "Lines", l.Where("b = ", 2))
	if sql, _ := sub[line](t, again, "Lines"); sql != want {
		t.Errorf("preloaded again, the scope of Lines renders as %s, want %s", sql, want)
	}

	combined := impl.Combine(impl.Preload(q, "Lines", l.Where("a = ", 1)), impl.Preload(q, "Lines", l.Where("b = ", 2)),
		impl.Preload(q, "Customer", p))
	if sql, _ := sub[line](t, combined, "Lines"); sql != want {
		t.Errorf("combined, the scope of Lines renders as %s, want %s", sql, want)
	}
	if _, ok := impl.Preloaded[product](combined, "Customer"); !ok {
		t.Error("Customer is not preloaded once combined")
	}
}

func TestPreload_LeavesItsArgumentAlone(t *testing.T) {
	t.Parallel()

	var (
		q impl.Of[order]
		l impl.Of[line]
	)
	base := impl.Preload(q, "Lines", l.Where("a = ", 1))
	_ = impl.Preload(base, "Lines", l.Where("b = ", 2))
	if sql, _ := sub[line](t, base, "Lines"); sql != "WHERE order_id = ANY($1) AND (a = $2)" {
		t.Errorf("the scope of Lines renders as %s once preloaded again elsewhere", sql)
	}
}

func TestPreload_Errors(t *testing.T) {
	t.Parallel()

	var (
		q impl.Of[order]
		l impl.Of[line]
	)
	tests := []struct {
		name string
		q    impl.Of[order]
		want string
	}{
		{"a limit on the related rows", impl.Preload(q, "Lines", l.Limit(3)),
			"orm: Lines keeps only some of the rows it loads, which would count those of every row together"},
		{"an offset on the related rows", impl.Preload(q, "Lines", l.Offset(3)),
			"orm: Lines keeps only some of the rows it loads, which would count those of every row together"},
		{"an error in the related scope", impl.Preload(q, "Lines", l.Or(l.OrderBy("sku", false))),
			"orm: Or takes conditions alone, but its scope orders the rows"},
		{"a preload in or", q.Or(impl.Preload(q, "Lines", l)), "orm: Or takes conditions alone, but its scope loads Lines"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := tt.q.Render(impl.Postgres, 1); err == nil || err.Error() != tt.want {
				t.Errorf("Render error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestPreloaded_PanicsOnAnotherModel(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("Preloaded did not panic")
		}
	}()
	var (
		q impl.Of[order]
		l impl.Of[line]
	)
	impl.Preloaded[product](impl.Preload(q, "Lines", l), "Lines")
}

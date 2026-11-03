package impl_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/orm/impl"
)

type projectionParent struct{}
type projectionChild struct{}

func TestCorrelatedSelect(t *testing.T) {
	t.Parallel()
	for _, d := range []impl.Dialect{impl.Postgres, impl.MySQL} {
		t.Run(map[impl.Dialect]string{impl.Postgres: "postgres", impl.MySQL: "mysql"}[d], func(t *testing.T) {
			t.Parallel()
			p := impl.NewParent()
			sub := impl.CompareColumn(impl.Of[projectionChild]{}.Where("active = ", true), "cycle", "=",
				impl.NewColumn[int32](p, "cycle"))
			q := impl.Select(impl.Of[projectionParent]{}.Where("id = ", int64(7)), "Value", "children", "value", p,
				[]string{"id"}, []string{"parent_id"}, sub.One())
			sql, args, err := q.RenderSelect(d, "parents", []string{"id"}, []impl.Projection{{Field: "Value", Column: "value",
				Optional: true}})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(args, []any{true, int64(7)}) {
				t.Fatalf("args: %v", args)
			}
			if !strings.Contains(sql, "(SELECT") || strings.Contains(sql, "LIMIT 1") {
				t.Fatalf("One SQL: %s", sql)
			}
			if d == impl.Postgres && (!strings.Contains(sql, `"orm_parent"."cycle"`) ||
				!strings.Contains(sql, "active = $1") || !strings.Contains(sql, "id = $2")) {
				t.Fatalf("SQL: %s", sql)
			}
			if d == impl.MySQL && strings.Count(sql, "?") != 2 {
				t.Fatalf("SQL: %s", sql)
			}
		})
	}
}

func TestCorrelatedValidation(t *testing.T) {
	t.Parallel()
	p := impl.NewParent()
	for name, sub := range map[string]impl.Single[projectionChild]{
		"One limit":   impl.Of[projectionChild]{}.Limit(1).One(),
		"One offset":  impl.Of[projectionChild]{}.Offset(1).One(),
		"First order": impl.Of[projectionChild]{}.First(),
		"foreign parent": impl.CompareColumn(impl.Of[projectionChild]{}, "cycle", "=",
			impl.NewColumn[int32](impl.NewParent(), "cycle")).One(),
		"zero column": impl.CompareColumn(impl.Of[projectionChild]{}, "cycle", "=", impl.Column[int32]{}).One(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			q := impl.Select(impl.Of[projectionParent]{}, "Value", "children", "value", p, []string{"id"},
				[]string{"parent_id"}, sub)
			if _, _, err := q.RenderSelect(impl.Postgres, "parents", []string{"id"}, []impl.Projection{{Field: "Value",
				Column: "value", Optional: true}}); err == nil {
				t.Fatal("invalid selection accepted")
			}
		})
	}
	q := impl.CompareColumn(impl.Of[projectionChild]{}, "cycle", "=", impl.NewColumn[int32](p, "cycle"))
	if _, err := q.Render(impl.Postgres, 1); err == nil {
		t.Fatal("escaped parent reference accepted")
	}
	required := impl.Select(impl.Of[projectionParent]{}, "Value", "children", "value", p, []string{"id"},
		[]string{"parent_id"}, impl.Of[projectionChild]{}.One().Required())
	if err := impl.CheckSelection(required, "Value", true); err == nil {
		t.Fatal("missing required value accepted")
	}
	optional := impl.Select(impl.Of[projectionParent]{}, "Value", "children", "value", p, []string{"id"},
		[]string{"parent_id"}, impl.Of[projectionChild]{}.One())
	if _, _, err := optional.RenderSelect(impl.Postgres, "parents", []string{"id"}, []impl.Projection{{Field: "Value",
		Column: "value"}}); err == nil {
		t.Fatal("non-nullable destination accepted without Required")
	}
	if err := optional.ConditionsOnly("Count"); err == nil {
		t.Fatal("Count ignored a scalar selection")
	}
}

func TestCorrelatedFirstAndBatch(t *testing.T) {
	t.Parallel()
	for _, d := range []impl.Dialect{impl.Postgres, impl.MySQL} {
		p := impl.NewParent()
		first := impl.Select(impl.Of[projectionParent]{}, "Value", "children", "value", p, []string{"id"},
			[]string{"parent_id"}, impl.Of[projectionChild]{}.OrderBy("id", true).First())
		sql, _, err := first.RenderSelect(d, "parents", []string{"id"}, []impl.Projection{{Field: "Value", Column: "value",
			Optional: true}})
		if err != nil || !strings.Contains(sql, "ORDER BY id DESC LIMIT 1") {
			t.Fatalf("First: %s, %v", sql, err)
		}
		sub := impl.CompareColumn(impl.Of[projectionChild]{}, "cycle", "=", impl.NewColumn[int32](p,
			"cycle")).Where("active = ", true).OrderBy("id", true).Limit(2).Offset(1)
		sql, args, err := sub.RenderBatch(d, impl.Batch{ParentTable: "parents", Table: "children", ParentKeys: []string{"id"},
			ChildKeys: []string{"parent_id"}}, p, []string{"id", "cycle", "value"}, [][]any{{int64(7)}, {int64(8)}})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(sql, "ROW_NUMBER() OVER (PARTITION BY") || !strings.Contains(sql, " > 1") ||
			!strings.Contains(sql, " <= 3") {
			t.Fatalf("window: %s", sql)
		}
		if !reflect.DeepEqual(args, []any{int64(7), int64(8), true}) {
			t.Fatalf("batch args: %v", args)
		}
		if strings.Contains(sql, "SELECT * FROM (") { //nolint:unqueryvet // Reject wildcard projections.
			t.Fatalf("rank leaked into model columns: %s", sql)
		}
	}
}

func TestCombineDuplicateCollection(t *testing.T) {
	t.Parallel()
	parent := impl.NewParent()
	q := impl.PreloadMany(impl.Of[projectionParent]{}, "Children", parent, impl.Of[projectionChild]{}.Many())
	child, token, ok := impl.PreloadedMany[projectionChild](impl.Combine(q, q), "Children")
	if !ok {
		t.Fatal("collection was lost")
	}
	_, _, err := child.RenderBatch(impl.Postgres, impl.Batch{
		ParentTable: "parents", Table: "children", ParentKeys: []string{"id"}, ChildKeys: []string{"parent_id"},
	}, token, []string{"id"}, [][]any{{int64(1)}})
	if err == nil {
		t.Fatal("duplicate collection was silently accepted")
	}
	if _, _, err := impl.PreloadedMany[projectionChild](q, "Children"); !err {
		t.Fatal("original scope changed")
	}
}

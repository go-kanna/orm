package orm_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/orm"
)

type projectionParent struct{}
type projectionChild struct{}

func TestCorrelatedSelect(t *testing.T) {
	t.Parallel()
	for _, d := range []orm.Dialect{orm.Postgres, orm.MySQL} {
		t.Run(map[orm.Dialect]string{orm.Postgres: "postgres", orm.MySQL: "mysql"}[d], func(t *testing.T) {
			t.Parallel()
			p := orm.NewParent()
			sub := orm.CompareColumn(orm.Of[projectionChild]{}.Where("active = ", true), "cycle", "=",
				orm.NewColumn[int32](p, "cycle"))
			q := orm.Select(orm.Of[projectionParent]{}.Where("id = ", int64(7)), "Value", "children", "value", p,
				[]string{"id"}, []string{"parent_id"}, sub.One())
			sql, args, err := q.RenderSelect(d, "parents", []string{"id"}, []orm.Projection{{Field: "Value", Column: "value",
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
			if d == orm.Postgres && (!strings.Contains(sql, `"orm_parent"."cycle"`) ||
				!strings.Contains(sql, "active = $1") || !strings.Contains(sql, "id = $2")) {
				t.Fatalf("SQL: %s", sql)
			}
			if d == orm.MySQL && strings.Count(sql, "?") != 2 {
				t.Fatalf("SQL: %s", sql)
			}
		})
	}
}

func TestCorrelatedValidation(t *testing.T) {
	t.Parallel()
	p := orm.NewParent()
	for name, sub := range map[string]orm.Single[projectionChild]{
		"One limit":   orm.Of[projectionChild]{}.Limit(1).One(),
		"One offset":  orm.Of[projectionChild]{}.Offset(1).One(),
		"First order": orm.Of[projectionChild]{}.First(),
		"foreign parent": orm.CompareColumn(orm.Of[projectionChild]{}, "cycle", "=",
			orm.NewColumn[int32](orm.NewParent(), "cycle")).One(),
		"zero column": orm.CompareColumn(orm.Of[projectionChild]{}, "cycle", "=", orm.Column[int32]{}).One(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			q := orm.Select(orm.Of[projectionParent]{}, "Value", "children", "value", p, []string{"id"},
				[]string{"parent_id"}, sub)
			if _, _, err := q.RenderSelect(orm.Postgres, "parents", []string{"id"}, []orm.Projection{{Field: "Value",
				Column: "value", Optional: true}}); err == nil {
				t.Fatal("invalid selection accepted")
			}
		})
	}
	q := orm.CompareColumn(orm.Of[projectionChild]{}, "cycle", "=", orm.NewColumn[int32](p, "cycle"))
	if _, err := q.Render(orm.Postgres, 1); err == nil {
		t.Fatal("escaped parent reference accepted")
	}
	required := orm.Select(orm.Of[projectionParent]{}, "Value", "children", "value", p, []string{"id"},
		[]string{"parent_id"}, orm.Of[projectionChild]{}.One().Required())
	if err := orm.CheckSelection(required, "Value", true); err == nil {
		t.Fatal("missing required value accepted")
	}
	optional := orm.Select(orm.Of[projectionParent]{}, "Value", "children", "value", p, []string{"id"},
		[]string{"parent_id"}, orm.Of[projectionChild]{}.One())
	if _, _, err := optional.RenderSelect(orm.Postgres, "parents", []string{"id"}, []orm.Projection{{Field: "Value",
		Column: "value"}}); err == nil {
		t.Fatal("non-nullable destination accepted without Required")
	}
	if err := optional.ConditionsOnly("Count"); err == nil {
		t.Fatal("Count ignored a scalar selection")
	}
}

func TestCorrelatedFirstAndBatch(t *testing.T) {
	t.Parallel()
	for _, d := range []orm.Dialect{orm.Postgres, orm.MySQL} {
		p := orm.NewParent()
		first := orm.Select(orm.Of[projectionParent]{}, "Value", "children", "value", p, []string{"id"},
			[]string{"parent_id"}, orm.Of[projectionChild]{}.OrderBy("id", true).First())
		sql, _, err := first.RenderSelect(d, "parents", []string{"id"}, []orm.Projection{{Field: "Value", Column: "value",
			Optional: true}})
		if err != nil || !strings.Contains(sql, "ORDER BY id DESC LIMIT 1") {
			t.Fatalf("First: %s, %v", sql, err)
		}
		sub := orm.CompareColumn(orm.Of[projectionChild]{}, "cycle", "=", orm.NewColumn[int32](p,
			"cycle")).Where("active = ", true).OrderBy("id", true).Limit(2).Offset(1)
		sql, args, err := sub.RenderBatch(d, orm.Batch{ParentTable: "parents", Table: "children", ParentKeys: []string{"id"},
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

func TestJoinDuplicateCollection(t *testing.T) {
	t.Parallel()
	parent := orm.NewParent()
	q := orm.PreloadMany(orm.Of[projectionParent]{}, "Children", parent, orm.Of[projectionChild]{}.Many())
	child, token, ok := orm.Batched[projectionChild](orm.Join(q, q), "Children")
	if !ok {
		t.Fatal("collection was lost")
	}
	_, _, err := child.RenderBatch(orm.Postgres, orm.Batch{
		ParentTable: "parents", Table: "children", ParentKeys: []string{"id"}, ChildKeys: []string{"parent_id"},
	}, token, []string{"id"}, [][]any{{int64(1)}})
	if err == nil {
		t.Fatal("duplicate collection was silently accepted")
	}
	if _, _, err := orm.Batched[projectionChild](q, "Children"); !err {
		t.Fatal("original scope changed")
	}
}

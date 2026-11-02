package orm_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/orm"
)

func TestChanges(t *testing.T) {
	t.Parallel()
	for _, d := range []orm.Dialect{orm.Postgres, orm.MySQL} {
		base := orm.Changes[int]{}.Set("note", "original")
		changed := base.Set("note", nil).Set("paid", false).Set("total", 0)
		q := orm.Of[int]{}.Where("id = ", 7).Where("accepted_at IS NULL")
		sql, args, err := changed.RenderUpdate(d, "order", []string{"note", "paid", "total"}, q)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(args, []any{nil, false, 0, 7}) || strings.Contains(sql, "original") {
			t.Fatalf("updated values: %s, %v", sql, args)
		}
		if d == orm.Postgres && (!strings.Contains(sql, `"note" = $1`) || !strings.Contains(sql, "id = $4")) {
			t.Fatalf("placeholder numbering: %s", sql)
		}
		_, original, err := base.RenderUpdate(d, "order", []string{"note"}, q)
		if err != nil || !reflect.DeepEqual(original, []any{"original", 7}) {
			t.Fatalf("original scope mutated: %v, %v", original, err)
		}
		for _, tc := range []struct {
			c orm.Changes[int]
			q orm.Of[int]
		}{
			{orm.Changes[int]{}, q},
			{base.Set("id", 10), q},
			{base, orm.Of[int]{}},
			{base, q.OrderBy("id", false)},
			{base, q.Limit(1)},
		} {
			if _, _, err := tc.c.RenderUpdate(d, "order", []string{"note"}, tc.q); err == nil {
				t.Fatal("invalid update was accepted")
			}
		}
	}
}

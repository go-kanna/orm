package impl_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/orm/impl"
)

func TestRenderDelete(t *testing.T) {
	t.Parallel()
	for _, d := range []impl.Dialect{impl.Postgres, impl.MySQL} {
		base := impl.Of[int]{}.Where("status = ", "old")
		q := base.OrderBy("expires_at", false).OrderBy("id", false).Limit(2).Offset(1)
		sql, args, err := q.RenderDelete(d, "records", []string{"tenant_id", "id"})
		if err != nil || !reflect.DeepEqual(args, []any{"old"}) {
			t.Fatalf("delete page: %s, %v, %v", sql, args, err)
		}
		if !strings.Contains(sql, "ORDER BY expires_at, id LIMIT 2 OFFSET 1") || strings.Contains(sql, "'old'") {
			t.Fatalf("target selection: %s", sql)
		}
		if d == impl.MySQL && !strings.Contains(sql, "orm_delete_targets") {
			t.Fatalf("MySQL derived target table: %s", sql)
		}
		plain, _, err := base.RenderDelete(d, "records", nil)
		if err != nil || strings.Contains(plain, " IN (") || strings.Contains(plain, "LIMIT") {
			t.Fatalf("plain delete or mutated original: %s, %v", plain, err)
		}
		for _, invalid := range []impl.Of[int]{
			{}, base.Limit(1), base.OrderBy("id", false), base.Limit(-1), base.Offset(-1),
		} {
			if _, _, err := invalid.RenderDelete(d, "records", []string{"id"}); err == nil {
				t.Fatal("invalid deletion scope accepted")
			}
		}
		if _, _, err := q.RenderDelete(d, "records", nil); err == nil {
			t.Fatal("paged delete without identity accepted")
		}
		zero, _, err := base.OrderBy("id", false).Limit(0).RenderDelete(d, "records", []string{"id"})
		if err != nil || !strings.Contains(zero, "LIMIT 0") {
			t.Fatalf("zero limit: %s, %v", zero, err)
		}
	}
}

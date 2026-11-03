package impl_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/orm/impl"
)

func TestRenderEach(t *testing.T) {
	t.Parallel()

	lines := impl.Of[line]{}.Where("quantity > ", 0).OrderBy("sku", false).Offset(1).Limit(2)
	byOrder := impl.Batch{Table: "order_lines", ChildKeys: []string{"order_id"}}
	throughTags := impl.Batch{Table: "tags", Through: "order_tags", ThroughParent: []string{"order_id"},
		ThroughChild: []string{"tag"}, TargetKeys: []string{"name"}}
	tests := []struct {
		name    string
		d       impl.Dialect
		batch   impl.Batch
		key     []string
		columns []string
		sql     string
		args    []any
	}{
		{"has many", impl.Postgres, byOrder, []string{"order_id", "line_no"}, []string{"order_id", "line_no", "sku"},
			`SELECT "orm_key_0", "order_id", "line_no", "sku" FROM (SELECT "orm_child"."order_id" AS "orm_key_0", ` +
				`"orm_child"."order_id", "orm_child"."line_no", "orm_child"."sku", ROW_NUMBER() OVER (PARTITION BY ` +
				`"orm_child"."order_id" ORDER BY sku, order_id, line_no) AS "orm_rank" FROM "order_lines" AS "orm_child" ` +
				`WHERE (("orm_child"."order_id" = $1) OR ("orm_child"."order_id" = $2)) AND (quantity > $3)) ` +
				`AS "orm_ranked" WHERE "orm_rank" > 1 AND "orm_rank" <= 3 ORDER BY "orm_rank"`,
			[]any{int64(7), int64(8), 0}},
		{"many to many on MySQL", impl.MySQL, throughTags, []string{"name"}, []string{"name"},
			"SELECT `orm_key_0`, `name` FROM (SELECT `orm_link`.`orm_l_order_id` AS `orm_key_0`, `orm_child`.`name`, " +
				"ROW_NUMBER() OVER (PARTITION BY `orm_link`.`orm_l_order_id` ORDER BY sku, name) AS `orm_rank` " +
				"FROM `tags` AS `orm_child` JOIN (SELECT `order_id` AS `orm_l_order_id`, `tag` AS `orm_l_tag` " +
				"FROM `order_tags`) AS `orm_link` ON `orm_link`.`orm_l_tag` = `orm_child`.`name` " +
				"WHERE ((`orm_link`.`orm_l_order_id` = ?) OR (`orm_link`.`orm_l_order_id` = ?)) AND (quantity > ?)) " +
				"AS `orm_ranked` WHERE `orm_rank` > 1 AND `orm_rank` <= 3 ORDER BY `orm_rank`",
			[]any{int64(7), int64(8), 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sql, args, err := lines.RenderEach(tt.d, tt.batch, tt.key, tt.columns, [][]any{{int64(7)}, {int64(8)}})
			if err != nil {
				t.Fatal(err)
			}
			if sql != tt.sql || !reflect.DeepEqual(args, tt.args) {
				t.Errorf("RenderEach =\n%s %v\nwant\n%s %v", sql, args, tt.sql, tt.args)
			}
		})
	}
}

func TestRenderEach_OrdersByTheKeyWithoutAnOrder(t *testing.T) {
	t.Parallel()

	sql, _, err := impl.Of[line]{}.Limit(1).RenderEach(impl.Postgres,
		impl.Batch{Table: "order_lines", ChildKeys: []string{"order_id"}}, []string{"order_id", "line_no"},
		[]string{"sku"}, [][]any{{int64(7)}})
	if err != nil || !strings.Contains(sql, "ORDER BY order_id, line_no) AS") {
		t.Errorf("RenderEach = %s, %v; want the rows of each counted by the primary key", sql, err)
	}
}

func TestRenderEach_Errors(t *testing.T) {
	t.Parallel()

	var l impl.Of[line]
	byOrder := impl.Batch{Table: "order_lines", ChildKeys: []string{"order_id"}}
	tests := []struct {
		name string
		q    impl.Of[line]
		key  []string
		want string
	}{
		{"no order on a table without a primary key", l.Limit(1), nil,
			"orm: a page of the related rows of each row requires an order"},
		{"a row to continue from", l.OrderBy("sku", false).After([]string{"sku"}, map[string]any{"sku": "A"}).Limit(1),
			[]string{"sku"}, "orm: a page of the related rows of each row continues by Offset, not by a row"},
		{"an overflowing page", l.OrderBy("sku", false).Offset(1).Limit(int(^uint(0) >> 1)), []string{"sku"},
			"orm: a page of 9223372036854775807 rows after 1 overflows"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, _, err := tt.q.RenderEach(impl.Postgres, byOrder, tt.key, []string{"sku"}, [][]any{{int64(7)}}); err == nil ||
				err.Error() != tt.want {
				t.Errorf("RenderEach error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestPreloadEach(t *testing.T) {
	t.Parallel()

	var (
		q impl.Of[order]
		l impl.Of[line]
	)
	paged := impl.PreloadEach(q, "Lines", l.OrderBy("sku", false).Limit(3))
	sub, ok := impl.Preloaded[line](paged, "Lines")
	if !ok || !sub.PagesEach() {
		t.Errorf("Preloaded = %v, PagesEach = %v; want the page of the lines of each order", ok, sub.PagesEach())
	}
	if _, err := paged.Render(impl.Postgres, 1, nil); err != nil {
		t.Errorf("Render with a page of the lines of each order = %v", err)
	}
	cursor := impl.PreloadEach(q, "Lines", l.After([]string{"sku"}, map[string]any{"sku": "A"}))
	if _, err := cursor.Render(impl.Postgres, 1, nil); err == nil ||
		err.Error() != "orm: Lines continues a page of the rows of each row by Offset, not by a row" {
		t.Errorf("Render with a row to continue the lines from = %v", err)
	}
}

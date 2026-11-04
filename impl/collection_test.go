package impl_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/orm/impl"
)

func TestRenderEach(t *testing.T) {
	t.Parallel()

	var l impl.Of[line]
	paged := l.Where("quantity > ", 0).OrderBy("sku", false).Offset(1).Limit(2)
	byOrder := impl.Batch{Table: "order_lines", ChildKeys: []string{"order_id"}, KeyTypes: []string{"int8"}}
	throughTags := impl.Batch{Table: "tags", Through: "order_tags", ThroughParent: []string{"order_id"},
		ThroughChild: []string{"tag"}, TargetKeys: []string{"name"}}
	keys := []any{[]int64{7, 8}}
	tests := []struct {
		name    string
		q       impl.Of[line]
		d       impl.Dialect
		batch   impl.Batch
		key     []string
		columns []string
		first   int
		sql     string
		args    []any
	}{
		{"has many", l.Where("quantity > ", 0).OrderBy("sku", false), impl.Postgres, byOrder,
			[]string{"order_id", "line_no"}, []string{"sku"}, 1000,
			`SELECT "orm_key"."orm_n", "orm_child"."sku" FROM unnest($1::int8[], $2::int8[]) AS "orm_key" ("orm_n", ` +
				`"orm_k0") JOIN "order_lines" AS "orm_child" ON "orm_child"."order_id" = "orm_key"."orm_k0" ` +
				`WHERE (quantity > $3) ORDER BY sku, order_id, line_no`,
			[]any{[]int64{1000, 1001}, []int64{7, 8}, 0}},
		{"a key of a type of a schema", l, impl.Postgres, impl.Batch{Table: "order_lines", ChildKeys: []string{"state"},
			KeyTypes: []string{`public."State"`}}, nil, []string{"sku"}, 0,
			`SELECT "orm_key"."orm_n", "orm_child"."sku" FROM unnest($1::int8[], $2::text[]::public."State"[]) AS ` +
				`"orm_key" ("orm_n", "orm_k0") JOIN "order_lines" AS "orm_child" ON "orm_child"."state" = "orm_key"."orm_k0"`,
			[]any{[]int64{0, 1}, []int64{7, 8}}},
		{"a page of has many", paged, impl.Postgres, byOrder, []string{"order_id", "line_no"},
			[]string{"order_id", "line_no", "sku"}, 0,
			`SELECT "orm_n", "order_id", "line_no", "sku" FROM (SELECT "orm_key"."orm_n", "orm_child"."order_id", ` +
				`"orm_child"."line_no", "orm_child"."sku", ROW_NUMBER() OVER (PARTITION BY "orm_key"."orm_n" ORDER BY ` +
				`sku, order_id, line_no) AS "orm_rank" FROM unnest($1::int8[], $2::int8[]) AS "orm_key" ("orm_n", ` +
				`"orm_k0") JOIN "order_lines" AS "orm_child" ON "orm_child"."order_id" = "orm_key"."orm_k0" ` +
				`WHERE (quantity > $3)) AS "orm_ranked" WHERE "orm_rank" > 1 AND "orm_rank" <= 3 ORDER BY "orm_rank"`,
			[]any{[]int64{0, 1}, []int64{7, 8}, 0}},
		{"a page of many to many on MySQL", paged, impl.MySQL, throughTags, []string{"name"}, []string{"name"}, 0,
			"SELECT `orm_n`, `name` FROM (SELECT `orm_key`.`orm_n`, `orm_child`.`name`, ROW_NUMBER() OVER " +
				"(PARTITION BY `orm_key`.`orm_n` ORDER BY sku, name) AS `orm_rank` FROM (VALUES ROW(?, ?), ROW(?, ?)) " +
				"AS `orm_key` (`orm_n`, `orm_k0`) JOIN (SELECT `order_id` AS `orm_l_order_id`, `tag` AS `orm_l_tag` " +
				"FROM `order_tags`) AS `orm_link` ON `orm_link`.`orm_l_order_id` = `orm_key`.`orm_k0` JOIN `tags` AS " +
				"`orm_child` ON `orm_link`.`orm_l_tag` = `orm_child`.`name` WHERE (quantity > ?)) AS `orm_ranked` " +
				"WHERE `orm_rank` > 1 AND `orm_rank` <= 3 ORDER BY `orm_rank`",
			[]any{0, int64(7), 1, int64(8), 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sql, args, err := tt.q.RenderEach(impl.NewEach(tt.d, tt.batch, tt.key, tt.columns), keys, tt.first)
			if err != nil {
				t.Fatal(err)
			}
			if sql != tt.sql || !reflect.DeepEqual(args, tt.args) {
				t.Errorf("RenderEach =\n%s %#v\nwant\n%s %#v", sql, args, tt.sql, tt.args)
			}
		})
	}
}

func TestRenderEach_OrdersByTheKeyWithoutAnOrder(t *testing.T) {
	t.Parallel()

	sql, _, err := impl.Of[line]{}.Limit(1).RenderEach(impl.NewEach(impl.Postgres,
		impl.Batch{Table: "order_lines", ChildKeys: []string{"order_id"}, KeyTypes: []string{"int8"}},
		[]string{"order_id", "line_no"}, []string{"sku"}), []any{[]int64{7}}, 0)
	if err != nil || !strings.Contains(sql, "ORDER BY order_id, line_no) AS") {
		t.Errorf("RenderEach = %s, %v; want the rows of each counted by the primary key", sql, err)
	}
}

func TestRenderEach_Errors(t *testing.T) {
	t.Parallel()

	var l impl.Of[line]
	byOrder := impl.Batch{Table: "order_lines", ChildKeys: []string{"order_id"}, KeyTypes: []string{"int8"}}
	untyped := impl.Batch{Table: "order_lines", ChildKeys: []string{"order_id"}}
	tests := []struct {
		name  string
		q     impl.Of[line]
		batch impl.Batch
		key   []string
		keys  []any
		want  string
	}{
		{"no order on a table without a primary key", l.Limit(1), byOrder, nil, []any{[]int64{7}},
			"orm: a page of the related rows of each row requires an order"},
		{"a row to continue from", l.OrderBy("sku", false).After([]string{"sku"}, map[string]any{"sku": "A"}).Limit(1),
			byOrder, []string{"sku"}, []any{[]int64{7}},
			"orm: a page of the related rows of each row continues by Offset, not by a row"},
		{"an overflowing page", l.OrderBy("sku", false).Offset(1).Limit(int(^uint(0) >> 1)), byOrder, []string{"sku"},
			[]any{[]int64{7}}, "orm: a page of 9223372036854775807 rows after 1 overflows"},
		{"no key", l, byOrder, nil, []any{[]int64{}}, "orm: related rows need the keys of the rows they are loaded for"},
		{"a key of another width", l, byOrder, nil, []any{[]int64{7}, []int64{1}},
			"orm: a key of the rows related to has another number of columns"},
		{"no types to cast the keys to", l, untyped, nil, []any{[]int64{7}},
			"orm: PostgreSQL casts each column of a key to its type, which the batch does not name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			each := impl.NewEach(impl.Postgres, tt.batch, tt.key, []string{"sku"})
			if _, _, err := tt.q.RenderEach(each, tt.keys, 0); err == nil ||
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

func TestGroup(t *testing.T) {
	t.Parallel()

	rows, numbers := []string{"a", "b", "c", "d", "e"}, []int{2, 0, 2, 0, 2}
	groups := impl.Group(rows, numbers, 4)
	if want := [][]string{{"b", "d"}, nil, {"a", "c", "e"}, nil}; !reflect.DeepEqual(groups, want) {
		t.Errorf("Group = %q, want %q", groups, want)
	}
	groups[0] = append(groups[0], "x")
	if groups[2][0] != "a" {
		t.Errorf("appending to the rows of one key changed those of another: %q", groups)
	}
	pointers := impl.GroupPointers(rows, numbers, 4)
	if len(pointers[0]) != 2 || pointers[0][1] != &rows[3] || pointers[1] != nil || len(pointers[2]) != 3 {
		t.Errorf("GroupPointers = %v", pointers)
	}
}

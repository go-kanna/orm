package impl_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/orm/impl"
)

func TestChanges(t *testing.T) {
	t.Parallel()
	for _, d := range []impl.Dialect{impl.Postgres, impl.MySQL} {
		base := impl.Changes[int]{}.Set("note", "original")
		changed := base.Set("note", nil).Set("paid", false).Set("total", 0)
		q := impl.Of[int]{}.Where("id = ", 7).Where("accepted_at IS NULL")
		sql, args, err := changed.RenderUpdate(d, "order", []string{"note", "paid", "total"}, q)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(args, []any{nil, false, 0, 7}) || strings.Contains(sql, "original") {
			t.Fatalf("updated values: %s, %v", sql, args)
		}
		if d == impl.Postgres && (!strings.Contains(sql, `"note" = $1`) || !strings.Contains(sql, "id = $4")) {
			t.Fatalf("placeholder numbering: %s", sql)
		}
		_, original, err := base.RenderUpdate(d, "order", []string{"note"}, q)
		if err != nil || !reflect.DeepEqual(original, []any{"original", 7}) {
			t.Fatalf("original scope mutated: %v, %v", original, err)
		}
		for _, tc := range []struct {
			c impl.Changes[int]
			q impl.Of[int]
		}{
			{impl.Changes[int]{}, q},
			{base.Set("id", 10), q},
			{base, impl.Of[int]{}},
			{base, q.OrderBy("id", false)},
			{base, q.Limit(1)},
		} {
			if _, _, err := tc.c.RenderUpdate(d, "order", []string{"note"}, tc.q); err == nil {
				t.Fatal("invalid update was accepted")
			}
		}
	}
}

func TestChanges_RenderUpdateSelected(t *testing.T) {
	t.Parallel()

	const lock = " FOR UPDATE SKIP LOCKED"
	changes := impl.Changes[int]{}.Set("status", "taken")
	base := impl.Of[int]{}.Where("status = ", "new")
	page := base.OrderBy("queued_at", false).Limit(2).Offset(1)
	key := []string{"id"}
	for _, tt := range []struct {
		name string
		d    impl.Dialect
		q    impl.Of[int]
		key  []string
		lock string
		want string
	}{
		{"no page", impl.Postgres, base, key, "", `UPDATE "jobs" SET "status" = $1 WHERE (status = $2)`},
		{"a page", impl.Postgres, page, key, "", `UPDATE "jobs" SET "status" = $1 WHERE "id" IN ` +
			`(SELECT "id" FROM "jobs" WHERE (status = $2) ORDER BY queued_at, id LIMIT 2 OFFSET 1)`},
		{"a page on MySQL", impl.MySQL, page, key, "", "UPDATE `jobs` SET `status` = ? WHERE `id` IN " +
			"(SELECT `id` FROM (SELECT `id` FROM `jobs` WHERE (status = ?) ORDER BY queued_at, id LIMIT 2 OFFSET 1) " +
			"AS `orm_update_targets`)"},
		{"a page of no order", impl.Postgres, base.Limit(1), []string{"tenant_id", "id"}, "",
			`UPDATE "jobs" SET "status" = $1 WHERE ("tenant_id", "id") IN ` +
				`(SELECT "tenant_id", "id" FROM "jobs" WHERE (status = $2) LIMIT 1)`},
		{"a page continued", impl.Postgres, base.OrderBy("id", false).After(key, map[string]any{"id": 7}).Limit(1),
			key, "", `UPDATE "jobs" SET "status" = $1 WHERE "id" IN ` +
				`(SELECT "id" FROM "jobs" WHERE (status = $2) AND ((id) > ($3)) ORDER BY id LIMIT 1)`},
		{"a lock", impl.Postgres, base, key, lock, `UPDATE "jobs" SET "status" = $1 WHERE "id" IN ` +
			`(SELECT "id" FROM "jobs" WHERE (status = $2) FOR UPDATE SKIP LOCKED)`},
		{"a page under a lock", impl.Postgres, page, key, lock, `UPDATE "jobs" SET "status" = $1 WHERE "id" IN ` +
			`(SELECT "id" FROM "jobs" WHERE (status = $2) ORDER BY queued_at, id LIMIT 2 OFFSET 1 FOR UPDATE SKIP LOCKED)`},
	} {
		got, args, err := changes.RenderUpdateSelected(tt.d, "jobs", []string{"status"}, tt.key, tt.lock, tt.q)
		if err != nil || got != tt.want || len(args) < 2 || args[0] != "taken" || args[1] != "new" {
			t.Errorf("RenderUpdateSelected of %s = %s, %v, %v; want %s", tt.name, got, args, err, tt.want)
		}
	}
	for _, tt := range []struct {
		name string
		q    impl.Of[int]
		key  []string
		lock string
		want string
	}{
		{"a page of every row", impl.Of[int]{}.Limit(1), key, "",
			"orm: Update would update every row of jobs: give it a condition"},
		{"an order alone", base.OrderBy("id", false), key, "", "orm: Update ordering requires Limit or Offset"},
		{"an order alone under a lock", base.OrderBy("id", false), key, lock,
			"orm: Update ordering requires Limit or Offset"},
		{"a page with no key", page, nil, "", "orm: paged Update requires a primary or non-null unique key"},
		{"a lock with no key", base, nil, lock, "orm: locked Update requires a primary or non-null unique key"},
		{"a relation", impl.Preload(page, "Lines", impl.Of[int]{}), key, "", "orm: Update cannot load relations"},
	} {
		_, _, err := changes.RenderUpdateSelected(impl.Postgres, "jobs", []string{"status"}, tt.key, tt.lock, tt.q)
		if err == nil || err.Error() != tt.want {
			t.Errorf("RenderUpdateSelected of %s error = %v, want %q", tt.name, err, tt.want)
		}
	}
}

func TestChanges_Add(t *testing.T) {
	t.Parallel()

	q := impl.Of[int]{}.Where("id = ", 7)
	page := impl.Of[int]{}.Where("status = ", "new").OrderBy("queued_at", false).Limit(2)
	for _, tt := range []struct {
		name    string
		d       impl.Dialect
		changes impl.Changes[int]
		render  func(c impl.Changes[int], d impl.Dialect) (string, []any, error)
		want    string
		args    []any
	}{
		{"by key", impl.Postgres, impl.Changes[int]{}.Add("attempts", 1).Set("status", "taken"),
			func(c impl.Changes[int], d impl.Dialect) (string, []any, error) {
				return c.RenderUpdate(d, "jobs", []string{"attempts", "status"}, q)
			}, `UPDATE "jobs" SET "attempts" = "attempts" + $1, "status" = $2 WHERE (id = $3)`, []any{1, "taken", 7}},
		{"by key on MySQL", impl.MySQL, impl.Changes[int]{}.Add("attempts", 1).Set("status", "taken"),
			func(c impl.Changes[int], d impl.Dialect) (string, []any, error) {
				return c.RenderUpdate(d, "jobs", []string{"attempts", "status"}, q)
			}, "UPDATE `jobs` SET `attempts` = `attempts` + ?, `status` = ? WHERE (id = ?)", []any{1, "taken", 7}},
		{"a page", impl.Postgres, impl.Changes[int]{}.Add("attempts", -1),
			func(c impl.Changes[int], d impl.Dialect) (string, []any, error) {
				return c.RenderUpdateSelected(d, "jobs", []string{"attempts"}, []string{"id"}, "", page)
			}, `UPDATE "jobs" SET "attempts" = "attempts" + $1 WHERE "id" IN ` +
				`(SELECT "id" FROM "jobs" WHERE (status = $2) ORDER BY queued_at, id LIMIT 2)`, []any{-1, "new"}},
		{"a page on MySQL", impl.MySQL, impl.Changes[int]{}.Add("attempts", -1),
			func(c impl.Changes[int], d impl.Dialect) (string, []any, error) {
				return c.RenderUpdateSelected(d, "jobs", []string{"attempts"}, []string{"id"}, "", page)
			}, "UPDATE `jobs` SET `attempts` = `attempts` + ? WHERE `id` IN (SELECT `id` FROM " +
				"(SELECT `id` FROM `jobs` WHERE (status = ?) ORDER BY queued_at, id LIMIT 2) AS `orm_update_targets`)",
			[]any{-1, "new"}},
		{"Set then Add", impl.Postgres, impl.Changes[int]{}.Set("attempts", 5).Add("attempts", 1),
			func(c impl.Changes[int], d impl.Dialect) (string, []any, error) {
				return c.RenderUpdate(d, "jobs", []string{"attempts"}, q)
			}, `UPDATE "jobs" SET "attempts" = "attempts" + $1 WHERE (id = $2)`, []any{1, 7}},
		{"Add then Set", impl.MySQL, impl.Changes[int]{}.Add("attempts", 1).Set("attempts", 5),
			func(c impl.Changes[int], d impl.Dialect) (string, []any, error) {
				return c.RenderUpdate(d, "jobs", []string{"attempts"}, q)
			}, "UPDATE `jobs` SET `attempts` = ? WHERE (id = ?)", []any{5, 7}},
		{"Add then Add", impl.Postgres, impl.Changes[int]{}.Add("attempts", 1).Add("attempts", 2),
			func(c impl.Changes[int], d impl.Dialect) (string, []any, error) {
				return c.RenderUpdate(d, "jobs", []string{"attempts"}, q)
			}, `UPDATE "jobs" SET "attempts" = "attempts" + $1 WHERE (id = $2)`, []any{2, 7}},
	} {
		got, args, err := tt.render(tt.changes, tt.d)
		if err != nil || got != tt.want || !reflect.DeepEqual(args, tt.args) {
			t.Errorf("Update of %s = %s, %v, %v; want %s, %v", tt.name, got, args, err, tt.want, tt.args)
		}
	}

	base := impl.Changes[int]{}.Set("attempts", 5)
	_ = base.Add("attempts", 1)
	if sql, args, err := base.RenderUpdate(impl.Postgres, "jobs", []string{"attempts"}, q); err != nil ||
		strings.Contains(sql, "+") || !reflect.DeepEqual(args, []any{5, 7}) {
		t.Errorf("Add changed the change set it derives from: %s, %v, %v", sql, args, err)
	}
	if _, _, err := (impl.Changes[int]{}).Add("name", 1).RenderUpdate(impl.Postgres, "jobs", []string{"attempts"},
		q); err == nil || err.Error() != "orm: Update cannot assign column name" {
		t.Errorf("Add of a column not allowed error = %v", err)
	}
}

func TestChanges_UpsertValues(t *testing.T) {
	t.Parallel()

	allowed, added, key := []string{"name", "visits", "phone"}, []string{"visits"}, []string{"email"}
	for _, tt := range []struct {
		name    string
		changes impl.Changes[int]
		want    []any
	}{
		{"no change", impl.Changes[int]{}, []any{false, nil, false, nil, false, nil, false, nil}},
		{"Set", impl.Changes[int]{}.Set("phone", nil).Set("visits", 3),
			[]any{false, nil, true, 3, false, nil, true, nil}},
		{"Add", impl.Changes[int]{}.Add("visits", 1), []any{false, nil, false, nil, true, 1, false, nil}},
		{"Set then Add", impl.Changes[int]{}.Set("visits", 3).Add("visits", 1),
			[]any{false, nil, false, nil, true, 1, false, nil}},
		{"Add then Set", impl.Changes[int]{}.Add("visits", 1).Set("visits", 3),
			[]any{false, nil, true, 3, false, nil, false, nil}},
	} {
		got, err := tt.changes.UpsertValues("Upsert", allowed, added, key)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("UpsertValues of %s = %v, %v, want %v", tt.name, got, err, tt.want)
		}
	}
	for _, tt := range []struct {
		name    string
		changes impl.Changes[int]
		want    string
	}{
		{"the key", impl.Changes[int]{}.Set("email", 1),
			"orm: Upsert meets its row by email, which its change set cannot write"},
		{"the key added to", impl.Changes[int]{}.Add("email", 1),
			"orm: Upsert meets its row by email, which its change set cannot write"},
		{"a column not allowed", impl.Changes[int]{}.Set("created_at", 1),
			"orm: Upsert cannot assign column created_at"},
		{"a column not added to", impl.Changes[int]{}.Add("name", 1), "orm: Upsert cannot add to column name"},
	} {
		if _, err := tt.changes.UpsertValues("Upsert", allowed, added, key); err == nil || err.Error() != tt.want {
			t.Errorf("UpsertValues of %s error = %v, want %q", tt.name, err, tt.want)
		}
	}
}

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

func TestChanges_UpsertValues(t *testing.T) {
	t.Parallel()

	allowed, key := []string{"name", "phone"}, []string{"email"}
	got, err := impl.Changes[int]{}.Set("phone", nil).UpsertValues("Upsert", allowed, key)
	if want := []any{false, nil, true, nil}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("UpsertValues = %v, %v, want %v", got, err, want)
	}
	if got, err := (impl.Changes[int]{}).UpsertValues("Upsert", allowed, key); err != nil ||
		!reflect.DeepEqual(got, []any{false, nil, false, nil}) {
		t.Errorf("UpsertValues of no change = %v, %v", got, err)
	}
	for column, want := range map[string]string{
		"email":      "orm: Upsert meets its row by email, which its change set cannot write",
		"created_at": "orm: Upsert cannot assign column created_at",
	} {
		if _, err := (impl.Changes[int]{}).Set(column, 1).UpsertValues("Upsert", allowed, key); err == nil ||
			err.Error() != want {
			t.Errorf("UpsertValues of %s error = %v, want %q", column, err, want)
		}
	}
}

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

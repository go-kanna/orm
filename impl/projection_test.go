package impl_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/orm/impl"
)

const (
	membershipSelected = `SELECT memberships.id, "user".display_name`
	membershipJoins    = ` INNER JOIN users AS "user" ON "user".id = memberships.user_id`
)

var membershipColumns = []string{"id", "user_id", "version"}

func TestRenderJoinedProjection(t *testing.T) {
	t.Parallel()
	for _, d := range []impl.Dialect{impl.Postgres, impl.MySQL} {
		q := impl.Of[int]{}.Where("version > ", int64(7)).OrderBy("id", false).Limit(2)
		text, args, reversed, err := q.RenderJoinedProjection(d, "memberships", membershipSelected, membershipJoins,
			[]string{"id", "user_id"}, membershipColumns, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := `SELECT memberships.id, "user".display_name FROM ` +
			`(SELECT "id", "user_id" FROM "memberships" WHERE (version > $1)) AS "memberships"` + membershipJoins +
			` ORDER BY "memberships"."id" LIMIT 2`
		if d == impl.MySQL {
			want = `SELECT memberships.id, "user".display_name FROM ` +
				"(SELECT `id`, `user_id` FROM `memberships` WHERE (version > ?)) AS `memberships`" + membershipJoins +
				" ORDER BY `memberships`.`id` LIMIT 2"
		}
		if text != want || !reflect.DeepEqual(args, []any{int64(7)}) || reversed {
			t.Fatalf("got %s, %v, reversed=%v; want %s", text, args, reversed, want)
		}
	}
}

func TestRenderJoinedProjection_WithoutConditions(t *testing.T) {
	t.Parallel()

	q := impl.Of[int]{}.OrderBy("version", true).Limit(2)
	text, _, _, err := q.RenderJoinedProjection(impl.Postgres, "memberships", membershipSelected, membershipJoins,
		[]string{"id", "user_id"}, membershipColumns, []string{"id"})
	if err != nil {
		t.Fatal(err)
	}
	want := membershipSelected + ` FROM "memberships"` + membershipJoins +
		` ORDER BY "memberships"."version" DESC, "memberships"."id" DESC LIMIT 2`
	if text != want {
		t.Errorf("got %s; want %s", text, want)
	}
}

func TestRenderJoinedProjection_Refusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		q    impl.Of[int]
		want string
	}{
		{"order expression", impl.Of[int]{}.OrderBy("random()", false), "ordered by columns of memberships alone"},
		{"relation", impl.Preload(impl.Of[int]{}, "Lines", impl.Of[int]{}), "loads relations"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, _, err := tc.q.RenderJoinedProjection(impl.Postgres, "memberships", membershipSelected,
				membershipJoins, []string{"id", "user_id"}, membershipColumns, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v; want %s", err, tc.want)
			}
		})
	}
}

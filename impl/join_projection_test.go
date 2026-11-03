package impl_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kanna/orm/impl"
	"github.com/go-kanna/orm/join"
)

var joinUsers = impl.TableJoin{
	Table: "users", ParentColumns: []string{"user_id"}, Columns: []string{"id"},
	Fields: []string{"id", "display_name"},
}

var joinedColumns = []impl.SelectColumn{
	{Table: "memberships", Column: "id"}, {Table: "users", Column: "display_name"},
}

func TestRenderJoinedProjection(t *testing.T) {
	t.Parallel()
	for _, d := range []impl.Dialect{impl.Postgres, impl.MySQL} {
		q := impl.Of[int]{}.Where("version > ", int64(7)).OrderBy("id", false).Limit(2).JoinTable("users")
		text, args, reversed, err := q.RenderProjection(d, "memberships", joinedColumns, []impl.TableJoin{joinUsers},
			[]string{"id", "user_id", "version"}, "unused", nil)
		if err != nil {
			t.Fatal(err)
		}
		want := `SELECT "memberships"."id", "users"."display_name" FROM ` +
			`(SELECT "id", "user_id" FROM "memberships" WHERE (version > $1)) AS "memberships" ` +
			`INNER JOIN "users" ON "memberships"."user_id" = "users"."id" ORDER BY "memberships"."id" LIMIT 2`
		if d == impl.MySQL {
			want = strings.ReplaceAll(strings.ReplaceAll(want, `"`, "`"), "$1", "?")
		}
		if text != want || !reflect.DeepEqual(args, []any{int64(7)}) || reversed {
			t.Fatalf("got %s, %v, reversed=%v; want %s", text, args, reversed, want)
		}
	}
}

func TestRenderJoinedProjection_EndsAnOrderInTheKey(t *testing.T) {
	t.Parallel()

	q := impl.Of[int]{}.OrderBy("version", true).Limit(2).JoinTable("users")
	text, _, _, err := q.RenderProjection(impl.Postgres, "memberships", joinedColumns, []impl.TableJoin{joinUsers},
		[]string{"id", "user_id", "version"}, "unused", []string{"id"})
	if err != nil {
		t.Fatal(err)
	}
	if want := ` ORDER BY "memberships"."version" DESC, "memberships"."id" DESC LIMIT 2`; !strings.HasSuffix(text, want) {
		t.Errorf("got %s; want it to end in %s", text, want)
	}
}

func TestJoinedProjectionRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		q       impl.Of[int]
		columns []impl.SelectColumn
		joins   []impl.TableJoin
		want    string
	}{
		{"missing join", impl.Of[int]{}, joinedColumns, []impl.TableJoin{joinUsers}, "requires Join"},
		{"unknown join", impl.Of[int]{}.JoinTable("absent"), joinedColumns, []impl.TableJoin{joinUsers}, "snapshot"},
		{"ambiguous key", impl.Of[int]{}.JoinTable("users"), joinedColumns,
			[]impl.TableJoin{joinUsers, joinUsers}, "foreign key"},
		{"order expression", impl.Of[int]{}.JoinTable("users").OrderBy("random()", false),
			joinedColumns, []impl.TableJoin{joinUsers}, "base column"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, _, err := tc.q.RenderProjection(impl.Postgres, "memberships", tc.columns, tc.joins,
				[]string{"id"}, "unused", nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v; want %s", err, tc.want)
			}
		})
	}
}

func TestJoinScopesRemainImmutableAndRequireProjections(t *testing.T) {
	t.Parallel()
	base := impl.Of[int]{}.Where("id = ", 1)
	joined := base.JoinTable("users")
	if _, err := base.Render(impl.Postgres, 1, nil); err != nil {
		t.Fatal("joining modified the original scope", err)
	}
	if _, err := joined.Render(impl.Postgres, 1, nil); err == nil {
		t.Fatal("ordinary reads silently ignored a join")
	}
	if err := joined.ConditionsOnly("Count"); err == nil {
		t.Fatal("condition-only operations accepted joins")
	}
	if _, _, err := joined.RenderDelete(impl.Postgres, "memberships", []string{"id"}); err == nil {
		t.Fatal("deletion accepted a join")
	}
	combined := impl.Combine(joined, joined)
	text, _, _, err := combined.RenderProjection(impl.Postgres, "memberships", joinedColumns,
		[]impl.TableJoin{joinUsers}, []string{"id"}, "unused", nil)
	if err != nil || strings.Count(text, " INNER JOIN ") != 1 {
		t.Fatalf("duplicate joined scopes: %s, %v", text, err)
	}
}

func TestExplicitJoinConditions(t *testing.T) {
	t.Parallel()
	metadata := []impl.TableJoin{{Table: "users", Fields: []string{"id", "tenant_id", "display_name"}}}
	for _, d := range []impl.Dialect{impl.Postgres, impl.MySQL} {
		conditions := []join.Condition{join.On("user_id", "id"), join.On("tenant_id", "tenant_id")}
		q := impl.Of[int]{}.Where("version > ", 7).JoinTable("users", conditions...)
		conditions[0] = join.On("missing", "id")
		q = impl.Combine(q, impl.Of[int]{}.JoinTable("users", join.On("user_id", "id"), join.On("tenant_id", "tenant_id")))
		text, args, _, err := q.RenderProjection(d, "memberships", joinedColumns, metadata,
			[]string{"id", "user_id", "tenant_id", "version"}, "unused", nil)
		want := `SELECT "memberships"."id", "users"."display_name" FROM ` +
			`(SELECT "id", "user_id", "tenant_id" FROM "memberships" WHERE (version > $1)) AS "memberships" ` +
			`INNER JOIN "users" ON "memberships"."user_id" = "users"."id" AND "memberships"."tenant_id" = "users"."tenant_id"`
		if d == impl.MySQL {
			want = strings.ReplaceAll(strings.ReplaceAll(want, `"`, "`"), "$1", "?")
		}
		if err != nil || text != want || !reflect.DeepEqual(args, []any{7}) {
			t.Fatalf("explicit join: %s, %v, %v; want %s", text, args, err, want)
		}
	}
}

func TestExplicitJoinRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		q    impl.Of[int]
		want string
	}{
		{"no foreign key", impl.Of[int]{}.JoinTable("users"), "foreign key"},
		{"unknown table", impl.Of[int]{}.JoinTable("absent", join.On("user_id", "id")), "snapshot"},
		{"unknown base column", impl.Of[int]{}.JoinTable("users", join.On("missing", "id")), "snapshot columns"},
		{"unknown joined column", impl.Of[int]{}.JoinTable("users", join.On("user_id", "missing")), "snapshot columns"},
		{"expression", impl.Of[int]{}.JoinTable("users", join.On("user_id", "id OR true")), "snapshot columns"},
		{"self join", impl.Of[int]{}.JoinTable("memberships", join.On("user_id", "id")), "different table"},
		{"conflicting join", impl.Of[int]{}.JoinTable("users", join.On("user_id", "id")).JoinTable("users"), "conflicting"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, _, err := tc.q.RenderProjection(impl.Postgres, "memberships", joinedColumns,
				[]impl.TableJoin{{Table: "users", Fields: []string{"id", "display_name"}}}, []string{"id", "user_id"}, "unused",
				nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v; want %s", err, tc.want)
			}
		})
	}
}

func TestExplicitJoinResolvesAmbiguousForeignKeys(t *testing.T) {
	t.Parallel()
	second := joinUsers
	second.ParentColumns = []string{"creator_id"}
	q := impl.Of[int]{}.JoinTable("users", join.On("user_id", "id"))
	text, _, _, err := q.RenderProjection(impl.Postgres, "memberships", joinedColumns,
		[]impl.TableJoin{joinUsers, second}, []string{"id", "user_id", "creator_id"}, "unused", nil)
	if err != nil || !strings.Contains(text, `ON "memberships"."user_id" = "users"."id"`) {
		t.Fatalf("explicit foreign key choice: %s, %v", text, err)
	}
}

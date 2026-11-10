package vet

import (
	"go/constant"
	"testing"
)

func strVal(s string) value { return constVal{constant.MakeString(s)} }

func strList(ss ...string) value {
	l := listVal{elems: []value{}}
	for _, s := range ss {
		l.elems = append(l.elems, strVal(s))
	}
	return l
}

// TestChecks covers the checks the examples have no table or column for.
func TestChecks(t *testing.T) {
	t.Parallel()

	at := cause{pos: 1, in: "Purge", method: "Limit"}
	paged := emptyScope().then(&scopeVal{conds: set(at), limit: set(at)})
	named := emptyScope().then(&scopeVal{conds: set(at)})
	ordered := emptyScope().then(&scopeVal{conds: set(at), orders: set(at)})
	assigns := (&changeVal{}).assign("created_at", cause{pos: 2, in: "Touch", method: "SetCreatedAt"})
	names := (&changeVal{}).assign("name", cause{pos: 2, in: "Rename", method: "SetName"})
	adds, _ := ops["impl.Changes.Add"](opCall{args: []value{&changeVal{}, strVal("visits")},
		at: cause{pos: 2, in: "Visit", method: "AddVisits"}})
	for _, tt := range []struct {
		name  string
		check func(opCall) *failed
		args  []value
		want  string
	}{
		{"paged Delete without a key", renderDelete,
			[]value{paged, unknown{}, strVal("logs"), strList()},
			"paged Delete requires a primary or non-null unique key"},
		{"paged Delete with a key", renderDelete,
			[]value{paged, unknown{}, strVal("logs"), strList("id")}, ""},
		{"Update of a column it cannot write", renderUpdate,
			[]value{assigns, unknown{}, strVal("logs"), strList("name"), named},
			"Update cannot assign column created_at"},
		{"Update by key of a page", renderUpdate,
			[]value{names, unknown{}, strVal("logs"), strList("name"), paged},
			"Update takes conditions alone, but its scope keeps only some of the rows"},
		{"Update of a page selected by a key", renderUpdateSelected,
			[]value{names, unknown{}, strVal("logs"), strList("name"), strList("id"), strVal(""), paged}, ""},
		{"Update of a page with no key to select it by", renderUpdateSelected,
			[]value{names, unknown{}, strVal("logs"), strList("name"), strList(), strVal(""), paged},
			"paged Update requires a primary or non-null unique key"},
		{"Update ordered without a page", renderUpdateSelected,
			[]value{names, unknown{}, strVal("logs"), strList("name"), strList("id"), strVal(""), ordered},
			"Update ordering requires Limit or Offset"},
		{"Update selected of a column it cannot write", renderUpdateSelected,
			[]value{assigns, unknown{}, strVal("logs"), strList("name"), strList("id"), strVal(""), paged},
			"Update cannot assign column created_at"},
		{"Upsert of a column it cannot write", upsertValues,
			[]value{assigns, strVal("UpsertByName"), strList("name"), strList(), strList("id")},
			"UpsertByName cannot assign column created_at"},
		{"Upsert of a column it can write", upsertValues,
			[]value{assigns, strVal("UpsertByName"), strList("created_at"), strList(), strList("id")}, ""},
		{"Upsert of its key", upsertValues,
			[]value{names, strVal("UpsertByName"), strList("created_at"), strList(), strList("name")},
			"UpsertByName meets its row by name, which its change set cannot write"},
		{"Update adding to a column it cannot write", renderUpdate,
			[]value{adds, unknown{}, strVal("logs"), strList("name"), named}, "Update cannot assign column visits"},
		{"Upsert adding to its key", upsertValues,
			[]value{adds, strVal("UpsertByVisits"), strList("name"), strList(), strList("visits")},
			"UpsertByVisits meets its row by visits, which its change set cannot write"},
	} {
		got := ""
		if f := tt.check(opCall{args: tt.args}); f != nil {
			got = f.msg
		}
		if got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestPaths covers how what holds on some paths alone joins what holds on every one.
func TestPaths(t *testing.T) {
	t.Parallel()

	ordered := emptyScope().then(&scopeVal{orders: set(cause{pos: 1})})
	paged := emptyScope().then(&scopeVal{limit: set(cause{pos: 2})})
	if f := conditionsOnly(ordered.join(paged), "Count"); f != nil {
		t.Errorf("an order on one path and a limit on the other = %q, want nothing, since neither is on both", f.msg)
	}
	if f := conditionsOnly(ordered.join(ordered.then(paged)), "Count"); f == nil || f.at.pos != 1 {
		t.Errorf("an order on both paths = %+v, want the order", f)
	}
	if got := ordered.join(unknownScope()).orders.t; got != some {
		t.Errorf("an order joined with what vet does not know = %d, want %d", got, some)
	}
	limited := emptyScope().then(&scopeVal{err: setErr("a limit of -1 rows", cause{pos: 3})})
	if got := limited.err.then(errProp{}); got.msg != "a limit of -1 rows" {
		t.Errorf("an error with nothing added = %+v, want it kept", got)
	}
}

// Raw's Where may make an error of the scope, by parts vet does not read, and keeps one the scope holds already.
func TestRawWhere(t *testing.T) {
	t.Parallel()

	at := cause{pos: 1, in: "Purge", method: "Limit"}
	for _, tt := range []struct {
		name  string
		scope *scopeVal
		want  errProp
	}{
		{"of a scope without an error", emptyScope(), errProp{t: some}},
		{"of a scope with one", &scopeVal{err: setErr("a limit of -1 rows", at)}, setErr("a limit of -1 rows", at)},
	} {
		v, _ := ops["scope.Raw.Where"](opCall{args: []value{tt.scope, unknown{}}, at: at})
		s := v.(*scopeVal) //nolint:forcetypeassert // Where returns a scope
		if s.err != tt.want || s.conds.t != every {
			t.Errorf("Where %s = %+v, %+v; want %+v and a condition", tt.name, s.err, s.conds, tt.want)
		}
	}
}

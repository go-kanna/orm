package gen

import (
	"fmt"
	"regexp"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/directive"

	"github.com/go-kanna/orm/internal/snapshot"
)

// lockClauses are the locking clauses of the strengths //kanna:lock takes.
var lockClauses = map[string]string{
	"update":        "FOR UPDATE",
	"no-key-update": "FOR NO KEY UPDATE",
	"share":         "FOR SHARE",
	"key-share":     "FOR KEY SHARE",
}

// lockWaits are what //kanna:lock takes after a strength, and what each adds to its clause.
var lockWaits = map[string]string{"nowait": "NOWAIT", "skip-locked": "SKIP LOCKED"}

// lockWords are the words //kanna:lock takes: a strength, then nowait or skip-locked if any.
var lockWords = []string{"update", "no-key-update", "share", "key-share", "nowait", "skip-locked"}

// methodDiags are ds, about a directive of the method named method, saying so.
func methodDiags(method string, ds []diag.Diag) []diag.Diag {
	for i := range ds {
		ds[i].Message = method + ": " + ds[i].Message
	}
	return ds
}

// hinted gives each of ds hints.
func hinted(ds []diag.Diag, hints ...string) []diag.Diag {
	for i := range ds {
		ds[i] = ds[i].WithHints(hints...)
	}
	return ds
}

// lockedName matches a name that ends in the strength of a lock, as GetForUpdate does, and not one in which more
// follows, as GetForUpdateForm, which may well read rows for an update to come.
var lockedName = regexp.MustCompile(`For(Update|NoKeyUpdate|Share|KeyShare)$`)

// namedStrengths are the strengths of //kanna:lock by the words a name ends in.
var namedStrengths = map[string]string{
	"Update": "update", "NoKeyUpdate": "no-key-update", "Share": "share", "KeyShare": "key-share",
}

// unlockedProjection reports why p is read without a lock, by errorf, if it is: it holds aggregates of groups of rows,
// or joins a table, whose rows a lock on those of p's table would leave as they are.
func unlockedProjection(p readProjection, errorf func(string, ...any) diag.Diag) (diag.Diag, bool) {
	switch {
	case len(p.aggregates) > 0:
		return errorf("a projection of aggregates is read of groups of rows, which hold no lock").
			WithHints("lock the rows by reading the model"), false
	case len(p.joins) > 0:
		return errorf("%s joins a table, and a projection joining one is read without a lock", p.joins[0].field).
			WithHints("lock the rows by reading the model, and read the values joined apart",
				"or write the query by hand, locking the rows of the tables meant, as FOR UPDATE OF does"), false
	}
	return diag.Diag{}, true
}

// lock reads the //kanna:lock of m into its locking clause. A method that reads rows and whose name ends in the
// strength of a lock but has none is warned about, as it would be generated without one; one whose //kanna:lock is of
// another strength than its name ends in stops generation, as one of the two is wrong. An Update of a change set that
// can take a page locks the rows it selects in the subquery that selects them by key, whether a scope pages them or
// not, which is not known before it runs.
func (r repoReader) lock(m *repoMethod, d directive.Directive) []diag.Diag {
	reads := m.verb == verbGet || m.verb == verbFind || m.verb == verbFirst || m.verb == verbList
	updates := m.selects() && m.scoped
	if !d.Found {
		if reads && lockedName.MatchString(m.name) {
			return []diag.Diag{diag.Warningf(m.pos, "%s: its name tells of a lock, but it has no //kanna:lock", m.name).
				WithHints("declare the lock, or name it otherwise")}
		}
		return nil
	}

	errorf := func(format string, args ...any) diag.Diag {
		return diag.Errorf(d.Pos, "%s: %s", m.name, fmt.Sprintf(format, args...))
	}
	if !reads && !updates {
		return []diag.Diag{errorf("//kanna:lock is for a Get, a Find, a First, a List, or an Update of a change set " +
			"taking scopes and no unique key")}
	}
	if updates && r.s.Dialect == snapshot.MySQL {
		return []diag.Diag{errorf("MySQL may still wait for the rows a locked subquery passes over, as its UPDATE can " +
			"read more of the table than the rows it writes").WithHints(
			"lock the rows with a List in a transaction, then update them by their keys")}
	}
	if updates && len(targetKey(m.table)) == 0 {
		return []diag.Diag{errorf("%s has no primary key or non-null unique key to select the rows to lock by",
			m.target.table)}
	}
	if m.projection != nil {
		if d, ok := unlockedProjection(*m.projection, errorf); !ok {
			return []diag.Diag{d}
		}
	}
	args, ds := d.Parse(directive.Spec{Words: lockWords, Needed: true})
	if len(ds) > 0 {
		return hinted(methodDiags(m.name, ds), "as in //kanna:lock update skip-locked")
	}
	_, strong := lockClauses[args[0].Name]
	if !strong || len(args) > 2 || len(args) == 2 && lockWaits[args[1].Name] == "" {
		return []diag.Diag{errorf("//kanna:lock takes the strength of the lock, then nowait or skip-locked if any").
			WithHints("as in //kanna:lock update skip-locked")}
	}
	strength := args[0].Name
	if named := lockedName.FindStringSubmatch(m.name); named != nil && namedStrengths[named[1]] != strength {
		return []diag.Diag{errorf("its name ends in a lock of %s, but //kanna:lock is of %s",
			namedStrengths[named[1]], strength).WithHints("make the two one")}
	}
	clause := lockClauses[strength]
	if r.s.Dialect == snapshot.MySQL && (strength == "no-key-update" || strength == "key-share") {
		return []diag.Diag{errorf("MySQL has no %s", clause).WithHints("lock with update or share")}
	}
	if len(args) == 2 {
		if args[1].Name == "skip-locked" && (m.verb == verbGet || m.verb == verbFind) {
			return []diag.Diag{errorf("skip-locked passes a locked row over, which a %s would report as not found", m.verb).
				WithHints("use nowait, which returns an error for a locked row")}
		}
		clause += " " + lockWaits[args[1].Name]
	}
	m.lock = clause
	return nil
}

package gen

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/directive"
	"github.com/go-kanna/internal/suggest"

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

// lockedName matches a name that ends in the strength of a lock, as GetForUpdate does, and not one in which more
// follows, as GetForUpdateForm, which may well read rows for an update to come.
var lockedName = regexp.MustCompile(`For(Update|NoKeyUpdate|Share|KeyShare)$`)

// namedStrengths are the strengths of //kanna:lock by the words a name ends in.
var namedStrengths = map[string]string{
	"Update": "update", "NoKeyUpdate": "no-key-update", "Share": "share", "KeyShare": "key-share",
}

// lock reads the //kanna:lock of m into its locking clause. A method that reads rows and whose name ends in the
// strength of a lock but has none is warned about, as it would be generated without one; one whose //kanna:lock is of
// another strength than its name ends in stops generation, as one of the two is wrong.
func (r repoReader) lock(m *repoMethod, d directive.Directive) []diag.Diag {
	reads := m.verb == verbGet || m.verb == verbFind || m.verb == verbFirst || m.verb == verbList
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
	if !reads {
		return []diag.Diag{errorf("//kanna:lock is for a Get, a Find, a First, or a List")}
	}
	fields := strings.Fields(d.Args)
	if len(fields) == 0 || len(fields) > 2 {
		return []diag.Diag{errorf("//kanna:lock takes the strength of the lock, then nowait or skip-locked if any").
			WithHints("as in //kanna:lock update skip-locked")}
	}
	strength := fields[0]
	if named := lockedName.FindStringSubmatch(m.name); named != nil && namedStrengths[named[1]] != strength {
		return []diag.Diag{errorf("its name ends in a lock of %s, but //kanna:lock is of %s",
			namedStrengths[named[1]], strength).WithHints("make the two one")}
	}
	clause, ok := lockClauses[strength]
	if !ok {
		unknown := errorf("//kanna:lock takes update, no-key-update, share, or key-share, not %q", strength)
		if near, ok := suggest.Closest(strength, slices.Sorted(maps.Keys(lockClauses))); ok {
			unknown = unknown.WithHints(fmt.Sprintf("did you mean %s?", near))
		}
		return []diag.Diag{unknown}
	}
	if r.s.Dialect == snapshot.MySQL && (strength == "no-key-update" || strength == "key-share") {
		return []diag.Diag{errorf("MySQL has no %s", clause).WithHints("lock with update or share")}
	}
	if len(fields) == 2 {
		wait, ok := lockWaits[fields[1]]
		if !ok {
			return []diag.Diag{errorf("//kanna:lock takes nowait or skip-locked after the strength, not %q", fields[1])}
		}
		if fields[1] == "skip-locked" && (m.verb == verbGet || m.verb == verbFind) {
			return []diag.Diag{errorf("skip-locked passes a locked row over, which a %s would report as not found", m.verb).
				WithHints("use nowait, which returns an error for a locked row")}
		}
		clause += " " + wait
	}
	m.lock = clause
	return nil
}

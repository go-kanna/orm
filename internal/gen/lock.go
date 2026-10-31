package gen

import (
	"fmt"
	"go/ast"
	"go/token"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/directive"
	"github.com/go-kanna/internal/suggest"

	"github.com/go-kanna/orm/internal/decl"
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

// lockedName matches a name telling of a lock, but not one like ListForSharedFolders.
var lockedName = regexp.MustCompile(`For(Update|NoKeyUpdate|Share|KeyShare)($|[A-Z0-9])`)

// lock reads the //kanna:lock of m into its locking clause. A method that reads rows and whose name tells of a lock
// but has none is warned about, as it would be generated without one.
func (r repoReader) lock(m *repoMethod, d directive.Directive) []diag.Diag {
	reads := m.verb == verbGet || m.verb == verbFirst || m.verb == verbList
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
		return []diag.Diag{errorf("//kanna:lock is for a Get, a First, or a List")}
	}
	fields := strings.Fields(d.Args)
	if len(fields) == 0 || len(fields) > 2 {
		return []diag.Diag{errorf("//kanna:lock takes the strength of the lock, then nowait or skip-locked if any").
			WithHints("as in //kanna:lock update skip-locked")}
	}
	strength := fields[0]
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
		if fields[1] == "skip-locked" && m.verb == verbGet {
			return []diag.Diag{errorf("skip-locked passes a locked row over, which a Get would report as not found").
				WithHints("use nowait, which returns an error for a locked row")}
		}
		clause += " " + wait
	}
	m.lock = clause
	return nil
}

// hasNoGeneratedSQL is the error for a //kanna:lock on a method named name, whose SQL is written by hand in where.
func hasNoGeneratedSQL(d directive.Directive, name, where string) diag.Diag {
	return diag.Errorf(d.Pos, "%s: //kanna:lock is for a method kanna-orm writes the SQL of", name).
		WithHints("lock in " + where)
}

// strayLocks reports the //kanna:lock directives of file on no method of repos, such as one at the end of a line or
// parted from a method by a blank line. Nothing reads them, so the method would go unlocked without a word.
func strayLocks(fset *token.FileSet, file *ast.File, repos []decl.Repository) []diag.Diag {
	docs := map[*ast.CommentGroup]bool{}
	for _, repo := range repos {
		it, ok := repo.Spec.Type.(*ast.InterfaceType)
		if !ok {
			continue
		}
		for _, f := range it.Methods.List {
			if len(f.Names) > 0 {
				docs[f.Doc] = true
			}
		}
	}

	var ds []diag.Diag
	for _, cg := range file.Comments {
		if docs[cg] {
			continue
		}
		d, fds := directive.Find(fset, cg, "lock")
		ds = append(ds, fds...)
		if d.Found {
			ds = append(ds, diag.Errorf(d.Pos, "//kanna:lock marks no method of a //kanna:repository").
				WithHints("write it right above the method"))
		}
	}
	return ds
}

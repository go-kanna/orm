// Package vet is kanna-orm vet, which finds the scopes given to a repository that make it fail on every path, as
// the runtime would report them.
package vet

import (
	"go/token"
	"slices"

	"github.com/go-kanna/internal/cli"
	"github.com/go-kanna/internal/diag"
)

// Command returns kanna-orm vet.
func Command() cli.Vet {
	return cli.Vet{
		Name:    "kanna-orm",
		Summary: "find the scopes given to a repository that make it fail on every path",
		Run:     Run,
	}
}

// Run checks the packages patterns match in dir, and returns what it finds, the files named as from dir.
func Run(dir, tags string, patterns []string) []diag.Diag {
	l, ds := load(dir, tags, patterns)
	if diag.HasErrors(ds) {
		return ds
	}
	a := newAnalysis(l.prog, l.fset, l.files)
	a.findRepositories(l.types)
	a.findBindings()
	for _, fn := range l.functions {
		a.check(fn)
		a.checkBinds(fn)
	}
	return reports(a, dir)
}

func reports(a *analysis, dir string) []diag.Diag {
	slices.SortFunc(a.reports, func(x, y report) int { return int(x.pos - y.pos) })
	var (
		ds   []diag.Diag
		seen = map[token.Pos]bool{}
	)
	for _, r := range a.reports {
		if seen[r.pos] {
			continue
		}
		seen[r.pos] = true
		pos := a.fset.Position(r.pos)
		d := diag.Errorf(pos, "%s", r.fail.msg)
		if at := r.fail.at; at.pos.IsValid() && r.fail.hint != "" {
			p := a.fset.Position(at.pos)
			if p.Filename != pos.Filename || p.Line != pos.Line {
				p.Filename = diag.ShortPath(dir, p.Filename)
				d = d.WithHints(p.String() + ": " + describe(at, r.fail.hint, r.in))
			}
		}
		ds = append(ds, d)
	}
	return ds
}

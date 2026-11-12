package gen

import (
	"errors"
	"maps"
	"path/filepath"
	"slices"

	"github.com/go-kanna/internal/deps"

	"github.com/go-kanna/orm/internal/snapshot"
)

// reportDeps tells what a run in dir reads and writes. A scope type reads its model,
// which the package declaring it need not import. Where the run writes follows
// from the directives and -destination alone, so it is told even when the models
// do not resolve yet or the snapshot is not there, which the kanna command
// reports as an input that is missing. So are the names of the implementations,
// which the interfaces fix; those of the scope types wait for the snapshot.
func reportDeps(dir string, cfg config, patterns []string) (deps.Report, error) {
	dest, d := readDestination(dir, cfg)
	if d != nil {
		return deps.Report{}, errors.New(d.String())
	}
	load := patterns
	if dest != nil && hasCode(dest.dir) {
		load = append(slices.Clone(load), dest.dir)
		if len(patterns) == 0 {
			load = []string{".", dest.dir}
		}
	}
	l, d := loadRun(dir, cfg, load, nil)
	if d != nil {
		return deps.Report{}, errors.New(d.String())
	}

	r := deps.Report{Packages: l.set.Paths()}
	if len(l.scopePkgs) == 0 && len(l.repoPkgs) == 0 {
		return r, nil
	}
	path, err := snapshot.Path(dir, cfg.snapshot)
	if err != nil {
		return r, nil //nolint:nilerr // the run reports it
	}
	r.Inputs = []string{path}
	for _, pkg := range l.roots {
		if slices.Contains(l.scopePkgs, pkg) && len(pkg.GoFiles) > 0 {
			r.Writes = append(r.Writes, filepath.Join(filepath.Dir(pkg.GoFiles[0]), scopesOutputName))
		}
	}
	repos := map[string]bool{}
	if len(l.repoPkgs) > 0 && dest != nil {
		r.Writes = append(r.Writes, filepath.Join(dest.dir, repositoryOutputName))
		for _, pkg := range l.repoPkgs {
			maps.Copy(repos, repositoryNames(pkg, dest.pkgPath))
		}
	}

	p, stop := planRun(dir, cfg, load, dest, l)
	if stop != nil {
		r.Declares = slices.Collect(maps.Keys(repos))
		return r, nil // which the run reports
	}

	for _, e := range p.idx {
		if slices.Contains(r.Packages, e.pkgPath) {
			r.Refs = append(r.Refs, deps.Ref{Package: e.modelPath, Via: "the scope type " + e.name})
		}
	}
	declares := generatedNames(p.plans, p.s.Dialect)
	maps.Copy(declares, repos)
	r.Declares = slices.Collect(maps.Keys(declares))
	return r, nil
}

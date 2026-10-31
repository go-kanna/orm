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
// which the package declaring it need not import. A snapshot that is not there
// yet leaves what the run generates unknown, for the kanna command to report it
// as an input that is missing.
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
	l, d := loadRun(dir, cfg, load, dest, nil)
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

	p, stop := planRun(dir, cfg, load, dest, l)
	if stop != nil {
		return r, nil // which the run reports
	}

	for _, e := range p.idx {
		if slices.Contains(r.Packages, e.pkgPath) {
			r.Refs = append(r.Refs, deps.Ref{Package: e.modelPath, Via: "the scope type " + e.name})
		}
	}
	for _, pkg := range p.roots {
		if len(p.plans[pkg]) > 0 {
			r.Writes = append(r.Writes, filepath.Join(filepath.Dir(pkg.GoFiles[0]), outputName))
		}
	}
	declares := generatedNames(p.plans, p.s.Dialect)
	if len(p.repo.repos) > 0 {
		r.Writes = append(r.Writes, filepath.Join(dest.dir, outputName))
		for _, pkg := range p.repoPkgs {
			maps.Copy(declares, repositoryNames(pkg, dest.pkgPath))
		}
	}
	r.Declares = slices.Collect(maps.Keys(declares))
	return r, nil
}

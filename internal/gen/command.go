package gen

import (
	"errors"
	"flag"
	"go/ast"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/go-kanna/internal/cli"
	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/output"
	"github.com/go-kanna/internal/packages"

	"github.com/go-kanna/orm/internal/decl"
	"github.com/go-kanna/orm/internal/snapshot"
	"github.com/go-kanna/orm/internal/sqlfile"
)

// config holds what kanna-orm's own flags set.
type config struct {
	snapshot, tags, destination, driver string
}

// Command returns kanna-orm, which generates from the snapshot kanna-orm describe, a command of its own, takes.
func Command() cli.Command[config] {
	return cli.Command[config]{
		Name: generator,
		Summary: "write the methods of the //kanna:scope types, and the implementations of the //kanna:repository " +
			"interfaces, from the snapshot kanna-orm describe takes of a database",
		Args: "[packages]",
		Help: "The packages default to the one in the current directory, so that\n" +
			"//go:generate go tool " + generator + " needs no argument.\n\n" +
			"kanna-orm describe [flags] [packages] writes the snapshot; kanna-orm describe -h tells more.",
		Flags: func(fs *flag.FlagSet, cfg *config) {
			fs.StringVar(&cfg.destination, "destination", "", "write the implementations of the package's "+
				"//kanna:repository interfaces into `dir`; required if it has any")
			fs.StringVar(&cfg.driver, "driver", "",
				"`pgx` or database/sql (default: pgx on PostgreSQL, database/sql on MySQL)")
			fs.StringVar(&cfg.snapshot, "snapshot", "",
				"the snapshot to read (default: "+snapshot.Name+" at the root of the module)")
			fs.StringVar(&cfg.tags, "tags", "", "comma-separated build tags")
		},
		Run: run,
	}
}

func run(dir string, cfg config, patterns []string) ([]output.Change, []diag.Diag) {
	dest, d := readDestination(dir, cfg)
	if d != nil {
		return nil, []diag.Diag{*d}
	}

	// Code in the destination, such as methods of a repository written by hand, calls what the run generates, so it
	// is loaded too.
	load := patterns
	if dest != nil && hasCode(dest.dir) {
		load = append(slices.Clone(load), dest.dir)
		if len(patterns) == 0 {
			load = []string{".", dest.dir}
		}
	}
	set, err := packages.Load(load, packages.Config{Dir: dir, Tags: cfg.tags, Regenerated: []string{outputName}})
	if err != nil {
		return nil, []diag.Diag{diag.Errorf(token.Position{}, "%v", err)}
	}
	scopePkgs, repoPkgs := declarations(set.Packages)
	if d := dest.check(cfg.destination, scopePkgs, repoPkgs); d != nil {
		return nil, []diag.Diag{*d}
	}
	roots := slices.DeleteFunc(slices.Clone(set.Packages), dest.holds)

	var (
		s       snapshot.Snapshot
		idx     []indexed
		plans   = map[*packages.Package][]scopePlan{}
		repo    repoPlan
		ds      []diag.Diag
		pending map[string]bool
	)
	if len(scopePkgs) > 0 || len(repoPkgs) > 0 {
		// Without the snapshot or the index, what the run generates is unknown, and so which errors loading the
		// packages say only that it is missing: they wait for the run that can tell.
		var d *diag.Diag
		if s, d = readSnapshot(dir, cfg.snapshot); d != nil {
			return nil, []diag.Diag{*d}
		}
		if idx, err = readIndex(dir); err != nil {
			return nil, []diag.Diag{diag.Errorf(token.Position{}, "%v", err)}
		}
		// Each .sql file is read once, though both the scopes and the queries of a package may be written there.
		stmts := map[*packages.Package][]sqlfile.Statement{}
		for _, pkg := range roots {
			if slices.Contains(scopePkgs, pkg) || slices.Contains(repoPkgs, pkg) {
				var sds []diag.Diag
				stmts[pkg], sds = readSQL(pkg)
				ds = append(ds, sds...)
			}
			ps, pds := plan(pkg, s, idx, stmts[pkg])
			plans[pkg] = ps
			ds = append(ds, pds...)
		}
		pending = generatedNames(plans, s.Dialect)
		if len(repoPkgs) > 0 {
			var rds []diag.Diag
			repo, rds = planRepository(repoPkgs, set.Packages, s, idx, dest, cfg.driver, stmts[repoPkgs[0]])
			ds = append(ds, rds...)
			for _, pkg := range repoPkgs {
				if dest != nil {
					maps.Copy(pending, repositoryNames(pkg, dest.pkgPath))
				}
			}
		}
	}

	// Code calling what this run generates does not compile before it has run, nor does code calling it as the run
	// is about to change it. So a type error of a package generated for does not stop the run here: packages.Recheck
	// tells, once the output is in place, whether it remains.
	blocking, deferred := set.Split()
	if diag.HasErrors(blocking) {
		return nil, append(blocking, set.SetAsideWarnings()...)
	}
	if ds = append(blocking, ds...); diag.HasErrors(ds) {
		// What is wrong with a declaration may well be one of the type errors, but for those of a call to what the
		// run generates.
		inRoots, inScopes := set.Pending(pending), pendingInScopes(idx, roots)
		return nil, packages.Stopped(deferred, ds, func(d diag.Diag) bool { return inRoots(d) || inScopes(d) })
	}

	changes, ods := outputs(roots, plans, s.Dialect, repo, dest, len(repoPkgs) > 0)
	if ds = append(ds, ods...); diag.HasErrors(ods) {
		return nil, ds
	}
	if rds := packages.Recheck(set, changes); len(rds) > 0 {
		return nil, slices.Concat(ds, rds, set.SetAsideWarnings())
	}
	if len(scopePkgs) == 0 && len(repoPkgs) == 0 {
		ds = append(ds, nothingDeclared(roots, patterns))
	}
	return changes, ds
}

// The type errors of code calling what kanna-orm generates, as go/types writes them.
var (
	undefinedFunc   = regexp.MustCompile(`^undefined: (\w+)$`)
	undefinedMethod = regexp.MustCompile(`\(type \*?(\S+) has no field or method \w+`)
)

// pendingInScopes tells whether d is an error of a package of scope types, of idx, that the run loads but does not
// generate for, roots being those it does, saying no more than that the function returning a scope, or a method of a
// scope type, is not there: what the run of that package writes.
func pendingInScopes(idx []indexed, roots []*packages.Package) func(d diag.Diag) bool {
	type names struct{ types, funcs map[string]bool }
	dirs := map[string]names{}
	for _, e := range idx {
		dir := filepath.Dir(e.pos.Filename)
		n, ok := dirs[dir]
		if !ok {
			n = names{types: map[string]bool{}, funcs: map[string]bool{}}
			dirs[dir] = n
		}
		n.types[e.name] = true
		if fn, ok := funcName(e.table); ok {
			n.funcs[fn] = true
		}
	}
	for _, pkg := range roots {
		if len(pkg.GoFiles) > 0 {
			delete(dirs, filepath.Dir(pkg.GoFiles[0]))
		}
	}
	return func(d diag.Diag) bool {
		n, ok := dirs[filepath.Dir(d.Pos.Filename)]
		if !ok || d.Severity != diag.Error {
			return false
		}
		first, _, _ := strings.Cut(d.Message, "\n")
		if m := undefinedFunc.FindStringSubmatch(first); m != nil {
			return n.funcs[m[1]]
		}
		if m := undefinedMethod.FindStringSubmatch(first); m != nil {
			return n.types[m[1][strings.LastIndex(m[1], ".")+1:]]
		}
		return false
	}
}

// readDestination tells the package the -destination flag of a run in dir names, if it names one, and checks the
// -driver flag.
func readDestination(dir string, cfg config) (*destination, *diag.Diag) {
	if cfg.driver != "" && cfg.driver != string(driverPgx) && cfg.driver != string(driverSQL) {
		d := diag.Errorf(token.Position{}, "-driver takes pgx or database/sql, not %q", cfg.driver)
		return nil, &d
	}
	if cfg.destination == "" {
		return nil, nil
	}
	path := cfg.destination
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	dest, err := destinationAt(filepath.Clean(path))
	if err != nil {
		d := diag.Errorf(token.Position{Filename: path}, "%v", err)
		return nil, &d
	}
	return &dest, nil
}

// outputs writes orm_gen.go for each of roots that plans has scope types of, and removes it from the others; and
// writes the implementations of repo into dest, or, when no package of the run declares repositories, removes
// those an earlier run left there.
func outputs(roots []*packages.Package, plans map[*packages.Package][]scopePlan, d snapshot.Dialect, repo repoPlan,
	dest *destination, repositories bool,
) ([]output.Change, []diag.Diag) {
	var (
		changes []output.Change
		ds      []diag.Diag
	)
	write := func(dir, pkgName string, emit func(declared []string) ([]byte, []string, error)) bool {
		path := filepath.Join(dir, outputName)
		src, imports, err := emit(slices.Sorted(maps.Keys(output.DeclaredNames(dir, outputName, pkgName))))
		if err != nil {
			ds = append(ds, diag.Errorf(token.Position{Filename: path}, "%v", err))
			return false
		}
		changes = append(changes, output.Change{Path: path, Data: src})
		ds = append(ds, output.MissingRequires(path, imports)...)
		return true
	}
	for _, pkg := range roots {
		if len(plans[pkg]) == 0 {
			changes = append(changes, removals([]*packages.Package{pkg})...)
			continue
		}
		if !write(filepath.Dir(pkg.GoFiles[0]), pkg.Name, func(declared []string) ([]byte, []string, error) {
			return emit(pkg, d, plans[pkg], declared)
		}) {
			return nil, ds
		}
	}
	switch {
	case len(repo.repos) > 0:
		if !write(dest.dir, dest.name, func(declared []string) ([]byte, []string, error) {
			return emitRepositories(dest.pkgPath, dest.name, d, repo.driver, repo.repos, repo.models, declared)
		}) {
			return nil, ds
		}
	case dest != nil && !repositories:
		changes = append(changes, dest.removal()...)
	}
	return changes, ds
}

// nothingDeclared warns that roots, the packages patterns match, declare neither a //kanna:scope type nor a
// //kanna:repository interface.
func nothingDeclared(roots []*packages.Package, patterns []string) diag.Diag {
	in := make([]string, 0, len(roots))
	for _, pkg := range roots {
		in = append(in, pkg.PkgPath)
	}
	if len(in) == 0 {
		in = patterns // which matched no package
	}
	return diag.Warningf(token.Position{}, "no type is marked //kanna:scope or //kanna:repository in %s",
		strings.Join(in, ", "))
}

// repoPlan is what a run writes into the destination: the implementations of repos, reading the whole rows of
// models, on driver.
type repoPlan struct {
	repos  []repository
	models map[string]readModel
	driver driver
}

// planRepository reads the repositories of the one package of repoPkgs, among pkgs, the packages the run loads, for
// their implementations in dest on the driver the -driver flag names, or else the one of the database.
func planRepository(repoPkgs, pkgs []*packages.Package, s snapshot.Snapshot, idx []indexed, dest *destination,
	drvFlag string, stmts []sqlfile.Statement,
) (repoPlan, []diag.Diag) {
	pkg := repoPkgs[0]
	errorf := func(format string, args ...any) []diag.Diag {
		return []diag.Diag{diag.Errorf(token.Position{}, format, args...)}
	}
	switch {
	case len(repoPkgs) > 1:
		return repoPlan{}, errorf("%s and %s both declare //kanna:repository interfaces, but -destination takes "+
			"those of one package", pkg.PkgPath, repoPkgs[1].PkgPath)
	case dest == nil:
		return repoPlan{}, []diag.Diag{diag.Errorf(token.Position{}, "%s declares //kanna:repository interfaces, "+
			"whose implementations go into the package -destination names", pkg.PkgPath).
			WithHints("as in //go:generate go tool kanna-orm -destination ../postgres")}
	}
	drv := driverPgx
	if s.Dialect == snapshot.MySQL {
		drv = driverSQL
	}
	if drvFlag != "" {
		drv = driver(drvFlag)
	}
	if drv == driverPgx && s.Dialect == snapshot.MySQL {
		return repoPlan{}, errorf("-driver pgx runs on PostgreSQL, but the snapshot is of MySQL")
	}
	repos, models, ds := planRepositories(pkg, pkgs, s, idx, dest.handWritten(), stmts)
	return repoPlan{repos: repos, models: models, driver: drv}, ds
}

// declarations tells which of pkgs declare a //kanna:scope type, and which a //kanna:repository interface.
func declarations(pkgs []*packages.Package) (scoped, repos []*packages.Package) {
	for _, pkg := range pkgs {
		hasScopes, hasRepos := false, false
		for _, f := range pkg.Syntax {
			if ast.IsGenerated(f) {
				continue
			}
			decls, _ := decl.Read(pkg.Fset, f)
			hasScopes = hasScopes || len(decls.Scopes) > 0
			hasRepos = hasRepos || len(decls.Repositories) > 0
		}
		if hasScopes {
			scoped = append(scoped, pkg)
		}
		if hasRepos {
			repos = append(repos, pkg)
		}
	}
	return scoped, repos
}

// removals removes the output an earlier run left in pkgs, whose declarations are all gone.
func removals(pkgs []*packages.Package) []output.Change {
	var changes []output.Change
	for _, pkg := range pkgs {
		if len(pkg.GoFiles) == 0 {
			continue
		}
		if path := filepath.Join(filepath.Dir(pkg.GoFiles[0]), outputName); packages.GeneratedFiles(pkg)[path] {
			changes = append(changes, output.Change{Path: path, Remove: true})
		}
	}
	return changes
}

// readSnapshot reads the snapshot the -snapshot flag of a run in dir names, or the one at the root of its module.
func readSnapshot(dir, flag string) (snapshot.Snapshot, *diag.Diag) {
	path, err := snapshot.Path(dir, flag)
	if err != nil {
		d := diag.Errorf(token.Position{}, "%v", err)
		return snapshot.Snapshot{}, &d
	}
	at := token.Position{Filename: path}
	data, err := os.ReadFile(path) //nolint:gosec // the snapshot
	if errors.Is(err, fs.ErrNotExist) {
		d := diag.Errorf(at, "not described yet").WithHints("run go tool kanna-orm describe to write it")
		return snapshot.Snapshot{}, &d
	}
	if err != nil {
		d := diag.Errorf(at, "%v", err)
		return snapshot.Snapshot{}, &d
	}
	s, err := snapshot.Unmarshal(data)
	if err != nil {
		d := diag.Errorf(at, "%v", err)
		return snapshot.Snapshot{}, &d
	}
	return s, nil
}

// generatedNames names what plans generate, for a database of dialect d, as Set.Pending takes them: the function
// returning the empty scope of each scope type, and its methods.
func generatedNames(plans map[*packages.Package][]scopePlan, d snapshot.Dialect) map[string]bool {
	names := map[string]bool{}
	for pkg, ps := range plans {
		for _, p := range ps {
			names[pkg.PkgPath+"."+p.fn] = true
			for _, m := range methodsOf(p, d) {
				names[pkg.PkgPath+"."+p.name+"."+m.name] = true
			}
			for _, w := range p.written {
				names[pkg.PkgPath+"."+p.name+"."+w.name] = true
			}
			// Whatever a field whose type does not type-check may be once it does.
			for _, f := range p.unchecked {
				for _, suffix := range columnMethods(kind{ordered: true, text: true}, true, snapshot.PostgreSQL) {
					names[pkg.PkgPath+"."+p.name+"."+f+suffix] = true
				}
				names[pkg.PkgPath+"."+p.name+".With"+f] = true
			}
		}
	}
	return names
}

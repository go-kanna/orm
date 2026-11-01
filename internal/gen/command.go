package gen

import (
	"errors"
	"flag"
	"go/ast"
	"go/token"
	"go/types"
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
	constructorDB                       string
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
			fs.StringVar(&cfg.constructorDB, "constructor-db", "",
				"database argument `type` for generated public constructors, as *example.com/app/db.Reader")
			fs.StringVar(&cfg.snapshot, "snapshot", "",
				"the snapshot to read (default: "+snapshot.Name+" at the root of the module)")
			fs.StringVar(&cfg.tags, "tags", "", "comma-separated build tags")
		},
		Run:  run,
		Deps: reportDeps,
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
	l, d := loadRun(dir, cfg, load, dest, nil)
	if d != nil {
		return nil, []diag.Diag{*d}
	}

	p, stop := planRun(dir, cfg, load, dest, l)
	if stop != nil {
		return nil, stop
	}
	set, repoPkgs, roots := p.set, p.repoPkgs, p.roots
	s, idx, plans, repo, ds, pending := p.s, p.idx, p.plans, p.repo, p.ds, p.pending

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
	// What another package's run declares, as a scope type //kanna:scopes implies, is no mistake of this run's.
	rds := slices.DeleteFunc(packages.Recheck(set, changes), set.Pending(impliedNames(idx)))
	if len(rds) > 0 {
		return nil, slices.Concat(ds, rds, set.SetAsideWarnings())
	}
	if len(p.scopePkgs) == 0 && len(repoPkgs) == 0 {
		ds = append(ds, nothingDeclared(roots, patterns))
	}
	return changes, ds
}

// plannedRun is what a run in dir works out from the packages it loaded, before it looks at their type errors.
type plannedRun struct {
	loadedRun

	s       snapshot.Snapshot
	idx     []indexed
	plans   map[*packages.Package][]scopePlan
	repo    repoPlan
	ds      []diag.Diag
	pending map[string]bool
}

// planRun works out what a run generates from l, which it loads again should the models need it, and what load and
// dest gave it. The diagnostics it returns stop the run; those of plannedRun.ds wait for the type errors of the load.
func planRun(dir string, cfg config, load []string, dest *destination, l loadedRun) (plannedRun, []diag.Diag) {
	p := plannedRun{loadedRun: l, plans: map[*packages.Package][]scopePlan{}}
	if len(p.scopePkgs) == 0 && len(p.repoPkgs) == 0 {
		return p, nil
	}

	// Without the snapshot or the index, what the run generates is unknown, and so which errors loading the
	// packages say only that it is missing: they wait for the run that can tell.
	var d *diag.Diag
	if p.s, d = readSnapshot(dir, cfg.snapshot); d != nil {
		return p, []diag.Diag{*d}
	}
	var err error
	if p.idx, err = readIndex(dir, p.s.Tables); err != nil {
		return p, []diag.Diag{diag.Errorf(token.Position{}, "%v", err)}
	}
	// The models //kanna:scopes implies scope types of are read from their packages, which are loaded too when
	// nothing loaded imports them yet, as nothing does before the first run.
	if missing := resolveModels(p.idx, p.set.Packages); len(missing) > 0 {
		if p.loadedRun, d = loadRun(dir, cfg, load, dest, missing); d != nil {
			return p, []diag.Diag{*d}
		}
		resolveModels(p.idx, p.set.Packages)
	}
	for _, pkg := range p.roots {
		var stmts []sqlfile.Statement
		if (slices.Contains(p.scopePkgs, pkg) || slices.Contains(p.repoPkgs, pkg)) && len(pkg.GoFiles) > 0 {
			var sds []diag.Diag
			stmts, sds = readSQL(filepath.Dir(pkg.GoFiles[0]))
			p.ds = slices.Concat(p.ds, sds, misplacedQueries(stmts))
		}
		ps, pds := plan(pkg, p.s, p.idx, stmts)
		p.plans[pkg] = ps
		p.ds = append(p.ds, pds...)
	}
	p.pending = generatedNames(p.plans, p.s.Dialect)
	maps.Copy(p.pending, impliedNames(p.idx))
	if len(p.repoPkgs) > 0 {
		var queries []sqlfile.Statement
		if dest != nil {
			var sds []diag.Diag
			queries, sds = readSQL(dest.dir)
			p.ds = slices.Concat(p.ds, sds, misplacedScopes(queries))
		}
		var rds []diag.Diag
		p.repo, rds = planRepository(p.repoPkgs, p.set.Packages, p.s, p.idx, dest, cfg, queries)
		p.ds = append(p.ds, rds...)
		for _, pkg := range p.repoPkgs {
			if dest != nil {
				maps.Copy(p.pending, repositoryNames(pkg, dest.pkgPath))
			}
		}
	}
	return p, nil
}

// loadedRun is what a run loads: set, of which scopePkgs declare scope types, repoPkgs repositories, and roots are
// those the run generates for.
type loadedRun struct {
	set                        packages.Set
	scopePkgs, repoPkgs, roots []*packages.Package
}

// loadRun loads the packages load names for a run in dir, and those at the import paths of extra, which it reads but
// does not generate for, and checks that dest is none of those it generates for.
func loadRun(dir string, cfg config, load []string, dest *destination, extra []string) (loadedRun, *diag.Diag) {
	if cfg.constructorDB != "" {
		ref, err := parseDatabaseType(cfg.constructorDB)
		if err != nil {
			d := diag.Errorf(token.Position{}, "%v", err)
			return loadedRun{}, &d
		}
		extra = append(slices.Clone(extra), ref.path)
	}
	if len(extra) > 0 {
		if len(load) == 0 {
			load = []string{"."}
		}
		load = append(slices.Clone(load), extra...)
	}
	set, err := packages.Load(load, packages.Config{Dir: dir, Tags: cfg.tags, Regenerated: []string{outputName}})
	if err != nil {
		d := diag.Errorf(token.Position{}, "%v", err)
		return loadedRun{}, &d
	}
	scopePkgs, repoPkgs := declarations(set.Packages)
	if d := dest.check(cfg.destination, scopePkgs); d != nil {
		return loadedRun{}, d
	}
	roots := slices.DeleteFunc(slices.Clone(set.Packages), func(p *packages.Package) bool {
		return dest.holds(p) || slices.Contains(extra, p.PkgPath)
	})
	return loadedRun{set: set, scopePkgs: scopePkgs, repoPkgs: repoPkgs, roots: roots}, nil
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
			return n.funcs[m[1]] || n.types[m[1]]
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
// their implementations in dest on the driver the -driver flag names, or else the one of the database, with the
// queries written by hand there, of stmts.
func planRepository(repoPkgs, pkgs []*packages.Package, s snapshot.Snapshot, idx []indexed, dest *destination,
	cfg config, stmts []sqlfile.Statement,
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
	if cfg.driver != "" {
		drv = driver(cfg.driver)
	}
	if drv == driverPgx && s.Dialect == snapshot.MySQL {
		return repoPlan{}, errorf("-driver pgx runs on PostgreSQL, but the snapshot is of MySQL")
	}
	if cfg.constructorDB != "" && !dest.holds(pkg) {
		return repoPlan{}, errorf("-constructor-db requires the repository interface and implementation in the same package")
	}
	var input types.Type
	if cfg.constructorDB != "" {
		var err error
		input, err = databaseType(cfg.constructorDB, importedPackages(pkgs), drv)
		if err != nil {
			return repoPlan{}, errorf("%v", err)
		}
	}
	repos, models, ds := planRepositories(pkg, pkgs, s, idx, *dest, stmts, drv, input)
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
			hasScopes = hasScopes || len(decls.Scopes) > 0 || len(decls.Models) > 0
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
			if p.implied {
				names[pkg.PkgPath+"."+p.name] = true
			}
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

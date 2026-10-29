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
	gopackages "golang.org/x/tools/go/packages"

	"github.com/go-kanna/orm/internal/decl"
	"github.com/go-kanna/orm/internal/snapshot"
)

// config holds what kanna-orm's own flags set.
type config struct {
	snapshot, tags string
}

// Command returns kanna-orm, which generates from the snapshot kanna-orm describe, a command of its own, takes.
func Command() cli.Command[config] {
	return cli.Command[config]{
		Name:    generator,
		Summary: "write the methods of the //kanna:scope types, from the snapshot kanna-orm describe takes of a database",
		Args:    "[packages]",
		Help: "The packages default to the one in the current directory, so that\n" +
			"//go:generate go tool " + generator + " needs no argument.\n\n" +
			"kanna-orm describe [flags] [packages] writes the snapshot; kanna-orm describe -h tells more.",
		Flags: func(fs *flag.FlagSet, cfg *config) {
			fs.StringVar(&cfg.snapshot, "snapshot", "",
				"the snapshot to read (default: "+snapshot.Name+" at the root of the module)")
			fs.StringVar(&cfg.tags, "tags", "", "comma-separated build tags")
		},
		Run: run,
	}
}

func run(dir string, cfg config, patterns []string) ([]output.Change, []diag.Diag) {
	set, err := packages.Load(patterns, packages.Config{Dir: dir, Tags: cfg.tags, Regenerated: []string{outputName}})
	if err != nil {
		return nil, []diag.Diag{diag.Errorf(token.Position{}, "%v", err)}
	}

	var (
		s     snapshot.Snapshot
		plans = map[*packages.Package][]scopePlan{}
		ds    []diag.Diag
	)
	scoped, declared := declarations(set.Packages)
	if scoped {
		// Without the snapshot or the index, what the run generates is unknown, and so which errors loading the
		// packages say only that it is missing: they wait for the run that can tell.
		var d *diag.Diag
		if s, d = readSnapshot(dir, cfg.snapshot); d != nil {
			return nil, []diag.Diag{*d}
		}
		idx, err := readIndex(dir)
		if err != nil {
			return nil, []diag.Diag{diag.Errorf(token.Position{}, "%v", err)}
		}
		for _, pkg := range set.Packages {
			ps, pds := plan(pkg, s, idx)
			plans[pkg] = ps
			ds = append(ds, pds...)
		}
	}

	// Code calling what this run generates does not compile before it has run, nor does code calling it as the run
	// is about to change it. So a type error of a package generated for does not stop the run here: recheck tells,
	// once the output is in place, whether it remains.
	blocking, deferred := split(set)
	if diag.HasErrors(blocking) {
		for _, path := range set.SetAside {
			blocking = append(blocking, diag.Warningf(token.Position{Filename: path},
				"set aside because it no longer compiles; it is regenerated once the errors above are fixed"))
		}
		return nil, blocking
	}
	if ds = append(blocking, ds...); diag.HasErrors(ds) {
		// What is wrong with a declaration may well be one of the type errors, but for those of a call to what the
		// run generates.
		return nil, append(slices.DeleteFunc(deferred, pending(plans, s.Dialect)), ds...)
	}

	var (
		changes []output.Change
		outputs = map[string][]byte{}
	)
	for _, pkg := range set.Packages {
		if len(plans[pkg]) == 0 {
			for _, removed := range removals([]*packages.Package{pkg}) {
				changes, outputs[removed.Path] = append(changes, removed), nil
			}
			continue
		}
		pkgDir := filepath.Dir(pkg.GoFiles[0])
		path := filepath.Join(pkgDir, outputName)
		declared := slices.Sorted(maps.Keys(output.DeclaredNames(pkgDir, outputName, pkg.Name)))
		src, imports, err := emit(pkg, s.Dialect, plans[pkg], declared)
		if err != nil {
			return nil, append(ds, diag.Errorf(token.Position{Filename: path}, "%v", err))
		}
		changes = append(changes, output.Change{Path: path, Data: src})
		outputs[path] = src
		ds = append(ds, output.MissingRequires(path, imports)...)
	}
	if rds := recheck(set.Packages, outputs); len(rds) > 0 {
		return nil, append(ds, rds...)
	}

	if !declared {
		in := make([]string, 0, len(set.Packages))
		for _, pkg := range set.Packages {
			in = append(in, pkg.PkgPath)
		}
		if len(in) == 0 {
			in = patterns // which matched no package
		}
		ds = append(ds, diag.Warningf(token.Position{}, "no type is marked //kanna:scope or //kanna:repository in %s",
			strings.Join(in, ", ")))
	}
	return changes, ds
}

// split tells the errors loading set that generating cannot mend, those of a package not generated for and any but a
// type error, from the type errors of the packages generated for.
func split(set packages.Set) (blocking, deferred []diag.Diag) {
	typeErrorsOnly := map[string]bool{}
	for _, pkg := range set.Packages {
		// The compiler's account of the type errors comes along as a list error, which packages.Load passes over for
		// that of the type checker.
		if slices.ContainsFunc(pkg.Errors, func(e gopackages.Error) bool {
			return e.Kind != gopackages.TypeError && (e.Kind != gopackages.ListError || !strings.HasPrefix(e.Msg, "# "))
		}) {
			continue
		}
		for _, f := range pkg.CompiledGoFiles {
			typeErrorsOnly[f] = true
		}
	}
	for _, d := range set.Diags {
		if typeErrorsOnly[d.Pos.Filename] {
			deferred = append(deferred, d)
		} else {
			blocking = append(blocking, d)
		}
	}
	return blocking, deferred
}

// declarations reports whether pkgs declare a //kanna:scope type, and whether they declare that or a
// //kanna:repository interface.
func declarations(pkgs []*packages.Package) (scoped, declared bool) {
	for _, pkg := range pkgs {
		for _, f := range pkg.Syntax {
			if ast.IsGenerated(f) {
				continue
			}
			decls, _ := decl.Read(pkg.Fset, f)
			scoped = scoped || len(decls.Scopes) > 0
			declared = declared || scoped || len(decls.Repositories) > 0
		}
	}
	return scoped, declared
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

var (
	missingMethod = regexp.MustCompile(`\(type \*?(?:[\w.]+\.)?(\w+) has no field or method (\w+)`)
	missingName   = regexp.MustCompile(`^undefined: (?:\w+\.)?(\w+)$`)
)

// pending returns whether an error loading the packages says only that a function or a method that plans has
// generated, for a database of dialect d, is missing.
func pending(plans map[*packages.Package][]scopePlan, d snapshot.Dialect) func(diag.Diag) bool {
	methods, funcs := map[string]map[string]bool{}, map[string]bool{}
	for _, ps := range plans {
		for _, p := range ps {
			funcs[p.fn] = true
			names := map[string]bool{}
			for _, m := range methodsOf(p, d) {
				names[m.name] = true
			}
			for _, w := range p.written {
				names[w.name] = true
			}
			methods[p.name] = names
		}
	}
	return func(ld diag.Diag) bool {
		if ld.Severity != diag.Error {
			return false
		}
		if m := missingMethod.FindStringSubmatch(ld.Message); m != nil {
			return methods[m[1]][m[2]]
		}
		if m := missingName.FindStringSubmatch(ld.Message); m != nil {
			return funcs[m[1]]
		}
		return false
	}
}

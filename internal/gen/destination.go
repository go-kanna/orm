package gen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/output"
	"github.com/go-kanna/internal/packages"

	"github.com/go-kanna/orm/internal/decl"
	"github.com/go-kanna/orm/internal/gomod"
	"github.com/go-kanna/orm/internal/snapshot"
	"github.com/go-kanna/orm/internal/sqlfile"
)

// destination is the package the implementations of the repositories are written into.
type destination struct {
	dir, pkgPath, name string
}

// destinationAt tells the package in dir, which need not exist yet: its import path, from the module it is in, and
// its name, that of its Go files or else of dir.
func destinationAt(dir string) (destination, error) {
	pkgPath, err := gomod.PkgPath(dir)
	if err != nil {
		return destination{}, err //nolint:wrapcheck // the caller reports it at dir
	}
	name, err := output.PackageName(dir, outputName, "")
	if err != nil {
		return destination{}, err //nolint:wrapcheck // the caller reports it at dir
	}
	return destination{dir: dir, pkgPath: pkgPath, name: name}, nil
}

// holds reports whether pkg is the package in d, if there is a destination.
func (d *destination) holds(pkg *packages.Package) bool {
	return d != nil && len(pkg.GoFiles) > 0 && output.SameDir(d.dir, filepath.Dir(pkg.GoFiles[0]))
}

// check keeps scope methods and repository implementations from writing the same file.
func (d *destination) check(flag string, scopePkgs []*packages.Package) *diag.Diag {
	if i := slices.IndexFunc(scopePkgs, d.holds); i >= 0 {
		e := diag.Errorf(token.Position{}, "-destination %s holds the //kanna:scope types of %s, whose methods go "+
			"into its %s", flag, scopePkgs[i].PkgPath, outputName).WithHints("name a directory of the implementations' own")
		return &e
	}
	return nil
}

// hasCode reports whether dir holds Go files besides the output and tests, which a run loads.
func hasCode(dir string) bool {
	names, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	return slices.ContainsFunc(names, func(name string) bool {
		return filepath.Base(name) != outputName && !strings.HasSuffix(name, "_test.go")
	})
}

// generatedFile reports whether path is a file carrying the generated-code marker, which a run may remove; a file
// of that name written by hand is left alone.
func generatedFile(path string) bool {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly|parser.ParseComments)
	return err == nil && ast.IsGenerated(f)
}

// handWritten names the methods the files of d other than the output declare, by the type they are declared on.
func (d destination) handWritten() map[string]map[string]bool {
	names, _ := filepath.Glob(filepath.Join(d.dir, "*.go"))
	methods := map[string]map[string]bool{}
	fset := token.NewFileSet()
	for _, name := range names {
		if filepath.Base(name) == outputName || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil || f.Name.Name != d.name {
			continue
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
				continue
			}
			t := fn.Recv.List[0].Type
			if star, ok := t.(*ast.StarExpr); ok {
				t = star.X
			}
			if id, ok := t.(*ast.Ident); ok {
				if methods[id.Name] == nil {
					methods[id.Name] = map[string]bool{}
				}
				methods[id.Name][fn.Name.Name] = true
			}
		}
	}
	return methods
}

// planRepositories reads the repositories of pkg, for their implementations in dest, leaving out the methods dest
// declares by hand, and the models they read whole rows of. pkgs are the packages the run loads, whose scope types the
// repositories may take, and stmts the statements of the .sql files of dest.
func planRepositories(pkg *packages.Package, pkgs []*packages.Package, s snapshot.Snapshot, idx []indexed,
	dest destination, stmts []sqlfile.Statement,
) ([]repository, map[string]readModel, []diag.Diag) {
	scopes, _ := findScopes(pkg, s, idx) // plan reports what is wrong with them
	queries, ds := readQueries(stmts)
	others := slices.DeleteFunc(slices.Clone(pkgs), func(p *packages.Package) bool { return p == pkg })
	r := repoReader{
		pkg: pkg, s: s, queries: queries, queriesAt: dest.pkgPath, skip: dest.handWritten(),
		imported: importedPackages(pkgs), local: dest.holds(pkg),
		l: linker{
			fset: pkg.Fset, pkgPath: pkg.PkgPath, pkgName: pkg.Name, scopes: scopes, tables: s.Tables, idx: idx,
			loaded: loadedScopes(others, s, idx),
		},
	}
	repos, rds := r.findRepositories()
	if ds = append(ds, rds...); diag.HasErrors(ds) {
		return nil, nil, ds
	}
	models, mds := r.readModels(repos)
	return repos, models, append(ds, mds...)
}

// repositoryNames names what the implementations of the repositories of pkg declare in the package at pkgPath, as
// Set.Pending takes them, whether or not the run can write them: DBTX, and for each repository its struct, its
// constructor, and the methods of its interface.
func repositoryNames(pkg *packages.Package, pkgPath string) map[string]bool {
	names := map[string]bool{pkgPath + ".DBTX": true}
	for _, file := range pkg.Syntax {
		if ast.IsGenerated(file) {
			continue
		}
		decls, _ := decl.Read(pkg.Fset, file) // findRepositories reports what is wrong with the directives
		for _, d := range decls.Repositories {
			ts := d.Spec
			name, constructor := ts.Name.Name, "New"+ts.Name.Name
			if pkgPath == pkg.PkgPath {
				name = lowerFirst(name)
				constructor = "new" + ts.Name.Name
			}
			names[pkgPath+"."+name] = true
			names[pkgPath+"."+constructor] = true
			if it, ok := ts.Type.(*ast.InterfaceType); ok {
				for _, f := range it.Methods.List {
					for _, n := range f.Names {
						names[pkgPath+"."+name+"."+n.Name] = true
						if pkgPath == pkg.PkgPath {
							names[pkgPath+"."+name+"."+lowerFirst(n.Name)] = true
						}
					}
				}
			}
		}
	}
	return names
}

// removal removes the implementations an earlier run left in d, when they are all gone.
func (d destination) removal() []output.Change {
	if path := filepath.Join(d.dir, outputName); generatedFile(path) {
		return []output.Change{{Path: path, Remove: true}}
	}
	return nil
}

package gen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
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
	name, err := output.PackageName(dir, repositoryOutputName)
	if err != nil {
		return destination{}, err //nolint:wrapcheck // the caller reports it at dir
	}
	return destination{dir: dir, pkgPath: pkgPath, name: name}, nil
}

// holds reports whether pkg is the package in d, if there is a destination.
func (d *destination) holds(pkg *packages.Package) bool {
	return d != nil && len(pkg.GoFiles) > 0 && output.SameDir(d.dir, filepath.Dir(pkg.GoFiles[0]))
}

// hasCode reports whether dir holds Go files besides the output and tests, which a run loads.
func hasCode(dir string) bool {
	names, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	return slices.ContainsFunc(names, func(name string) bool {
		return filepath.Base(name) != repositoryOutputName && !strings.HasSuffix(name, "_test.go")
	})
}

// generatedFile reports whether path is a file carrying the generated-code marker, which a run may remove; a file
// of that name written by hand is left alone.
func generatedFile(path string) bool {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly|parser.ParseComments)
	return err == nil && ast.IsGenerated(f)
}

// receiverCalls finds what the methods the files of d other than the output declare select on their receiver, as
// r.upsert does upsert in func (r category) Upsert, by the type they are declared on: the helpers they call. A selector
// on anything else, as on a value of another type, is none of them. They are read from the syntax alone, as the type
// is not declared before the first generation.
func (d destination) receiverCalls() map[string]map[string]bool {
	names, _ := filepath.Glob(filepath.Join(d.dir, "*.go"))
	calls := map[string]map[string]bool{}
	fset := token.NewFileSet()
	for _, name := range names {
		if filepath.Base(name) == repositoryOutputName || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil || f.Name.Name != d.name {
			continue
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv == nil || len(fn.Recv.List) != 1 || len(fn.Recv.List[0].Names) != 1 {
				continue
			}
			t := fn.Recv.List[0].Type
			if star, ok := t.(*ast.StarExpr); ok {
				t = star.X
			}
			id, ok := t.(*ast.Ident)
			if !ok {
				continue
			}
			if calls[id.Name] == nil {
				calls[id.Name] = map[string]bool{}
			}
			recv := fn.Recv.List[0].Names[0].Name
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if s, ok := n.(*ast.SelectorExpr); ok {
					if x, ok := s.X.(*ast.Ident); ok && x.Name == recv {
						calls[id.Name][s.Sel.Name] = true
					}
				}
				return true
			})
		}
	}
	return calls
}

// handFunc is a function declared by hand: its first result, if any, and the paths of the packages its file imports,
// by their names.
type handFunc struct {
	result  ast.Expr
	imports map[string]string
}

// returns reports whether f returns repo first: the interface, or its implementation, or a pointer to one.
func (f handFunc) returns(repo repository) bool {
	t := f.result
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	switch x := t.(type) {
	case *ast.Ident:
		return x.Name == repo.name || x.Name == repo.implementation()
	case *ast.SelectorExpr:
		pkg, ok := x.X.(*ast.Ident)
		return ok && x.Sel.Name == repo.name && f.imports[pkg.Name] == repo.iface.Path()
	}
	return false
}

// functions finds the functions the files of d other than the output and tests declare at their top level, by their
// names.
func (d destination) functions() map[string]handFunc {
	names, _ := filepath.Glob(filepath.Join(d.dir, "*.go"))
	funcs := map[string]handFunc{}
	fset := token.NewFileSet()
	for _, name := range names {
		if filepath.Base(name) == repositoryOutputName || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil || f.Name.Name != d.name {
			continue
		}
		imports := importNames(f)
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				h := handFunc{imports: imports}
				if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
					h.result = fn.Type.Results.List[0].Type
				}
				funcs[fn.Name.Name] = h
			}
		}
	}
	return funcs
}

// handMethod is a method declared by hand: where it is, and its parameters and results.
type handMethod struct {
	pos token.Position
	typ *ast.FuncType
}

// handWritten finds the methods the files of d other than the output declare, by the type they are declared on and
// their names.
func (d destination) handWritten() map[string]map[string]handMethod {
	names, _ := filepath.Glob(filepath.Join(d.dir, "*.go"))
	methods := map[string]map[string]handMethod{}
	fset := token.NewFileSet()
	for _, name := range names {
		if filepath.Base(name) == repositoryOutputName || strings.HasSuffix(name, "_test.go") {
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
					methods[id.Name] = map[string]handMethod{}
				}
				methods[id.Name][fn.Name.Name] = handMethod{pos: fset.Position(fn.Name.Pos()), typ: fn.Type}
			}
		}
	}
	return methods
}

// planRepositories reads the repositories of pkg, for their implementations in dest, leaving out the methods dest
// declares by hand, and the models they read whole rows of. pkgs are the packages the run loads, whose scope types the
// repositories may take, and stmts the statements of the .sql files of dest.
func planRepositories(pkg *packages.Package, pkgs []*packages.Package, s snapshot.Snapshot, idx []indexed,
	dest destination, stmts []sqlfile.Statement, drv driver,
) ([]repository, map[string]readModel, []diag.Diag) {
	scopes, _ := findScopes(pkg, s, idx) // plan reports what is wrong with them
	queries, ds := readQueries(stmts)
	others := slices.DeleteFunc(slices.Clone(pkgs), func(p *packages.Package) bool { return p == pkg })
	r := repoReader{
		pkg: pkg, s: s, queries: queries, queriesAt: dest.pkgPath, skip: dest.handWritten(),
		imported: importedPackages(pkgs), local: dest.holds(pkg),
		driver:   drv,
		calls:    dest.receiverCalls(),
		declared: output.DeclaredNames(dest.dir, repositoryOutputName, dest.name),
		funcs:    dest.functions(),
		l: linker{
			fset: pkg.Fset, pkgPath: pkg.PkgPath, pkgName: pkg.Name, scopes: scopes, tables: s.Tables, idx: idx,
			loaded: loadedScopes(others, s, idx), driver: drv,
		},
	}
	repos, rds := r.findRepositories()
	if ds = append(ds, rds...); diag.HasErrors(ds) {
		return nil, nil, ds
	}
	models, mds := r.readModels(repos)
	return repos, models, slices.Concat(ds, mds, r.driverReads(repos, models))
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
				names[pkgPath+".New"+ts.Name.Name] = true
			}
			names[pkgPath+"."+name] = true
			names[pkgPath+"."+constructor] = true
			operation := lowerFirst(ts.Name.Name) + "Operation"
			names[pkgPath+"."+operation] = true
			for _, m := range interfaceMethods(pkg, ts) {
				names[pkgPath+"."+name+"."+m] = true
				names[pkgPath+"."+operation+m] = true
				if pkgPath == pkg.PkgPath {
					names[pkgPath+"."+name+"."+lowerFirst(m)] = true
				}
			}
		}
	}
	return names
}

// interfaceMethods names the methods of the interface ts declares in pkg, those of the interfaces it embeds too, as
// far as pkg type-checks.
func interfaceMethods(pkg *packages.Package, ts *ast.TypeSpec) []string {
	var names []string
	if obj, ok := pkg.Types.Scope().Lookup(ts.Name.Name).(*types.TypeName); ok {
		if iface, ok := obj.Type().Underlying().(*types.Interface); ok {
			for m := range iface.Methods() {
				names = append(names, m.Name())
			}
		}
	}
	if it, ok := ts.Type.(*ast.InterfaceType); ok {
		for _, f := range it.Methods.List {
			for _, n := range f.Names {
				if !slices.Contains(names, n.Name) {
					names = append(names, n.Name)
				}
			}
		}
	}
	return names
}

// removal removes the implementations an earlier run left in d, when they are all gone.
func (d destination) removal() []output.Change {
	if path := filepath.Join(d.dir, repositoryOutputName); generatedFile(path) {
		return []output.Change{{Path: path, Remove: true}}
	}
	return nil
}

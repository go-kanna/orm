package vet

import (
	"bytes"
	"go/ast"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/packages"
	gopackages "golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"

	"github.com/go-kanna/orm/internal/gomod"
)

// loaded is the program vet reads: the packages it checks, and those of their module they import and of the
// repositories kanna-orm generated there, with their bodies.
type loaded struct {
	prog      *ssa.Program
	fset      *token.FileSet
	files     []*ast.File
	types     []*types.Package
	functions []*ssa.Function
}

// load loads the packages patterns match in dir, those of the same module they import, and those of the module
// holding repositories kanna-orm generated that import any of them, the implementations of their interfaces.
func load(dir, tags string, patterns []string) (loaded, []diag.Diag) {
	errorf := func(err error) (loaded, []diag.Diag) {
		return loaded{}, []diag.Diag{diag.Errorf(token.Position{}, "%v", err)}
	}
	root, modPath, err := gomod.Of(dir)
	if err != nil {
		return errorf(err)
	}
	var flags []string
	if tags != "" {
		flags = []string{"-tags=" + tags}
	}
	inModule := func(p *gopackages.Package) bool { return p.Module != nil && p.Module.Path == modPath }

	targets, err := gopackages.Load(&gopackages.Config{
		Dir: dir, BuildFlags: flags,
		Mode: gopackages.NeedName | gopackages.NeedImports | gopackages.NeedDeps | gopackages.NeedModule,
	}, patterns...)
	if err != nil {
		return errorf(err)
	}
	checked := map[string]bool{}
	reached := map[string]bool{}
	gopackages.Visit(targets, func(p *gopackages.Package) bool {
		if !inModule(p) || reached[p.PkgPath] {
			return false
		}
		reached[p.PkgPath] = true
		return true
	}, nil)
	for _, p := range targets {
		checked[p.PkgPath] = true
	}

	module, err := gopackages.Load(&gopackages.Config{
		Dir: root, BuildFlags: flags,
		Mode: gopackages.NeedName | gopackages.NeedFiles | gopackages.NeedImports | gopackages.NeedModule,
	}, "./...")
	if err != nil {
		return errorf(err)
	}
	for _, p := range module {
		if !inModule(p) || reached[p.PkgPath] || !holdsRepositories(p) {
			continue
		}
		for path := range p.Imports {
			if reached[path] {
				reached[p.PkgPath] = true
				break
			}
		}
	}

	paths := make([]string, 0, len(reached))
	for path := range reached {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	set, err := packages.Load(paths, packages.Config{Dir: dir, Tags: tags, TypesInfo: true})
	if err != nil {
		return errorf(err)
	}
	if diag.HasErrors(set.Diags) {
		return loaded{}, set.Diags
	}

	l := loaded{fset: set.Fset, prog: ssa.NewProgram(set.Fset, ssa.InstantiateGenerics)}
	created := map[*types.Package]bool{}
	var ssaPkgs []*ssa.Package
	for _, p := range set.Packages {
		if p.Types == nil || created[p.Types] {
			continue
		}
		created[p.Types] = true
		pkg := l.prog.CreatePackage(p.Types, p.Syntax, p.TypesInfo, true)
		l.files = append(l.files, p.Syntax...)
		l.types = append(l.types, p.Types)
		if checked[p.PkgPath] {
			ssaPkgs = append(ssaPkgs, pkg)
		}
	}
	var create func(pkgs []*types.Package)
	create = func(pkgs []*types.Package) {
		for _, t := range pkgs {
			if created[t] {
				continue
			}
			created[t] = true
			l.prog.CreatePackage(t, nil, nil, true)
			create(t.Imports())
		}
	}
	for _, t := range l.types {
		create(t.Imports())
	}
	l.prog.Build()
	l.functions = functionsOf(l.prog, ssaPkgs)
	return l, nil
}

// holdsRepositories reports whether p has a file of repositories kanna-orm generated.
func holdsRepositories(p *gopackages.Package) bool {
	for _, f := range p.GoFiles {
		if filepath.Base(f) == "repository_gen.go" && startsGenerated(f) {
			return true
		}
	}
	return false
}

func startsGenerated(path string) bool {
	f, err := os.Open(path) //nolint:gosec // a file of a package of the module
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 256)
	n, _ := io.ReadFull(f, head)
	return bytes.Contains(head[:n], []byte(generatedHeader))
}

// functionsOf are the functions and methods pkgs declare, in the order of their source.
func functionsOf(prog *ssa.Program, pkgs []*ssa.Package) []*ssa.Function {
	var fns []*ssa.Function
	for _, pkg := range pkgs {
		for _, m := range pkg.Members {
			switch m := m.(type) {
			case *ssa.Function:
				fns = append(fns, m)
			case *ssa.Type:
				if n, ok := m.Type().(*types.Named); ok && n.TypeParams().Len() > 0 {
					continue
				}
				for _, t := range []types.Type{m.Type(), types.NewPointer(m.Type())} {
					for sel := range prog.MethodSets.MethodSet(t).Methods() {
						fn := prog.MethodValue(sel)
						if fn != nil && fn.Synthetic == "" && fn.Pkg == pkg && !slices.Contains(fns, fn) {
							fns = append(fns, fn)
						}
					}
				}
			}
		}
	}
	slices.SortFunc(fns, func(a, b *ssa.Function) int {
		if c := int(a.Pos() - b.Pos()); c != 0 {
			return c
		}
		return strings.Compare(a.Name(), b.Name())
	})
	return fns
}

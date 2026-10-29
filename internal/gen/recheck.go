package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/packages"
)

// recheck type-checks pkgs again as they are once outputs are in place, each the file the run writes at its path or
// nil for one it removes, and reports the errors in their other files, written by hand or by another generator.
//
// An expression calling what the run is about to generate cannot be checked before it is generated, and neither can
// what it is part of, so a mistake there would otherwise show only on the next run; nor can a call to what the run
// is about to change. Every package of pkgs is checked again, as one that generates nothing may call into one that
// does. The generated files are not reported on: one may name a scope type of a package the run has not loaded,
// which their own compilation checks.
func recheck(pkgs []*packages.Package, outputs map[string][]byte) []diag.Diag {
	var (
		ds      []diag.Diag
		checked = map[string]*types.Package{}
		visited = map[*packages.Package]bool{}
		byPath  = map[string]*packages.Package{}
	)
	for _, pkg := range pkgs {
		byPath[pkg.PkgPath] = pkg
	}

	var check func(pkg *packages.Package)
	check = func(pkg *packages.Package) {
		if visited[pkg] {
			return
		}
		visited[pkg] = true
		// A package imports those of pkgs checked again before it.
		for path := range pkg.Imports {
			if p := byPath[path]; p != nil {
				check(p)
			}
		}

		var output string
		if len(pkg.GoFiles) > 0 {
			output = filepath.Join(filepath.Dir(pkg.GoFiles[0]), outputName)
		}
		src, replaced := outputs[output]
		files := make([]*ast.File, 0, len(pkg.Syntax)+1)
		for _, f := range pkg.Syntax {
			if !replaced || pkg.Fset.Position(f.Pos()).Filename != output {
				files = append(files, f)
			}
		}
		if src != nil {
			f, err := parser.ParseFile(pkg.Fset, output, src, parser.ParseComments|parser.SkipObjectResolution)
			if err != nil {
				ds = append(ds, diag.Errorf(token.Position{Filename: output}, "the code generated does not parse: %v",
					err))
				return
			}
			files = append(files, f)
		}

		conf := types.Config{
			Importer: importer{pkg: pkg, loaded: byPath, checked: checked},
			Error: func(err error) {
				if e, ok := err.(types.Error); ok { //nolint:errorlint // types.Config.Error is given types.Error alone
					if pos := e.Fset.Position(e.Pos); !replaced || pos.Filename != output {
						ds = append(ds, diag.Errorf(pos, "%s", e.Msg))
					}
				}
			},
		}
		p, _ := conf.Check(pkg.PkgPath, pkg.Fset, files, nil) // the errors went to conf.Error
		checked[pkg.PkgPath] = p
	}
	for _, pkg := range pkgs {
		check(pkg)
	}
	return ds
}

// importer imports what a package of the load imports: one of those checked again as checked, and any other as the
// load has it, so that the types are those the rest of the load knows. A package only the file the run writes imports
// may be one the package's own files do not, so the whole load is looked through.
type importer struct {
	pkg     *packages.Package
	loaded  map[string]*packages.Package
	checked map[string]*types.Package
}

func (im importer) Import(path string) (*types.Package, error) {
	if p := im.checked[path]; p != nil {
		return p, nil
	}
	if path == "unsafe" {
		return types.Unsafe, nil
	}
	if p := im.loaded[path]; p != nil && p.Types != nil {
		return p.Types, nil
	}
	seen := map[*types.Package]bool{}
	for next := im.pkg.Types.Imports(); len(next) > 0; {
		var deeper []*types.Package
		for _, p := range next {
			if seen[p] {
				continue
			}
			seen[p] = true
			if p.Path() == path {
				return p, nil
			}
			deeper = append(deeper, p.Imports()...)
		}
		next = deeper
	}
	return nil, fmt.Errorf("%s is not loaded", path)
}

package gen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/packages"

	"github.com/go-kanna/orm/internal/decl"
	"github.com/go-kanna/orm/internal/gomod"
	"github.com/go-kanna/orm/internal/snapshot"
)

// indexed is a //kanna:scope type of the module, as its syntax declares it, or one a //kanna:scopes directive implies.
type indexed struct {
	// pkgPath and pkgName are those of the package declaring it.
	pkgPath, pkgName, name string
	// modelPath and modelName name its model by the import path of the model's package.
	modelPath, modelName string
	table                string
	pos                  token.Position
	// implied reports whether a //kanna:scopes directive implies it, and model is then its model, once a run that
	// loads the model's package has found it.
	implied bool
	model   *types.Named
}

// readIndex reads the //kanna:scope types of the module dir is in, and those the //kanna:scopes directives there imply
// of tables, from their syntax alone: type-checking the whole module would make every run of go generate as slow as
// the module is large. A scope type whose syntax does not tell its model is left out, for the run generating its own
// package to report, and so is one implied of a model or a table a //kanna:scope type holds.
func readIndex(dir string, tables map[string]snapshot.Table) ([]indexed, error) {
	root, modPath, err := gomod.Of(dir)
	if err != nil {
		return nil, err //nolint:wrapcheck // it names dir already
	}

	var idx []indexed
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(file string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && file != root && gomod.Skip(file, d.Name()):
			return filepath.SkipDir
		case d.IsDir() || !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go"):
			return nil
		}
		data, err := os.ReadFile(file) //nolint:gosec // a file of the module
		if err != nil {
			return err //nolint:wrapcheck // wrapped below, as every error of the walk is
		}
		if !bytes.Contains(data, []byte("//kanna:scope")) {
			return nil
		}
		f, err := parser.ParseFile(fset, file, data, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil || ast.IsGenerated(f) {
			return nil //nolint:nilerr // the build of its own package reports a file that does not parse
		}

		pkgPath := modPath
		if rel, _ := filepath.Rel(root, filepath.Dir(file)); rel != "." {
			pkgPath += "/" + filepath.ToSlash(rel)
		}
		decls, _ := decl.Read(fset, f)
		for _, sc := range decls.Scopes {
			modelPath, modelName, ok := modelSyntax(f, pkgPath, sc.Spec)
			if !ok {
				continue
			}
			idx = append(idx, indexed{
				pkgPath: pkgPath, pkgName: f.Name.Name, name: sc.Spec.Name.Name,
				modelPath: modelPath, modelName: modelName, table: sc.Table, pos: sc.Pos,
			})
		}
		hasTable := func(table string) bool { _, ok := tables[table]; return ok }
		for _, m := range decls.Models {
			for _, im := range decl.Imply(m, root, modPath, hasTable) {
				idx = append(idx, indexed{
					pkgPath: pkgPath, pkgName: f.Name.Name, name: im.Model,
					modelPath: im.ModelPath, modelName: im.Model, table: im.Table, pos: im.Pos, implied: true,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the scope types of %s: %w", modPath, err)
	}

	// A //kanna:scope type wins over one implied of its model or its table.
	explicit := map[string]bool{}
	for _, e := range idx {
		if !e.implied {
			explicit[e.modelPath+"."+e.modelName] = true
			explicit[e.table] = true
		}
	}
	return slices.DeleteFunc(idx, func(e indexed) bool {
		return e.implied && (explicit[e.modelPath+"."+e.modelName] || explicit[e.table])
	}), nil
}

// resolveModels finds the models of the scope types of idx that //kanna:scopes directives imply, among the packages
// pkgs are and import, and returns the import paths of the packages of those it cannot find.
func resolveModels(idx []indexed, pkgs []*packages.Package) []string {
	byPath := importedPackages(pkgs)
	var missing []string
	for i, e := range idx {
		if !e.implied {
			continue
		}
		p := byPath[e.modelPath]
		if p == nil {
			if !slices.Contains(missing, e.modelPath) {
				missing = append(missing, e.modelPath)
			}
			continue
		}
		if obj, ok := p.Scope().Lookup(e.modelName).(*types.TypeName); ok {
			if n, ok := types.Unalias(obj.Type()).(*types.Named); ok && isStruct(n) {
				idx[i].model = n
			}
		}
	}
	return missing
}

// importedPackages maps the import path of each of pkgs, and of each package they import, to its types.
func importedPackages(pkgs []*packages.Package) map[string]*types.Package {
	byPath := map[string]*types.Package{}
	var walk func(p *types.Package)
	walk = func(p *types.Package) {
		if p == nil || byPath[p.Path()] != nil {
			return
		}
		byPath[p.Path()] = p
		for _, imported := range p.Imports() {
			walk(imported)
		}
	}
	for _, pkg := range pkgs {
		walk(pkg.Types)
	}
	return byPath
}

// impliedNames names, as Set.Pending takes them, the scope types //kanna:scopes implies of idx, and the functions
// returning their empty scopes: what the run of their packages declares.
func impliedNames(idx []indexed) map[string]bool {
	names := map[string]bool{}
	for _, e := range idx {
		if e.implied {
			names[e.pkgPath+"."+e.name] = true
			if fn, ok := funcName(e.table); ok {
				names[e.pkgPath+"."+fn] = true
			}
		}
	}
	return names
}

// modelSyntax reads the model of the scope type spec declares, in the package at pkgPath, from the orm.Of[M] of its
// one field and the imports of f.
func modelSyntax(f *ast.File, pkgPath string, spec *ast.TypeSpec) (modelPath, modelName string, ok bool) {
	st, ok := spec.Type.(*ast.StructType)
	if !ok || spec.TypeParams != nil || len(st.Fields.List) != 1 || len(st.Fields.List[0].Names) > 1 {
		return "", "", false
	}
	ix, ok := st.Fields.List[0].Type.(*ast.IndexExpr)
	if !ok {
		return "", "", false
	}
	of, ok := ix.X.(*ast.SelectorExpr)
	if !ok || of.Sel.Name != "Of" {
		return "", "", false
	}
	imports := importNames(f)
	if x, ok := of.X.(*ast.Ident); !ok || imports[x.Name] != ormPath {
		return "", "", false
	}
	switch m := ix.Index.(type) {
	case *ast.Ident:
		return pkgPath, m.Name, true
	case *ast.SelectorExpr:
		if x, ok := m.X.(*ast.Ident); ok && imports[x.Name] != "" {
			return imports[x.Name], m.Sel.Name, true
		}
	}
	return "", "", false
}

// importNames maps the name each import of f is known by in f to its path. A package imported without a name is
// taken to be named as its path ends, as goimports takes it.
func importNames(f *ast.File) map[string]string {
	names := map[string]string{}
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := assumedName(p)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[name] = p
	}
	return names
}

// assumedName is the name a package at importPath is taken to have: the last element of the path, or the one before a
// major version such as v2, without a go- before it, up to a character no Go name can hold.
func assumedName(importPath string) string {
	base := path.Base(importPath)
	if v, ok := strings.CutPrefix(base, "v"); ok && path.Dir(importPath) != "." {
		if _, err := strconv.Atoi(v); err == nil {
			base = path.Base(path.Dir(importPath))
		}
	}
	base = strings.TrimPrefix(base, "go-")
	if i := strings.IndexFunc(base, func(r rune) bool {
		return r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}); i >= 0 {
		base = base[:i]
	}
	return base
}

// checkUnique reports a scope type of pkg, one of scopes as findScopes found them, whose model or table is another's
// too: one declared before it in pkg, or one of another package in idx.
func checkUnique(pkg *packages.Package, scopes []scope, idx []indexed) []diag.Diag {
	var ds []diag.Diag
	for i, sc := range scopes {
		other, at, sameModel, ok := before(scopes[:i], sc)
		if !ok {
			other, at, sameModel, ok = elsewhere(pkg.PkgPath, idx, sc)
		}
		if !ok {
			continue
		}
		of := "the table " + sc.table
		if sameModel {
			of = types.TypeString(sc.model, relativeTo(pkg.Types))
		}
		ds = append(ds, diag.Errorf(sc.pos, "%s: %s is a scope type of %s too", sc.name, other, of).
			WithHints("it is declared "+at, "a table has one scope type, and a model belongs to one table"))
	}
	return ds
}

// before finds the first of scopes whose model or table is that of sc.
func before(scopes []scope, sc scope) (other, at string, sameModel, ok bool) {
	for _, s := range scopes {
		if s.model == sc.model || s.table == sc.table {
			return s.name, fmt.Sprintf("at %s:%d", filepath.Base(s.pos.Filename), s.pos.Line), s.model == sc.model, true
		}
	}
	return "", "", false, false
}

// elsewhere finds the first scope type of idx outside the package at pkgPath whose model or table is that of sc.
func elsewhere(pkgPath string, idx []indexed, sc scope) (other, at string, sameModel, ok bool) {
	for _, o := range idx {
		sameModel := o.modelPath == sc.model.Obj().Pkg().Path() && o.modelName == sc.model.Obj().Name()
		if o.pkgPath != pkgPath && (sameModel || o.table == sc.table) {
			return o.pkgName + "." + o.name,
				fmt.Sprintf("in %s, at %s:%d", o.pkgPath, filepath.Base(o.pos.Filename), o.pos.Line), sameModel, true
		}
	}
	return "", "", false, false
}

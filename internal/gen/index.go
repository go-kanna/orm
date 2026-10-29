package gen

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/packages"
	"golang.org/x/mod/modfile"

	"github.com/go-kanna/orm/internal/decl"
)

// indexed is a //kanna:scope type of the module, as its syntax declares it.
type indexed struct {
	// pkgPath and pkgName are those of the package declaring it.
	pkgPath, pkgName, name string
	// modelPath and modelName name its model by the import path of the model's package.
	modelPath, modelName string
	table                string
	pos                  token.Position
}

// readIndex reads the //kanna:scope types of the module dir is in, from their syntax alone: type-checking the whole
// module would make every run of go generate as slow as the module is large. A scope type whose syntax does not tell
// its model is left out, for the run generating its own package to report.
func readIndex(dir string) ([]indexed, error) {
	root, modPath, err := moduleOf(dir)
	if err != nil {
		return nil, err
	}

	var idx []indexed
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(file string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && file != root && skipDir(file, d.Name()):
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
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the scope types of %s: %w", modPath, err)
	}
	return idx, nil
}

// moduleOf returns the root of the module dir is in, and the module's path.
func moduleOf(dir string) (root, modPath string, err error) {
	for d := dir; ; d = filepath.Dir(d) {
		data, err := os.ReadFile(filepath.Join(d, "go.mod")) //nolint:gosec // the go.mod of the module
		switch {
		case err == nil:
			if modPath = modfile.ModulePath(data); modPath == "" {
				return "", "", fmt.Errorf("%s names no module", filepath.Join(d, "go.mod"))
			}
			return d, modPath, nil
		case !errors.Is(err, fs.ErrNotExist):
			return "", "", fmt.Errorf("find the module of %s: %w", dir, err)
		case filepath.Dir(d) == d:
			return "", "", fmt.Errorf("no go.mod in %s or above it", dir)
		}
	}
}

// skipDir reports whether the go command leaves the directory at file, named name, out of ./...: one starting with .
// or _, testdata, vendor, and the root of another module.
func skipDir(file, name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor" {
		return true
	}
	_, err := os.Stat(filepath.Join(file, "go.mod"))
	return err == nil
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

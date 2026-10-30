package gen

import (
	"fmt"
	"go/types"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/output"
	"github.com/go-kanna/internal/packages"

	"github.com/go-kanna/orm/internal/snapshot"
)

var ColumnName = columnName

// Readable is readable, for a Go type named by its basic kind.
func Readable(kind types.BasicKind, dbType string) (string, bool) {
	return readable(types.Typ[kind], dbType)
}

// ParseTag is parseTag, with what the tag says spread out.
func ParseTag(tag string) (column string, skip bool, fk string, err error) {
	t, err := parseTag(tag)
	return t.column, t.skip, t.fk, err
}

// index reads the index of the module of pkg, with the models of the scope types //kanna:scopes implies found among
// what pkg imports.
func index(pkg *packages.Package, s snapshot.Snapshot) []indexed {
	idx, _ := readIndex(filepath.Dir(pkg.GoFiles[0]), s.Tables) // the tests of the index report its errors
	resolveModels(idx, []*packages.Package{pkg})
	return idx
}

// Scope is a scope type as findScopes finds it, its model named by the name of its package.
type Scope struct{ Name, Field, Model, Table, Func string }

func FindScopes(pkg *packages.Package, s snapshot.Snapshot) ([]Scope, []diag.Diag) {
	found, ds := findScopes(pkg, s, index(pkg, s))
	scopes := make([]Scope, 0, len(found))
	for _, sc := range found {
		model := types.TypeString(sc.model, (*types.Package).Name)
		scopes = append(scopes, Scope{Name: sc.name, Field: sc.field, Model: model, Table: sc.table, Func: sc.fn})
	}
	return scopes, ds
}

// Fields matches the fields of the models of the scope types of pkg to their columns, and returns those holding a
// column as "Model.Path type column", then those holding other models as "Model.Path type relation", with the foreign
// key their tag names.
func Fields(pkg *packages.Package, s snapshot.Snapshot) ([]string, []diag.Diag) {
	scopes, ds := findScopes(pkg, s, index(pkg, s))
	var got []string
	for _, sc := range scopes {
		f, fds := fields(pkg.Fset, sc, s.Tables[sc.table])
		ds = append(ds, fds...)
		prefix := sc.model.Obj().Name() + "."
		for _, c := range f.columns {
			got = append(got, prefix+strings.Join(c.path, ".")+" "+types.TypeString(c.typ, (*types.Package).Name)+" "+
				c.col.Name)
		}
		for _, r := range f.relations {
			line := prefix + strings.Join(r.path, ".") + " " + types.TypeString(r.typ, (*types.Package).Name) +
				" relation"
			if r.fk != "" {
				line += " fk=" + r.fk
			}
			got = append(got, line)
		}
	}
	return got, ds
}

// Index reads the index of the module dir is in, of tables, each scope type as
// "pkgPath.Name modelPath.Name table file:line", the file named from dir, and "implied" after one //kanna:scopes
// implies.
func Index(dir string, tables map[string]snapshot.Table) ([]string, error) {
	idx, err := readIndex(dir, tables)
	got := make([]string, 0, len(idx))
	for _, e := range idx {
		rel, _ := filepath.Rel(dir, e.pos.Filename)
		line := fmt.Sprintf("%s.%s %s.%s %s %s:%d",
			e.pkgPath, e.name, e.modelPath, e.modelName, e.table, filepath.ToSlash(rel), e.pos.Line)
		if e.implied {
			line += " implied"
		}
		got = append(got, line)
	}
	return got, err
}

// CheckUnique finds the scope types of pkg, and reports those whose model or table is another's too.
func CheckUnique(pkg *packages.Package, s snapshot.Snapshot) ([]diag.Diag, error) {
	idx, err := readIndex(filepath.Dir(pkg.GoFiles[0]), s.Tables)
	resolveModels(idx, []*packages.Package{pkg})
	scopes, ds := findScopes(pkg, s, idx)
	return append(ds, checkUnique(pkg, scopes, idx)...), err
}

// Links finds the foreign keys the relations of the models of the scope types of pkg follow, each as
// "Model.Path kind pkg.Target: way; keys = targetKeys".
func Links(pkg *packages.Package, s snapshot.Snapshot) ([]string, []diag.Diag, error) {
	idx, err := readIndex(filepath.Dir(pkg.GoFiles[0]), s.Tables)
	if err != nil {
		return nil, nil, err
	}
	resolveModels(idx, []*packages.Package{pkg})
	scopes, ds := findScopes(pkg, s, idx)
	l := linker{fset: pkg.Fset, pkgPath: pkg.PkgPath, pkgName: pkg.Name, scopes: scopes, tables: s.Tables, idx: idx}
	var got []string
	for _, sc := range scopes {
		f, fds := fields(pkg.Fset, sc, s.Tables[sc.table])
		ds = append(ds, fds...)
		links, lds := l.link(sc, f.columns, f.relations)
		ds = append(ds, lds...)
		for _, lk := range links {
			got = append(got, fmt.Sprintf("%s.%s %s %s.%s: %s; %s = %s", sc.model.Obj().Name(),
				strings.Join(lk.field.path, "."), lk.way.kind, lk.target.pkgName, lk.target.name, lk.way.String(),
				fieldNames(lk.keys), fieldNames(lk.targetKeys)))
		}
	}
	return got, ds, nil
}

func fieldNames(columns []column) string {
	names := make([]string, 0, len(columns))
	for _, c := range columns {
		names = append(names, strings.Join(c.path, "."))
	}
	return strings.Join(names, ", ")
}

var SQLName = sqlName

// Reserved returns the reserved words of d, sorted.
func Reserved(d snapshot.Dialect) []string {
	return slices.Sorted(maps.Keys(reserved[d]))
}

// Emit generates orm_gen.go for pkg from s, or reports why it cannot.
func Emit(pkg *packages.Package, s snapshot.Snapshot) ([]byte, []diag.Diag, error) {
	dir := filepath.Dir(pkg.GoFiles[0])
	idx, err := readIndex(dir, s.Tables)
	if err != nil {
		return nil, nil, err
	}
	resolveModels(idx, []*packages.Package{pkg})
	stmts, ds := readSQL(dir)
	plans, pds := plan(pkg, s, idx, stmts)
	if ds = append(ds, pds...); diag.HasErrors(ds) {
		return nil, ds, nil
	}
	declared := slices.Sorted(maps.Keys(output.DeclaredNames(dir, outputName, pkg.Name)))
	src, _, err := emit(pkg, s.Dialect, plans, declared)
	return src, ds, err
}

// Methods names the methods emit writes on each scope type of pkg, the scopes written by hand included, sorted.
func Methods(pkg *packages.Package, s snapshot.Snapshot) (map[string][]string, error) {
	idx, err := readIndex(filepath.Dir(pkg.GoFiles[0]), s.Tables)
	if err != nil {
		return nil, err
	}
	resolveModels(idx, []*packages.Package{pkg})
	stmts, _ := readSQL(filepath.Dir(pkg.GoFiles[0]))
	plans, _ := plan(pkg, s, idx, stmts)
	methods := map[string][]string{}
	for _, p := range plans {
		for _, m := range methodsOf(p, s.Dialect) {
			methods[p.name] = append(methods[p.name], m.name)
		}
		for _, w := range p.written {
			methods[p.name] = append(methods[p.name], w.name)
		}
		slices.Sort(methods[p.name])
	}
	return methods, nil
}

// Repositories reads the repositories of pkg, implemented in destDir, and each of their methods as
// "Repository.Method verb table", followed by what else it takes and returns, or as "Repository.Method query",
// followed by its SQL, its arguments, and its fields.
func Repositories(pkg *packages.Package, s snapshot.Snapshot, destDir string) ([]string, []diag.Diag, error) {
	idx, err := readIndex(filepath.Dir(pkg.GoFiles[0]), s.Tables)
	if err != nil {
		return nil, nil, err
	}
	dest, err := destinationAt(destDir)
	if err != nil {
		return nil, nil, err
	}
	scopes, _ := findScopes(pkg, s, idx)
	stmts, ds := readSQL(dest.dir)
	queries, qds := readQueries(stmts)
	ds = append(ds, qds...)
	r := repoReader{
		pkg: pkg, s: s, queries: queries, queriesAt: dest.pkgPath,
		imported: importedPackages([]*packages.Package{pkg}),
		l:        linker{fset: pkg.Fset, pkgPath: pkg.PkgPath, pkgName: pkg.Name, scopes: scopes, tables: s.Tables, idx: idx},
	}
	repos, rds := r.findRepositories()
	ds = append(ds, rds...)
	var got []string
	for _, repo := range repos {
		for _, m := range repo.methods {
			line := repo.name + "." + m.name
			if q := m.query; q != nil {
				line += fmt.Sprintf(" query %q args=%s %s", q.sql, strings.Join(q.args, ","),
					[]string{"error", "row", "rows", "count", "bool"}[m.result])
				if q.row != nil {
					line += " " + types.TypeString(q.row, (*types.Package).Name) + "{" + fieldNames(q.fields) + "}"
				}
				got = append(got, line)
				continue
			}
			line += " " + string(m.verb) + " " + m.target.table
			var keys []string
			for _, k := range m.keys {
				keys = append(keys, k.param+"="+k.col.col.Name)
			}
			if len(keys) > 0 {
				line += " keys=" + strings.Join(keys, ",")
			}
			if m.scoped {
				line += " scoped"
			}
			if m.row != "" {
				line += " row=" + m.row
			}
			if m.many {
				line += " many"
			}
			line += " " + []string{"error", "row", "rows", "count", "bool"}[m.result]
			if len(m.conflict) > 0 {
				line += " conflict=" + strings.Join(m.conflict, ",")
			}
			if len(m.keep) > 0 {
				line += " keep=" + strings.Join(m.keep, ",")
			}
			got = append(got, line)
		}
	}
	return got, ds, nil
}

// EmitRepositories generates orm_gen.go for the destination, the package in destDir, from the repositories of the
// package at repoPath among pkgs, running on drv, or reports why it cannot.
func EmitRepositories(pkgs []*packages.Package, repoPath string, s snapshot.Snapshot, drv, destDir string) (
	[]byte, []diag.Diag, error,
) {
	i := slices.IndexFunc(pkgs, func(p *packages.Package) bool { return p.PkgPath == repoPath })
	idx, err := readIndex(filepath.Dir(pkgs[i].GoFiles[0]), s.Tables)
	if err != nil {
		return nil, nil, err
	}
	dir, err := filepath.Abs(destDir)
	if err != nil {
		return nil, nil, fmt.Errorf("the destination: %w", err)
	}
	dest, err := destinationAt(dir)
	if err != nil {
		return nil, nil, err
	}
	stmts, ds := readSQL(dest.dir)
	repos, models, rds := planRepositories(pkgs[i], pkgs, s, idx, dest, stmts)
	ds = append(ds, rds...)
	if diag.HasErrors(ds) {
		return nil, ds, nil
	}
	src, _, err := emitRepositories(dest.pkgPath, dest.name, s.Dialect, driver(drv), repos, models, nil)
	return src, ds, err
}

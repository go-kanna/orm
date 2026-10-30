package gen

import (
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/suggest"

	"github.com/go-kanna/orm/internal/placeholder"
	"github.com/go-kanna/orm/internal/snapshot"
	"github.com/go-kanna/orm/internal/sqlfile"
)

// query is a query written by hand that implements a method of a repository.
type query struct {
	// file is the .sql file it is written in.
	file string
	// sql is the statement, its parameters the placeholders of the database, and args the parameters of the method
	// passed for them, in order.
	sql  string
	args []string
	// row is T, of a result of T or []T, and fields its fields holding the columns of the result, in their order.
	row    types.Type
	fields []column
}

// readSQL reads the statements of the .sql files in dir: the scopes written by hand next to their scope types, or the
// queries written by hand in the destination.
func readSQL(dir string) ([]sqlfile.Statement, []diag.Diag) {
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return nil, []diag.Diag{diag.Errorf(token.Position{}, "%s: %v", dir, err)}
	}
	var (
		all []sqlfile.Statement
		ds  []diag.Diag
	)
	for _, file := range files {
		data, err := os.ReadFile(file) //nolint:gosec // a .sql file of a package the run reads or writes
		if err != nil {
			ds = append(ds, diag.Errorf(token.Position{Filename: file}, "%v", err))
			continue
		}
		stmts, fds := sqlfile.Parse(file, data)
		all, ds = append(all, stmts...), append(ds, fds...)
	}
	return all, ds
}

// misplacedQueries reports the queries of stmts, those of a package declaring scope types or repositories: a query
// goes into the destination, where the method it implements is written.
func misplacedQueries(stmts []sqlfile.Statement) []diag.Diag {
	var ds []diag.Diag
	for _, st := range stmts {
		if st.Kind == sqlfile.Query && st.Name != "" {
			ds = append(ds, diag.Errorf(st.Header, "-- query: %s goes where the repositories are implemented, not next "+
				"to the declarations", st.Name).WithHints("move it into the directory -destination names"))
		}
	}
	return ds
}

// misplacedScopes reports the scopes of stmts, those of the destination, which declares no scope type.
func misplacedScopes(stmts []sqlfile.Statement) []diag.Diag {
	var ds []diag.Diag
	for _, st := range stmts {
		if st.Kind == sqlfile.Scope && st.Name != "" {
			ds = append(ds, diag.Errorf(st.Header, "-- scope: %s.%s: %s is no //kanna:scope type of this package",
				st.Type, st.Name, st.Type))
		}
	}
	return ds
}

// readQueries picks the queries written by hand out of stmts, by their names.
func readQueries(stmts []sqlfile.Statement) (map[string]sqlfile.Statement, []diag.Diag) {
	var (
		queries = map[string]sqlfile.Statement{}
		ds      []diag.Diag
	)
	for _, st := range stmts {
		if st.Kind != sqlfile.Query || st.Name == "" || st.Text == "" {
			continue
		}
		if at, ok := queries[st.Name]; ok {
			if at.Header.Filename != st.Header.Filename { // sqlfile.Parse reports one written twice in a file
				ds = append(ds, diag.Errorf(st.Header, "-- query: %s is written a second time", st.Name).
					WithHints(fmt.Sprintf("it is also at %s:%d", filepath.Base(at.Header.Filename), at.Header.Line)))
			}
			continue
		}
		queries[st.Name] = st
	}
	return queries, ds
}

// query reads the method m, fn, which the query st implements, against what describe recorded of st.
func (r repoReader) query(m *repoMethod, fn *types.Func, st sqlfile.Statement) []diag.Diag {
	errorf := func(pos token.Position, format string, args ...any) []diag.Diag {
		return []diag.Diag{diag.Errorf(pos, "%s: %s", m.name, fmt.Sprintf(format, args...))}
	}
	described, ok := r.s.Statements[r.queriesAt+"."+st.Name]
	switch {
	case !ok:
		return []diag.Diag{diag.Errorf(st.Header, "-- query: %s: the snapshot has not described it", st.Name).
			WithHints("run go tool kanna-orm describe to describe it")}
	case described.Digest != snapshot.Digest(st.Text):
		return []diag.Diag{diag.Errorf(st.Header, "-- query: %s: it has changed since the snapshot described it",
			st.Name).WithHints("run go tool kanna-orm describe to describe it again")}
	}

	sig := fn.Signature()
	params, results := sig.Params(), sig.Results()
	if params.Len() == 0 || !isNamed(params.At(0).Type(), "context", "Context") {
		return errorf(m.pos, "its first parameter is no context.Context")
	}
	shape := []diag.Diag{diag.Errorf(m.pos, "%s: a query written by hand returns error alone, (int64, error), (T, "+
		"error), or ([]T, error)", m.name).WithHints("T being a struct with a field for each column of its result")}
	if results.Len() == 0 || results.Len() > 2 ||
		!types.Identical(results.At(results.Len()-1).Type(), types.Universe.Lookup("error").Type()) {
		return shape
	}

	sql, names, err := placeholder.Rewrite(st.Text, r.s.Dialect)
	if e, ok := errors.AsType[*placeholder.Error](err); ok {
		pos := token.Position{Filename: st.Header.Filename, Line: st.Line + e.Line - 1, Column: e.Column}
		return []diag.Diag{diag.Errorf(pos, "%s", e.Message)}
	}
	if err != nil {
		return errorf(st.Header, "%v", err)
	}
	q := query{file: filepath.Base(st.Header.Filename), sql: sql}
	var ds []diag.Diag
	declared := map[string]types.Type{}
	for i := 1; i < params.Len(); i++ {
		declared[params.At(i).Name()] = params.At(i).Type()
	}
	for _, p := range described.Params {
		if t, ok := declared[p.Name]; ok {
			if want, ok := fits(t, p.Type); !ok {
				typ := typeName(t)
				ds = append(ds, diag.Errorf(st.Header, "-- query: %s: :%s is %s, which %s, %s %s, is not", st.Name,
					p.Name, p.Type, p.Name, article(typ), typ).WithHints("declare it as "+want))
			}
		}
	}
	for _, n := range names {
		if _, ok := declared[n]; !ok {
			return []diag.Diag{diag.Errorf(st.Header, "-- query: %s: :%s is none of the parameters of the method", st.Name,
				n).WithHints(fmt.Sprintf("declare it: %s(ctx context.Context, %s ...)", m.name, n))}
		}
		q.args = append(q.args, n)
	}
	for i := 1; i < params.Len(); i++ {
		if p := params.At(i).Name(); !slices.Contains(names, p) {
			ds = append(ds, diag.Warningf(m.pos, "%s: the query does not use %s", m.name, p))
		}
	}

	if results.Len() == 1 {
		m.result = resultNone
	} else {
		switch t := results.At(0).Type(); {
		case isBasic(t, types.Int64):
			m.result = resultCount
		default:
			m.result, q.row = resultRow, t
			if s, ok := t.Underlying().(*types.Slice); ok {
				m.result, q.row = resultRows, s.Elem()
			}
			if _, ok := q.row.Underlying().(*types.Struct); !ok {
				return shape
			}
		}
	}
	rowsHint := "return ([]T, error) or (T, error), T having a field for each column"
	switch {
	case m.result == resultNone && len(described.Columns) > 0:
		return []diag.Diag{diag.Errorf(m.pos, "%s: the query returns columns, but the method returns error alone",
			m.name).WithHints(rowsHint)}
	case m.result == resultCount && len(described.Columns) > 0:
		return []diag.Diag{diag.Errorf(m.pos, "%s: the query returns columns, but (int64, error) is the number of "+
			"rows it affects", m.name).WithHints(rowsHint)}
	case m.result == resultRow || m.result == resultRows:
		fields, fds := resultFields(r.pkg.Fset, q.row, described.Columns, st.Header, m.name)
		if diag.HasErrors(fds) {
			return append(ds, fds...)
		}
		q.fields = fields
	}
	m.query = &q
	return ds
}

// resultFields matches the fields of row, a struct, to columns, the result of a query written by hand at header, for
// the method name: one for each column, by its name or its tag, in the order of the columns.
func resultFields(fset *token.FileSet, row types.Type, columns []snapshot.Result, header token.Position,
	name string,
) ([]column, []diag.Diag) {
	errorf := func(format string, args ...any) diag.Diag {
		return diag.Errorf(header, "-- query: %s: %s", name, fmt.Sprintf(format, args...))
	}
	type field struct {
		path   []string
		typ    types.Type
		pos    token.Position
		column string
	}
	var (
		all []field
		ds  []diag.Diag
	)
	var walk func(st *types.Struct, path []string)
	walk = func(st *types.Struct, path []string) {
		for i := range st.NumFields() {
			f := st.Field(i)
			tag, err := parseTag(st.Tag(i))
			fpath := append(slices.Clip(path), f.Name())
			switch t := f.Type(); {
			case err != nil:
				ds = append(ds, diag.Errorf(fset.Position(f.Pos()), "%s: %v", strings.Join(fpath, "."), err))
			case f.Name() == "_", tag.skip:
			case !f.Exported():
				ds = append(ds, diag.Errorf(fset.Position(f.Pos()), "%s: the code kanna-orm writes cannot reach an "+
					"unexported field", strings.Join(fpath, ".")).WithHints(`export it, or leave it out with orm:"-"`))
			case f.Embedded() && isModel(t) && reflect.StructTag(st.Tag(i)).Get("orm") == "":
				walk(t.Underlying().(*types.Struct), fpath) //nolint:forcetypeassert // a model is a struct
			case holdsColumn(t):
				column := tag.column
				if column == "" {
					column = columnName(f.Name())
				}
				all = append(all, field{path: fpath, typ: t, pos: fset.Position(f.Pos()), column: column})
			}
		}
	}
	walk(row.Underlying().(*types.Struct), nil) //nolint:forcetypeassert // the caller has made sure
	if diag.HasErrors(ds) {
		return nil, ds
	}

	names := make([]string, 0, len(all))
	for _, f := range all {
		names = append(names, f.column)
	}
	var matched []column
	for _, c := range columns {
		i := slices.IndexFunc(all, func(f field) bool { return f.column == c.Name })
		if i < 0 {
			d := errorf("%s has no field for the column %s", typeName(row), c.Name)
			if near, ok := suggest.Closest(c.Name, names); ok {
				d = d.WithHints(fmt.Sprintf("did you mean the field holding %s?", near))
			} else {
				d = d.WithHints(fmt.Sprintf("give %s a field for it, or name the column after one with AS", typeName(row)))
			}
			ds = append(ds, d)
			continue
		}
		f := all[i]
		typ := typeName(f.typ)
		if c.Nullable && !nilable(f.typ) {
			ds = append(ds, errorf("the column %s may be NULL, which %s.%s, %s %s, cannot hold", c.Name,
				typeName(row), strings.Join(f.path, "."), article(typ), typ).WithHints("declare it as *"+typ))
			continue
		}
		if want, ok := readable(f.typ, c.Type); !ok {
			ds = append(ds, errorf("the column %s is %s, which %s.%s, %s %s, cannot hold", c.Name, c.Type,
				typeName(row), strings.Join(f.path, "."), article(typ), typ).WithHints("declare it as "+want))
			continue
		}
		matched = append(matched, column{path: f.path, typ: f.typ, col: snapshot.Column{Name: c.Name}, pos: f.pos})
	}
	for _, f := range all {
		if !slices.ContainsFunc(columns, func(c snapshot.Result) bool { return c.Name == f.column }) {
			ds = append(ds, errorf("the query returns no column %s for %s.%s", f.column, typeName(row),
				strings.Join(f.path, ".")).WithHints(`select it, or leave the field out with orm:"-"`))
		}
	}
	return matched, ds
}

// unusedQueries reports the queries of queries that none of methods, those of the repositories of the package at
// pkgPath, is named after.
func unusedQueries(queries map[string]sqlfile.Statement, pkgPath string, methods []string) []diag.Diag {
	var ds []diag.Diag
	for _, name := range slices.Sorted(maps.Keys(queries)) {
		if slices.Contains(methods, name) {
			continue
		}
		d := diag.Errorf(queries[name].Header, "-- query: %s is no method of a //kanna:repository interface of %s",
			name, pkgPath)
		if near, ok := suggest.Closest(name, methods); ok {
			d = d.WithHints("did you mean " + near + "?")
		}
		ds = append(ds, d)
	}
	return ds
}

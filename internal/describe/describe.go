package describe

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/packages"
	"github.com/go-kanna/internal/suggest"
	"github.com/jackc/pgx/v5"

	"github.com/go-kanna/orm/internal/decl"
	"github.com/go-kanna/orm/internal/placeholder"
	"github.com/go-kanna/orm/internal/snapshot"
	"github.com/go-kanna/orm/internal/sqlfile"
)

// Database is what describe asks of a database.
type Database interface {
	Dialect() snapshot.Dialect
	Tables(ctx context.Context) (map[string]snapshot.Table, error)
	// Statement describes a statement written with :name parameters.
	Statement(ctx context.Context, sql string) (snapshot.Statement, error)
}

// Postgres returns the PostgreSQL database conn is connected to, its tables being those of the current schema.
func Postgres(conn *pgx.Conn) Database { return postgresDB{conn} }

// MySQL returns the MySQL database conn uses.
func MySQL(conn *sql.Conn) Database { return mysqlDB{conn} }

type postgresDB struct{ conn *pgx.Conn }

func (postgresDB) Dialect() snapshot.Dialect { return snapshot.PostgreSQL }

func (db postgresDB) Tables(ctx context.Context) (map[string]snapshot.Table, error) {
	return postgresTables(ctx, db.conn)
}

func (db postgresDB) Statement(ctx context.Context, sql string) (snapshot.Statement, error) {
	return postgresStatement(ctx, db.conn, sql)
}

type mysqlDB struct{ conn *sql.Conn }

func (mysqlDB) Dialect() snapshot.Dialect { return snapshot.MySQL }

func (db mysqlDB) Tables(ctx context.Context) (map[string]snapshot.Table, error) {
	return mysqlTables(ctx, db.conn)
}

func (db mysqlDB) Statement(ctx context.Context, sql string) (snapshot.Statement, error) {
	return mysqlStatement(ctx, db.conn, sql)
}

// Describe asks db of its tables and of the statements written by hand in the .sql files next to the
// //kanna:repository interfaces and the //kanna:scope types of pkgs.
func Describe(ctx context.Context, db Database, pkgs []*packages.Package) (snapshot.Snapshot, []diag.Diag) {
	s := snapshot.Snapshot{Dialect: db.Dialect(), Statements: map[string]snapshot.Statement{}}

	tables, err := db.Tables(ctx)
	if err != nil {
		return s, []diag.Diag{diag.Errorf(token.Position{}, "read the tables: %v", err)}
	}
	s.Tables = tables

	found, ds := statements(pkgs, tables)
	if diag.HasErrors(ds) {
		return s, ds
	}
	for _, f := range found {
		st, d := describe(ctx, db, f)
		if d != nil {
			ds = append(ds, *d)
			continue
		}
		s.Statements[f.key] = st
	}
	return s, ds
}

// A found statement is one of a .sql file, with the name the snapshot keeps it by and, for a scope, its table.
type found struct {
	key   string
	stmt  sqlfile.Statement
	table string
}

func describe(ctx context.Context, db Database, f found) (snapshot.Statement, *diag.Diag) {
	text, offset := f.stmt.Text, 0
	if f.stmt.Kind == sqlfile.Scope {
		text, offset = "SELECT 1 FROM "+quoteTable(db.Dialect(), f.table)+" WHERE (\n"+text+"\n)", 1
	}

	st, err := db.Statement(ctx, text)
	if e, ok := errors.AsType[*placeholder.Error](err); ok {
		pos := token.Position{Filename: f.stmt.Header.Filename, Line: f.stmt.Line + e.Line - 1 - offset, Column: e.Column}
		d := diag.Errorf(pos, "%s", e.Message)
		return st, &d
	}
	if err != nil {
		d := diag.Errorf(f.stmt.Header, "%s %s: %v", f.stmt.Kind, qualified(f.stmt), err)
		return st, &d
	}
	if f.stmt.Kind == sqlfile.Scope {
		st.Columns = nil
	}
	st.Digest = snapshot.Digest(f.stmt.Text)
	return st, nil
}

// statements finds the statements of the .sql files next to the //kanna:repository interfaces and the scope types of
// pkgs, those //kanna:scopes implies of tables included. The .sql files of a package with neither are none of
// kanna-orm's.
func statements(pkgs []*packages.Package, tables map[string]snapshot.Table) ([]found, []diag.Diag) {
	var (
		all []found
		ds  []diag.Diag
	)
	for _, pkg := range pkgs {
		if len(pkg.GoFiles) == 0 {
			continue
		}
		scopes, repository, pds := declarations(pkg, tables)
		ds = append(ds, pds...)
		if len(scopes) == 0 && !repository {
			continue
		}

		files, err := filepath.Glob(filepath.Join(filepath.Dir(pkg.GoFiles[0]), "*.sql"))
		if err != nil {
			ds = append(ds, diag.Errorf(token.Position{}, "%s: %v", pkg.PkgPath, err))
			continue
		}
		seen := map[string]token.Position{}
		for _, file := range files {
			data, err := os.ReadFile(file) //nolint:gosec // a .sql file beside the sources of a package loaded
			if err != nil {
				ds = append(ds, diag.Errorf(token.Position{Filename: file}, "%v", err))
				continue
			}
			stmts, fds := sqlfile.Parse(file, data)
			ds = append(ds, fds...)
			for _, st := range stmts {
				f, d := place(pkg.PkgPath, st, scopes, repository)
				if d != nil {
					ds = append(ds, *d)
					continue
				}
				if at, ok := seen[f.key]; ok {
					ds = append(ds, diag.Errorf(st.Header, "-- %s: %s is written a second time", st.Kind, qualified(st)).
						WithHints(fmt.Sprintf("it is also at %s:%d", filepath.Base(at.Filename), at.Line)))
					continue
				}
				seen[f.key] = st.Header
				all = append(all, f)
			}
		}
	}
	return all, ds
}

// place names a statement of the package at path, and finds the table of a scope.
func place(path string, st sqlfile.Statement, scopes map[string]string, repository bool) (found, *diag.Diag) {
	if st.Kind == sqlfile.Query {
		if !repository {
			d := diag.Errorf(st.Header, "-- query: %s is next to no //kanna:repository interface", st.Name)
			return found{}, &d
		}
		return found{key: path + "." + st.Name, stmt: st}, nil
	}
	table, ok := scopes[st.Type]
	if !ok {
		d := diag.Errorf(st.Header, "-- scope: %s.%s: %s is no //kanna:scope type of this package", st.Type, st.Name, st.Type)
		if near, ok := suggest.Closest(st.Type, slices.Sorted(maps.Keys(scopes))); ok {
			d = d.WithHints("did you mean " + near + "?")
		}
		return found{}, &d
	}
	return found{key: path + "." + qualified(st), stmt: st, table: table}, nil
}

// qualified names a scope by its type and its method, and a query by its method alone.
func qualified(st sqlfile.Statement) string {
	if st.Kind == sqlfile.Scope {
		return st.Type + "." + st.Name
	}
	return st.Name
}

// declarations reads the tables of the scope types of pkg, by their names, those //kanna:scopes implies of tables
// included, and whether it has a //kanna:repository interface.
func declarations(pkg *packages.Package, tables map[string]snapshot.Table) (map[string]string, bool, []diag.Diag) {
	var (
		scopes     = map[string]string{}
		models     []decl.Models
		repository bool
		ds         []diag.Diag
	)
	for _, file := range pkg.Syntax {
		decls, fds := decl.Read(pkg.Fset, file)
		ds = append(ds, fds...)
		for _, sc := range decls.Scopes {
			scopes[sc.Spec.Name.Name] = sc.Table
		}
		models = append(models, decls.Models...)
		repository = repository || len(decls.Repositories) > 0
	}
	if pkg.Module == nil {
		return scopes, repository, ds
	}
	dirOf := func(path string) (string, bool) {
		if path == pkg.Module.Path {
			return pkg.Module.Dir, true
		}
		rel, ok := strings.CutPrefix(path, pkg.Module.Path+"/")
		return filepath.Join(pkg.Module.Dir, filepath.FromSlash(rel)), ok
	}
	hasTable := func(table string) bool { _, ok := tables[table]; return ok }
	for _, m := range models {
		for _, im := range decl.Imply(m, dirOf, hasTable) {
			if _, ok := scopes[im.Model]; !ok {
				scopes[im.Model] = im.Table
			}
		}
	}
	return scopes, repository, ds
}

// quoteTable quotes each part of a table's name, which may name its schema too.
func quoteTable(dialect snapshot.Dialect, table string) string {
	q := `"`
	if dialect == snapshot.MySQL {
		q = "`"
	}
	parts := strings.Split(table, ".")
	for i, p := range parts {
		parts[i] = q + strings.ReplaceAll(p, q, q+q) + q
	}
	return strings.Join(parts, ".")
}

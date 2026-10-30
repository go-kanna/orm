// Package decl reads, from the syntax of a file alone, what is declared there for kanna-orm: the types marked
// //kanna:scope, the packages of models //kanna:scopes implies scope types for, and the interfaces marked
// //kanna:repository.
package decl

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/directive"
)

// Scope is a type marked //kanna:scope.
type Scope struct {
	Spec *ast.TypeSpec
	// Pos is where the directive is.
	Pos   token.Position
	Table string
	// Func names the function returning the empty scope, when func= does.
	Func string
}

// Models is a //kanna:scopes directive, which implies a scope type for each struct of the packages it names that a
// table is named after.
type Models struct {
	// Pos is where the directive is.
	Pos   token.Position
	Paths []string
}

// Repository is an interface marked //kanna:repository.
type Repository struct {
	Spec *ast.TypeSpec
	// Pos is where the directive is.
	Pos token.Position
	// Table is the table of the methods that tell none themselves, when table= names one.
	Table string
}

// Decls is what a file declares for kanna-orm.
type Decls struct {
	Scopes       []Scope
	Models       []Models
	Repositories []Repository
}

// Read reads the declarations of file. A scope type or a repository whose directive cannot be read is reported, and
// left out.
func Read(fset *token.FileSet, file *ast.File) (Decls, []diag.Diag) {
	var (
		decls Decls
		ds    []diag.Diag
		docs  = map[*ast.CommentGroup]bool{}
	)
	for _, node := range file.Decls {
		gd, ok := node.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		docs[gd.Doc] = true
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			doc := ts.Doc
			if doc == nil && len(gd.Specs) == 1 {
				doc = gd.Doc
			}
			docs[doc] = true

			d, fds := directive.Find(fset, doc, "scope")
			ds = append(ds, fds...)
			if d.Found {
				table, fn, err := scopeArgs(d.Args)
				if err != nil {
					ds = append(ds, diag.Errorf(d.Pos, "%v", err).WithHints("as in //kanna:scope table=orders"))
				} else {
					decls.Scopes = append(decls.Scopes, Scope{Spec: ts, Pos: d.Pos, Table: table, Func: fn})
				}
			}

			d, fds = directive.Find(fset, doc, "repository")
			ds = append(ds, fds...)
			if d.Found {
				table, err := repositoryArgs(d.Args)
				if err != nil {
					ds = append(ds, diag.Errorf(d.Pos, "%v", err).WithHints("as in //kanna:repository table=orders"))
				} else {
					decls.Repositories = append(decls.Repositories, Repository{Spec: ts, Pos: d.Pos, Table: table})
				}
			}
		}
	}

	// //kanna:scopes marks no declaration, so it stands in a comment of its own.
	for _, cg := range file.Comments {
		if docs[cg] {
			continue
		}
		d, fds := directive.Find(fset, cg, "scopes")
		ds = append(ds, fds...)
		if !d.Found {
			continue
		}
		paths := strings.Fields(d.Args)
		if len(paths) == 0 {
			ds = append(ds, diag.Errorf(d.Pos, "//kanna:scopes wants the packages of the models").
				WithHints("as in //kanna:scopes example.com/shop/model"))
			continue
		}
		decls.Models = append(decls.Models, Models{Pos: d.Pos, Paths: paths})
	}
	return decls, ds
}

// scopeArgs reads what follows //kanna:scope: the table, and the name func= gives the function returning the empty
// scope, if any.
func scopeArgs(args string) (table, fn string, err error) {
	for field := range strings.FieldsSeq(args) {
		key, value, _ := strings.Cut(field, "=")
		switch {
		case key == "table" && table != "", key == "func" && fn != "":
			return "", "", fmt.Errorf("//kanna:scope gives %s= twice", key)
		case key == "table" && value != "":
			table = value
		case key == "func" && token.IsIdentifier(value):
			fn = value
		case key == "func":
			return "", "", fmt.Errorf("//kanna:scope func=%s: %q is not a name a Go function can have", value, value)
		default:
			return "", "", fmt.Errorf("//kanna:scope takes table= and func=, not %q", field)
		}
	}
	if table == "" {
		return "", "", errors.New("//kanna:scope wants the table its type is a scope of")
	}
	return table, fn, nil
}

// repositoryArgs reads what follows //kanna:repository: the table table= names, if any.
func repositoryArgs(args string) (table string, err error) {
	for field := range strings.FieldsSeq(args) {
		key, value, _ := strings.Cut(field, "=")
		switch {
		case key == "table" && table != "":
			return "", errors.New("//kanna:repository gives table= twice")
		case key == "table" && value != "":
			table = value
		default:
			return "", fmt.Errorf("//kanna:repository takes table=, not %q", field)
		}
	}
	return table, nil
}

// Implied is a scope type a //kanna:scopes directive implies: one named after the struct it holds, Model, of the
// package at ModelPath, whose rows are those of Table.
type Implied struct {
	Model, ModelPath, Table string
	// Pos is where the directive is.
	Pos token.Position
}

// Imply implies the scope types of m, a directive of the module at root whose path is modPath: one for each struct of
// the packages it names that a table hasTable reports is named after. A package of another module is left out.
func Imply(m Models, root, modPath string, hasTable func(table string) bool) []Implied {
	var implied []Implied
	for _, path := range m.Paths {
		importPath, dir, ok := m.Resolve(path, root, modPath)
		if !ok {
			continue
		}
		for _, name := range structs(dir) {
			if table, ok := TableOf(name, hasTable); ok {
				implied = append(implied, Implied{Model: name, ModelPath: importPath, Table: table, Pos: m.Pos})
			}
		}
	}
	return implied
}

// Resolve tells the import path and the directory of the package path names in m, a directive of the module at root
// whose path is modPath: an import path, or a path relative to the directory of the file m is in, as ../model is.
// It reports a package of another module.
func (m Models) Resolve(path, root, modPath string) (importPath, dir string, ok bool) {
	if path == "." || path == ".." || strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") {
		dir = filepath.Join(filepath.Dir(m.Pos.Filename), filepath.FromSlash(path))
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", "", false
		}
		return strings.TrimSuffix(modPath+"/"+filepath.ToSlash(rel), "/."), dir, true
	}
	if path == modPath {
		return path, root, true
	}
	rel, ok := strings.CutPrefix(path, modPath+"/")
	if !ok {
		return "", "", false
	}
	return path, filepath.Join(root, filepath.FromSlash(rel)), true
}

// TableOf names the table a struct called name holds the rows of, of those hasTable reports: name in snake case with
// its last word made plural, as order_lines for OrderLine, categories for Category, and people for Person, or else as
// it is.
func TableOf(name string, hasTable func(table string) bool) (string, bool) {
	snake := SnakeCase(name)
	for _, table := range []string{plural(snake), snake} {
		if hasTable(table) {
			return table, true
		}
	}
	return "", false
}

// irregular are the plurals of English nouns no rule makes.
var irregular = map[string]string{
	"person": "people", "child": "children", "man": "men", "woman": "women", "mouse": "mice", "goose": "geese",
	"foot": "feet", "tooth": "teeth", "datum": "data", "medium": "media", "criterion": "criteria",
	"analysis": "analyses", "leaf": "leaves", "life": "lives", "knife": "knives", "wife": "wives", "half": "halves",
}

// plural makes the last word of a name in snake case plural.
func plural(word string) string {
	head, last := "", word
	if i := strings.LastIndexByte(word, '_'); i >= 0 {
		head, last = word[:i+1], word[i+1:]
	}
	if p, ok := irregular[last]; ok {
		return head + p
	}
	switch {
	case strings.HasSuffix(word, "y") && len(word) > 1 && !strings.ContainsRune("aeiou", rune(word[len(word)-2])):
		return strings.TrimSuffix(word, "y") + "ies"
	case strings.HasSuffix(word, "s"), strings.HasSuffix(word, "x"), strings.HasSuffix(word, "z"),
		strings.HasSuffix(word, "ch"), strings.HasSuffix(word, "sh"):
		return word + "es"
	}
	return word + "s"
}

// SnakeCase writes name in snake case. A word starts at an upper-case letter after a lower-case one or a digit, and at
// the last of a run of upper-case letters that a lower-case one follows, so that CustomerID is customer_id and
// HTTPServer http_server. There is no list of acronyms to consult: OAuthToken is o_auth_token.
func SnakeCase(name string) string {
	runes := []rune(name)
	var b strings.Builder
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// structs names the exported structs the Go files in dir declare, but its tests, which a //kanna:scopes directive may
// imply scope types for.
func structs(dir string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	var names []string
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			continue // the build of its own package reports it
		}
		for _, node := range f.Decls {
			gd, ok := node.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() || ts.TypeParams != nil || ts.Assign.IsValid() {
					continue
				}
				if _, ok := ts.Type.(*ast.StructType); ok {
					names = append(names, ts.Name.Name)
				}
			}
		}
	}
	return names
}

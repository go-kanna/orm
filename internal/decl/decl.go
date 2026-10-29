// Package decl reads, from the syntax of a file alone, what is declared there for kanna-orm: the types marked
// //kanna:scope, and the interfaces marked //kanna:repository.
package decl

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"strings"

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

// Decls is what a file declares for kanna-orm.
type Decls struct {
	Scopes       []Scope
	Repositories []*ast.TypeSpec
}

// Read reads the declarations of file. A scope type whose directive cannot be read is reported, and left out.
func Read(fset *token.FileSet, file *ast.File) (Decls, []diag.Diag) {
	var (
		decls Decls
		ds    []diag.Diag
	)
	for _, node := range file.Decls {
		gd, ok := node.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			doc := ts.Doc
			if doc == nil && len(gd.Specs) == 1 {
				doc = gd.Doc
			}

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
				decls.Repositories = append(decls.Repositories, ts)
			}
		}
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

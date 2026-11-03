package gen

import (
	"go/ast"
	"go/token"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/directive"

	"github.com/go-kanna/orm/internal/decl"
)

// methodDirectives are the directives written right above a method of a //kanna:repository, each with what a method
// whose SQL is written elsewhere should do in its place.
var methodDirectives = []struct{ key, instead string }{
	{"conflict", "handle the conflict"},
	{"lock", "lock"},
	{"pluck", "read the column"},
}

// hasNoGeneratedSQL is the error for a method directive d of key on a method named name, whose SQL is written by hand
// in where.
func hasNoGeneratedSQL(d directive.Directive, key, name, where string) diag.Diag {
	instead := key
	for _, md := range methodDirectives {
		if md.key == key {
			instead = md.instead
		}
	}
	return diag.Errorf(d.Pos, "%s: //kanna:%s is for a method kanna-orm writes the SQL of", name, key).
		WithHints(instead + " in " + where)
}

// strayDirectives reports the method directives of file on no method of repos, such as one at the end of a line or
// parted from a method by a blank line. Nothing reads them, so the method would go on without them, without a word.
func strayDirectives(fset *token.FileSet, file *ast.File, repos []decl.Repository) []diag.Diag {
	docs := map[*ast.CommentGroup]bool{}
	for _, repo := range repos {
		it, ok := repo.Spec.Type.(*ast.InterfaceType)
		if !ok {
			continue
		}
		for _, f := range it.Methods.List {
			if len(f.Names) > 0 {
				docs[f.Doc] = true
			}
		}
	}

	var ds []diag.Diag
	for _, cg := range file.Comments {
		if docs[cg] {
			continue
		}
		for _, md := range methodDirectives {
			d, fds := directive.Find(fset, cg, md.key)
			ds = append(ds, fds...)
			if d.Found {
				ds = append(ds, diag.Errorf(d.Pos, "//kanna:%s marks no method of a //kanna:repository", md.key).
					WithHints("write it right above the method"))
			}
		}
	}
	return ds
}

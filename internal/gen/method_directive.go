package gen

import (
	"go/ast"
	"go/token"
	"slices"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/directive"

	"github.com/go-kanna/orm/internal/decl"
)

// methodDirectives are the directives written right above a method of a //kanna:repository, each with what a method
// whose SQL is written elsewhere should do in its place.
var methodDirectives = []struct{ key, instead string }{
	{"conflict", "handle the conflict"},
	{"lock", "lock"},
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

// strayDirectives warns of the method directives of file on a method of an interface other than repos. One in no doc
// comment, as one at the end of a line or parted from a method by a blank line, is directive.Loose's to tell; one in
// the doc of something other than a method of an interface, decl.Misplaced's. Nothing reads them, so the method would
// go on without them.
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

	keys := make([]string, 0, len(methodDirectives))
	for _, md := range methodDirectives {
		keys = append(keys, md.key)
	}
	lines := directive.Lines(fset, []*ast.File{file})
	ds := directive.Loose(lines, keys...)
	for _, cg := range file.Comments {
		if docs[cg] {
			continue
		}
		for _, key := range keys {
			d, fds := directive.Find(fset, cg, key)
			ds = append(ds, fds...)
			if d.Found && slices.ContainsFunc(lines, func(l directive.Line) bool {
				return l.Pos == d.Pos && l.Shape(nil) == directive.InterfaceMethod
			}) {
				ds = append(ds, diag.Warningf(d.Pos, "//kanna:%s marks no method of a //kanna:repository", key).
					WithHints("write it right above the method"))
			}
		}
	}
	return ds
}

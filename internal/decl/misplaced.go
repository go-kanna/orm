package decl

import (
	"go/ast"
	"go/token"
	"go/types"
	"slices"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/directive"
)

// marks are the shapes of what the keys of kanna-orm mark, those of the doc of a declaration. //kanna:scopes stands
// in a comment of its own, and //kanna:pluck is no more.
var marks = map[string]directive.Shape{
	"scope":      directive.StructType,
	"repository": directive.InterfaceType,
	"conflict":   directive.InterfaceMethod,
	"lock":       directive.InterfaceMethod,
}

// Misplaced tells each directive of file, a file of pkg, in the doc of a declaration of a shape its key does not mark,
// as //kanna:repository of a struct: nothing reads it. They are told in the order they are written.
func Misplaced(fset *token.FileSet, file *ast.File, pkg *types.Package) []diag.Diag {
	var ds []diag.Diag
	for _, l := range directive.Lines(fset, []*ast.File{file}) {
		if shape, ok := marks[l.Key]; ok {
			ds = append(ds, directive.Misplaced([]directive.Line{l}, pkg, shape, l.Key)...)
		}
	}
	return ds
}

// IsMisplaced tells whether one of ds, as Misplaced tells them, is of the directive at pos.
func IsMisplaced(ds []diag.Diag, pos token.Position) bool {
	return slices.ContainsFunc(ds, func(d diag.Diag) bool { return d.Pos == pos })
}

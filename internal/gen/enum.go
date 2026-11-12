package gen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"sync"

	"github.com/go-kanna/internal/directive"

	"github.com/go-kanna/orm/internal/snapshot"
)

// jsonEnum returns the enum of kanna-value that t is, points to, or a value type of kanna-value holds, when dbType, a
// type of d, is json: none of its values is JSON text, which the database rejects when it is written, and its Scan
// rejects the JSON text it is read from.
func jsonEnum(fset *token.FileSet, t types.Type, dbType string, d snapshot.Dialect) (*types.Named, bool) {
	if !mysqlJSON(d, dbType) && !postgresJSON(d, dbType) {
		return nil, false
	}
	named, ok := types.Unalias(driverType(t)).(*types.Named)
	if !ok || !isEnum(fset, named) {
		return nil, false
	}
	return named, true
}

// isEnum reports whether named is an enum of kanna-value: a string type marked //kanna:enum, as kanna-value vet tells
// one. Its package may be read from export data, which keeps no doc comment, so the mark is read from the file
// declaring it.
func isEnum(fset *token.FileSet, named *types.Named) bool {
	if named.TypeParams().Len() > 0 || !types.Identical(named.Underlying(), types.Typ[types.String]) {
		return false
	}
	name := fset.Position(named.Obj().Pos()).Filename
	return name != "" && sourceEnums(fset, name)[named.Obj().Name()]
}

// sourceEnumCache holds, of each file read, the names of the types it declares marked //kanna:enum.
var sourceEnumCache sync.Map // sourceFile → func() map[string]bool

// sourceEnums tells the names of the types the file named name, of the packages fset was loaded with, declares marked
// //kanna:enum: none when it cannot be read.
func sourceEnums(fset *token.FileSet, name string) map[string]bool {
	load, _ := sourceEnumCache.LoadOrStore(sourceFile{fset, name}, sync.OnceValue(func() map[string]bool {
		fs := token.NewFileSet()
		file, err := parser.ParseFile(fs, name, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil
		}
		enums := map[string]bool{}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts := spec.(*ast.TypeSpec) //nolint:forcetypeassert // a type declaration declares nothing else
				doc := ts.Doc
				if doc == nil && len(gd.Specs) == 1 {
					doc = gd.Doc
				}
				if d, _ := directive.Find(fs, doc, "enum"); d.Found {
					enums[ts.Name.Name] = true
				}
			}
		}
		return enums
	}))
	return load.(func() map[string]bool)() //nolint:forcetypeassert // the cache holds nothing else
}

// enumHints are the hints for t when it cannot hold dbType, a type of d, being an enum of kanna-value on json. q
// qualifies the names of types, as the report does.
func enumHints(fset *token.FileSet, t types.Type, dbType string, d snapshot.Dialect, q types.Qualifier) []string {
	named, ok := jsonEnum(fset, t, dbType, d)
	if !ok {
		return nil
	}
	return []string{types.TypeString(named, q) + " is an enum of kanna-value, none of whose values is JSON text: " +
		"hold it in a column of text, such as text or varchar"}
}

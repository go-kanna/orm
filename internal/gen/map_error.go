package gen

import (
	"go/ast"
	"slices"

	"github.com/go-kanna/internal/diag"
)

// mapErrorName is the method of an implementation, written by hand, that the errors its generated methods return go
// through.
const mapErrorName = "mapError"

// mapErrorSignature reports whether m, the mapError method of implementation, the implementation of repo, takes the
// operation of repo an error comes from besides the error, and what is wrong with it unless it takes an error, or the
// operation and an error, and returns one.
func mapErrorSignature(repo repository, implementation string, m handMethod) (bool, []diag.Diag) {
	params := fieldTypes(m.typ.Params)
	if results := fieldTypes(m.typ.Results); len(results) == 1 && isIdent(results[0], "error") {
		switch {
		case len(params) == 1 && isIdent(params[0], "error"):
			return false, nil
		case len(params) == 2 && isIdent(params[0], repo.operation()) && isIdent(params[1], "error"):
			return true, nil
		}
	}
	as := "func (" + implementation + ") " + mapErrorName
	return false, []diag.Diag{diag.Errorf(m.pos, "%s.%s: the errors of %s go through it, so it takes an error, or the "+
		"operation of %s it comes from and the error, and returns one", implementation, mapErrorName, repo.name,
		repo.name).WithHints("as in "+as+"(err error) error", "or "+as+"(op "+repo.operation()+", err error) error")}
}

// fieldTypes are the types of fields, one for each name, or for each field without one.
func fieldTypes(fields *ast.FieldList) []ast.Expr {
	if fields == nil {
		return nil
	}
	var types []ast.Expr
	for _, f := range fields.List {
		types = append(types, slices.Repeat([]ast.Expr{f.Type}, max(len(f.Names), 1))...)
	}
	return types
}

// isIdent reports whether t is the identifier name.
func isIdent(t ast.Expr, name string) bool {
	id, ok := t.(*ast.Ident)
	return ok && id.Name == name
}

// mapped is err, an error a method returns, through the mapError method of the implementation if it has one, with the
// operation of the method if mapError takes it.
func (s signature) mapped(err string) string {
	switch {
	case s.recv == "":
		return err
	case s.op != "":
		return s.recv + "." + mapErrorName + "(" + s.op + ", " + err + ")"
	}
	return s.recv + "." + mapErrorName + "(" + err + ")"
}

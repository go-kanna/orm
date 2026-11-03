package gen

import (
	"go/ast"

	"github.com/go-kanna/internal/diag"
)

// mapErrorName is the method of an implementation, written by hand, that the errors its generated methods return go
// through.
const mapErrorName = "mapError"

// mapErrorSignature reports m, the mapError method of implementation, the implementation of repo, unless it takes an
// error and returns one.
func mapErrorSignature(repo repository, implementation string, m handMethod) []diag.Diag {
	if isError(m.typ.Params) && isError(m.typ.Results) {
		return nil
	}
	receiver := implementation
	if repo.local {
		receiver = "*" + receiver
	}
	return []diag.Diag{diag.Errorf(m.pos, "%s.%s: the errors of %s go through it, so it takes an error and returns one",
		implementation, mapErrorName, repo.name).WithHints("as in func (" + receiver + ") " + mapErrorName +
		"(err error) error")}
}

// isError reports whether fields are a single error.
func isError(fields *ast.FieldList) bool {
	if fields == nil || len(fields.List) != 1 || len(fields.List[0].Names) > 1 {
		return false
	}
	id, ok := fields.List[0].Type.(*ast.Ident)
	return ok && id.Name == "error"
}

// mapped is err, an error a method returns, through the mapError method of the implementation if it has one.
func (s signature) mapped(err string) string {
	if s.recv == "" {
		return err
	}
	return s.recv + "." + mapErrorName + "(" + err + ")"
}

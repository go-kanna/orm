package gen

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/directive"
)

type binding struct {
	accessor, localDBTX bool
}

type databaseTypeRef struct {
	path, name string
	pointer    bool
}

func parseDatabaseType(text string) (databaseTypeRef, error) {
	ref := databaseTypeRef{pointer: strings.HasPrefix(text, "*")}
	text = strings.TrimPrefix(text, "*")
	i := strings.LastIndex(text, ".")
	if i <= 0 || !ast.IsExported(text[i+1:]) || !token.IsIdentifier(text[i+1:]) {
		return ref, errors.New("-constructor-db wants a qualified type, as *example.com/app/db.Reader")
	}
	ref.path, ref.name = text[:i], text[i+1:]
	return ref, nil
}

func databaseType(text string, imported map[string]*types.Package, drv driver) (types.Type, error) {
	ref, err := parseDatabaseType(text)
	if err != nil {
		return nil, err
	}
	pkg := imported[ref.path]
	if pkg == nil {
		return nil, fmt.Errorf("-constructor-db: package %s is not loaded", ref.path)
	}
	obj, ok := pkg.Scope().Lookup(ref.name).(*types.TypeName)
	if !ok {
		return nil, fmt.Errorf("-constructor-db: %s.%s is not a type", ref.path, ref.name)
	}
	input := obj.Type()
	if ref.pointer {
		input = types.NewPointer(input)
	}
	if !acceptsDatabase(input, imported, drv) {
		return nil, fmt.Errorf("-constructor-db: %s does not implement DBTX for %s", text, drv)
	}
	return input, nil
}

// acceptsDatabase compares the input with the driver's own statement methods, without requiring the rest of it.
func acceptsDatabase(input types.Type, imported map[string]*types.Package, drv driver) bool {
	path, name := "github.com/jackc/pgx/v5", "Conn"
	methods := []string{"Exec", "Query", "QueryRow"}
	if drv == driverSQL {
		path, name = "database/sql", "DB"
		methods = []string{"ExecContext", "QueryContext", "QueryRowContext"}
	}
	pkg := imported[path]
	if pkg == nil || pkg.Scope().Lookup(name) == nil {
		return false
	}
	client := types.NewPointer(pkg.Scope().Lookup(name).Type())
	var required []*types.Func
	for _, name := range methods {
		obj, _, _ := types.LookupFieldOrMethod(client, true, nil, name)
		fn, ok := obj.(*types.Func)
		if !ok {
			return false
		}
		sig := fn.Signature()
		required = append(required, types.NewFunc(token.NoPos, nil, name,
			types.NewSignatureType(nil, nil, nil, sig.Params(), sig.Results(), sig.Variadic())))
	}
	return types.Implements(input, types.NewInterfaceType(required, nil).Complete())
}

func (r repoReader) bindMethod(m repoMethod, field *ast.Field, ts *ast.TypeSpec) (repoMethod, []diag.Diag) {
	errorf := func(format string, args ...any) (repoMethod, []diag.Diag) {
		return m, []diag.Diag{diag.Errorf(m.pos, "Bind: "+format, args...)}
	}
	if _, ok := r.queries[m.name]; ok {
		return errorf("cannot be implemented by a SQL query")
	}
	if field != nil {
		for _, name := range []string{"conflict", "lock"} {
			d, ds := directive.Find(r.pkg.Fset, field.Doc, name)
			if len(ds) > 0 {
				return m, ds
			}
			if d.Found {
				return errorf("//kanna:%s does not apply to Bind", name)
			}
		}
	}
	sig := m.fn.Signature()
	iface := r.pkg.Types.Scope().Lookup(ts.Name.Name)
	if sig.Variadic() || sig.Params().Len() != 1 || sig.Results().Len() != 1 ||
		!types.Identical(sig.Results().At(0).Type(), iface.Type()) {
		return errorf("declare it as Bind(db DBTX) %s, or with a transaction exposing DBTX()", ts.Name.Name)
	}
	m.bind = &binding{}
	if field != nil {
		if ft, ok := field.Type.(*ast.FuncType); ok && len(ft.Params.List) == 1 {
			id, ok := ft.Params.List[0].Type.(*ast.Ident)
			m.bind.localDBTX = ok && id.Name == "DBTX"
		}
	}
	input := sig.Params().At(0).Type()
	if m.bind.localDBTX || acceptsDatabase(input, r.imported, r.driver) {
		return m, nil
	}
	obj, _, _ := types.LookupFieldOrMethod(input, true, r.pkg.Types, "DBTX")
	if fn, ok := obj.(*types.Func); ok {
		accessor := fn.Signature()
		if accessor.Params().Len() == 0 && accessor.Results().Len() == 1 &&
			acceptsDatabase(accessor.Results().At(0).Type(), r.imported, r.driver) {
			m.bind.accessor = true
			return m, nil
		}
	}
	return errorf("its argument must implement DBTX for %s or expose DBTX() returning it", r.driver)
}

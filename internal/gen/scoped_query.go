package gen

import (
	"go/ast"
	"go/types"

	"github.com/go-kanna/internal/diag"

	"github.com/go-kanna/orm/internal/decl"
	"github.com/go-kanna/orm/internal/sqlfile"
)

// queryScopeParam reads the last of params, those of m, a method a query written by hand implements, which takes
// scopes, and returns the others, which the query takes.
func (r repoReader) queryScopeParam(m *repoMethod, params *types.Tuple, field *ast.Field, ts *ast.TypeSpec,
) (*types.Tuple, []diag.Diag) {
	last := params.At(params.Len() - 1)
	model, implied, ok := r.scopeParam(last, field, ts)
	m.impliedScope = implied
	if !ok {
		return nil, []diag.Diag{diag.Errorf(m.pos, "%s: its last parameter, %s, takes no scope type", m.name,
			last.Name())}
	}
	if ds := r.queryScope(m, model); ds != nil {
		return nil, ds
	}
	vars := make([]*types.Var, 0, params.Len()-1)
	for i := range params.Len() - 1 {
		vars = append(vars, params.At(i))
	}
	return types.NewTuple(vars...), nil
}

// queryScope reads model, of the scopes m, a method a query written by hand implements, takes: m reads the rows of
// the query's result as rows of model's table, which its scopes narrow, order, page, and load the relations of.
func (r repoReader) queryScope(m *repoMethod, model *types.Named) []diag.Diag {
	if v, _ := verbOf(m.name); v != verbGet && v != verbFind && v != verbFirst && v != verbList {
		return []diag.Diag{diag.Errorf(m.pos, "%s: a query written by hand takes scopes as a Get, a Find, a First, or a "+
			"List, which read rows", m.name)}
	}
	target, ok := r.l.scopeOf(model)
	if !ok {
		return []diag.Diag{diag.Errorf(m.pos, "%s: no scope type is declared for %s", m.name, typeName(model))}
	}
	m.scoped, m.model, m.target, m.table = true, model, target, r.s.Tables[target.table]
	return nil
}

// scopedResult checks the result of m, a query written by hand that takes scopes: rows of its model, or of a struct
// embedding it, which adds the columns the query adds to them. It names the query in the statement that applies the
// scopes to its result, and warns of an order of the query's own, which that statement does not keep, and of a lock
// of the query's own, which takes the rows before the scopes narrow them.
func (r repoReader) scopedResult(m *repoMethod, q *query, st sqlfile.Statement) []diag.Diag {
	if q.scalar || !types.Identical(q.row, m.model) && embedded(q.row, m.model) == "" {
		model := typeName(m.model)
		return []diag.Diag{diag.Errorf(m.pos, "%s: with scopes of %s, it reads rows of %s, or of a struct embedding it",
			m.name, model, model).WithHints("as in struct{ " + model + "; Approver *string }, which adds a column")}
	}
	q.cte = r.cteName(m.name, m.target.table)
	var ds []diag.Diag
	if st.OrderWithoutLimit(r.s.Dialect) {
		ds = append(ds, diag.Warningf(st.Header, "-- query: %s: its ORDER BY orders no rows the scopes read", m.name).
			WithHints("order them by the scopes, or keep the ORDER BY with a LIMIT"))
	}
	if pos, clause, ok := st.Lock(r.s.Dialect); ok {
		ds = append(ds, diag.Warningf(pos, "-- query: %s: its %s locks the rows of the query before the scopes narrow "+
			"and page them", m.name, clause).WithHints("write the condition and the page in the query, and take no scopes"))
	}
	return ds
}

// embedded names the field of row, a struct, embedding model by value, if there is one.
func embedded(row types.Type, model *types.Named) string {
	st, ok := row.Underlying().(*types.Struct)
	if !ok {
		return ""
	}
	for f := range st.Fields() {
		if f.Embedded() && types.Identical(f.Type(), model) {
			return f.Name()
		}
	}
	return ""
}

// cteName names the query of the method called name in the statement applying scopes to its result: the method's
// name in snake case, made other than table, which names the result there, and any table of the snapshot.
func (r repoReader) cteName(name, table string) string {
	cte := decl.SnakeCase(name)
	for _, taken := r.s.Tables[cte]; taken || cte == table; _, taken = r.s.Tables[cte] {
		cte += "_query"
	}
	const most = 63 // the longest name PostgreSQL keeps
	return cte[:min(len(cte), most)]
}

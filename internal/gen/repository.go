package gen

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/directive"
	"github.com/go-kanna/internal/packages"
	"github.com/go-kanna/internal/suggest"

	"github.com/go-kanna/orm/internal/decl"
	"github.com/go-kanna/orm/internal/snapshot"
	"github.com/go-kanna/orm/internal/sqlfile"
)

// verb is what a method of a repository does, as the start of its name tells.
type verb string

const (
	verbGet    verb = "Get"
	verbFirst  verb = "First"
	verbList   verb = "List"
	verbCount  verb = "Count"
	verbExists verb = "Exists"
	verbInsert verb = "Insert"
	verbUpdate verb = "Update"
	verbUpsert verb = "Upsert"
	verbDelete verb = "Delete"
)

var verbs = []verb{verbGet, verbFirst, verbList, verbCount, verbExists, verbInsert, verbUpdate, verbUpsert, verbDelete}

// shapes are the forms a method of each verb is declared in, for a hint.
var shapes = map[verb]string{
	verbGet:    "Get(ctx context.Context, id model.OrderID, s ...scopes.Order) (model.Order, error)",
	verbFirst:  "First(ctx context.Context, s ...scopes.Order) (model.Order, error)",
	verbList:   "List(ctx context.Context, s ...scopes.Order) ([]model.Order, error)",
	verbCount:  "Count(ctx context.Context, s ...scopes.Order) (int64, error)",
	verbExists: "Exists(ctx context.Context, s ...scopes.Order) (bool, error)",
	verbInsert: "Insert(ctx context.Context, o model.Order) (model.Order, error), or with error alone",
	verbUpdate: "Update(ctx context.Context, o model.Order) error",
	verbUpsert: "Upsert(ctx context.Context, o model.Order) error",
	verbDelete: "Delete(ctx context.Context, id model.OrderID) error, or DeleteStale(ctx context.Context, " +
		"s ...scopes.Order) (int64, error)",
}

// verbOf tells the verb name starts with: a verb, followed by nothing or by a word of its own, as in GetByNumber, but
// not in Getaway.
func verbOf(name string) (verb, bool) {
	for _, v := range verbs {
		rest, ok := strings.CutPrefix(name, string(v))
		if r, _ := utf8.DecodeRuneInString(rest); ok && (rest == "" || unicode.IsUpper(r) || unicode.IsDigit(r)) {
			return v, true
		}
	}
	return "", false
}

// result is what a method returns besides its error.
type result int

const (
	resultNone  result = iota // error alone
	resultRow                 // the model
	resultRows                // a slice of the model
	resultCount               // an int64
	resultBool                // a bool
)

// repository is a //kanna:repository interface.
type repository struct {
	name string
	pos  token.Position
	// iface is the package declaring it.
	iface   *types.Package
	methods []repoMethod
}

// repoMethod is a method of a repository, with what it reads or writes.
type repoMethod struct {
	name string
	pos  token.Position
	fn   *types.Func
	// verb is what it does, or nothing for a method a query written by hand implements.
	verb verb
	// target is the scope type holding model, the model of the rows the method reads or writes; table is its table,
	// and columns the fields of model holding its columns.
	target  indexed
	model   *types.Named
	table   snapshot.Table
	columns []column
	// keys are the parameters naming the columns the rows must equal, in their order.
	keys []key
	// scoped reports whether the method takes scopes, as its last parameter. row names the parameter of the row it
	// writes, and many reports whether it is a slice of rows.
	scoped bool
	row    string
	many   bool
	result result
	// conflict names the columns an upsert meets an existing row by, and keep those it leaves as they were.
	conflict, keep []string
	// query is the query written by hand that implements the method, if one does.
	query *query
	// impliedScope is the scope type its scopes are of, when //kanna:scopes implies it and it is not declared yet.
	impliedScope *indexed
}

// scopeParam reads the model of p, the variadic parameter of the method field, of the repository ts declares, from the
// scope type it takes: one declared, or one //kanna:scopes implies, which is not declared before its package is
// generated.
func (r repoReader) scopeParam(p *types.Var, field *ast.Field, ts *ast.TypeSpec) (*types.Named, *indexed, bool) {
	elem := p.Type().(*types.Slice).Elem() //nolint:forcetypeassert // a variadic one is
	if model, ok := scopeModel(elem); ok || !packages.Invalid(elem) {
		return model, nil, ok
	}
	if e, ok := r.impliedScope(field, ts); ok && e.model != nil {
		return e.model, &e, true
	}
	return nil, nil, false
}

// impliedScope finds, among the scope types //kanna:scopes implies, the one the last parameter of the method field
// declares takes, of the repository ts declares: by the name its syntax gives, as in s ...scopes.Order.
func (r repoReader) impliedScope(field *ast.Field, ts *ast.TypeSpec) (indexed, bool) {
	if field == nil {
		return indexed{}, false
	}
	ft, ok := field.Type.(*ast.FuncType)
	if !ok || ft.Params == nil || len(ft.Params.List) == 0 {
		return indexed{}, false
	}
	ell, ok := ft.Params.List[len(ft.Params.List)-1].Type.(*ast.Ellipsis)
	if !ok {
		return indexed{}, false
	}
	pkgPath, name := r.pkg.PkgPath, ""
	switch x := ell.Elt.(type) {
	case *ast.Ident:
		name = x.Name
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		if !ok {
			return indexed{}, false
		}
		pkgPath, name = r.importPath(ts, id.Name), x.Sel.Name
	}
	for _, e := range r.l.idx {
		if e.implied && e.pkgPath == pkgPath && e.name == name {
			return e, true
		}
	}
	return indexed{}, false
}

// importPath is the path of the package the file declaring ts imports as name.
func (r repoReader) importPath(ts *ast.TypeSpec, name string) string {
	for _, f := range r.pkg.Syntax {
		if f.Pos() <= ts.Pos() && ts.End() <= f.End() {
			return importNames(f)[name]
		}
	}
	return ""
}

// key is a parameter naming a column, which the rows must equal.
type key struct {
	param string
	col   column
}

// repoReader reads the repositories of a package.
type repoReader struct {
	pkg *packages.Package
	s   snapshot.Snapshot
	l   linker
	// queries are the queries written by hand in the destination, by their names, and queriesAt the destination's
	// import path, by which the snapshot keeps them.
	queries   map[string]sqlfile.Statement
	queriesAt string
	// skip names, by repository, the methods the destination declares by hand, which are left to it.
	skip map[string]map[string]bool
	// imported are the packages the run loads, and those they import, by their import paths, among which the model
	// of a table table= names is found.
	imported map[string]*types.Package
}

// findRepositories reads the //kanna:repository interfaces of pkg, and what each of their methods does.
func (r repoReader) findRepositories() ([]repository, []diag.Diag) {
	var (
		repos   []repository
		ds      []diag.Diag
		methods []string
	)
	for _, file := range r.pkg.Syntax {
		if ast.IsGenerated(file) {
			continue
		}
		decls, _ := decl.Read(r.pkg.Fset, file) // findScopes reports what is wrong with the directives
		for _, d := range decls.Repositories {
			repo, rds := r.repository(d)
			ds = append(ds, rds...)
			for _, m := range repo.methods {
				methods = append(methods, m.name)
			}
			if !diag.HasErrors(rds) {
				repos = append(repos, repo)
			}
		}
	}
	return repos, append(ds, unusedQueries(r.queries, r.pkg.PkgPath, methods)...)
}

func (r repoReader) repository(d decl.Repository) (repository, []diag.Diag) {
	ts := d.Spec
	pos := r.pkg.Fset.Position(ts.Name.Pos())
	repo := repository{name: ts.Name.Name, pos: pos, iface: r.pkg.Types}
	obj, _ := r.pkg.Types.Scope().Lookup(ts.Name.Name).(*types.TypeName)
	syntax, isInterface := ts.Type.(*ast.InterfaceType)
	if obj == nil || !isInterface || ts.TypeParams != nil {
		return repo, []diag.Diag{diag.Errorf(pos, "%s: //kanna:repository marks an interface, not generic", repo.name)}
	}
	iface := obj.Type().Underlying().(*types.Interface) //nolint:forcetypeassert // its syntax is an interface's
	var tableModel *types.Named
	if d.Table != "" {
		var ds []diag.Diag
		if tableModel, ds = r.modelOfTable(d); ds != nil {
			return repo, ds
		}
	}

	docs := map[string]*ast.Field{}
	for _, f := range syntax.Methods.List {
		for _, n := range f.Names {
			docs[n.Name] = f
		}
	}
	methods := make([]*types.Func, 0, iface.NumMethods())
	for m := range iface.Methods() {
		methods = append(methods, m)
	}
	slices.SortFunc(methods, func(a, b *types.Func) int { return cmp.Compare(a.Pos(), b.Pos()) })

	var ds []diag.Diag
	for _, fn := range methods {
		if r.skip[repo.name][fn.Name()] {
			continue
		}
		m, mds := r.method(fn, docs[fn.Name()], ts, tableModel)
		ds = append(ds, mds...)
		repo.methods = append(repo.methods, m)
	}
	return repo, ds
}

// method reads fn, a method of the repository ts declares, as field, its syntax, declares it. tableModel is the model
// of the table table= of the repository names, if it names one, which fn reads or writes when it tells no table.
func (r repoReader) method(fn *types.Func, field *ast.Field, ts *ast.TypeSpec, tableModel *types.Named) (
	repoMethod, []diag.Diag,
) {
	m := repoMethod{name: fn.Name(), pos: r.pkg.Fset.Position(fn.Pos()), fn: fn}
	var doc *ast.CommentGroup
	if field != nil {
		doc = field.Doc
	}
	errorf := func(format string, args ...any) []diag.Diag {
		return []diag.Diag{diag.Errorf(m.pos, "%s: %s", m.name, fmt.Sprintf(format, args...))}
	}
	conflict, cds := directive.Find(r.pkg.Fset, doc, "conflict")
	if st, ok := r.queries[m.name]; ok {
		if conflict.Found {
			cds = append(cds, diag.Errorf(conflict.Pos, "%s: //kanna:conflict is for an Upsert", m.name))
		}
		return m, append(cds, r.query(&m, fn, st)...)
	}

	v, ok := verbOf(m.name)
	if !ok {
		return m, errorf("its name starts with no verb kanna-orm knows, and no -- query: %s is written for it", m.name)
	}
	m.verb = v
	shapeError := func() []diag.Diag {
		return []diag.Diag{diag.Errorf(m.pos, "%s: kanna-orm cannot implement it as declared", m.name).
			WithHints("declare it as " + shapes[v])}
	}

	sig := fn.Signature()
	params, results := sig.Params(), sig.Results()
	if params.Len() == 0 || !isNamed(params.At(0).Type(), "context", "Context") {
		return m, errorf("its first parameter is no context.Context")
	}
	if results.Len() == 0 || results.Len() > 2 || !types.Identical(results.At(results.Len()-1).Type(),
		types.Universe.Lookup("error").Type()) {
		return m, shapeError()
	}

	// The model of the rows, as each of the result, the row, and the scopes tells it.
	var models []*types.Named
	keyParams := []*types.Var{}
	for i := 1; i < params.Len(); i++ {
		p := params.At(i)
		if sig.Variadic() && i == params.Len()-1 {
			model, implied, ok := r.scopeParam(p, field, ts)
			m.impliedScope = implied
			if !ok {
				return m, errorf("its last parameter, %s, takes no scope type", p.Name())
			}
			m.scoped, models = true, append(models, model)
			continue
		}
		if model, many, ok := r.rowModel(p.Type()); ok {
			if m.row != "" {
				return m, errorf("it takes both %s and %s, rows to write", m.row, p.Name())
			}
			m.row, m.many, models = p.Name(), many, append(models, model)
			continue
		}
		if p.Name() == "" || p.Name() == "_" {
			return m, errorf("its parameter %d has no name, which tells the column it stands for", i+1)
		}
		keyParams = append(keyParams, p)
	}
	if results.Len() == 2 {
		switch t := results.At(0).Type(); {
		case isBasic(t, types.Int64):
			m.result = resultCount
		case isBasic(t, types.Bool):
			m.result = resultBool
		default:
			model, many, ok := r.rowModel(t)
			if !ok {
				return m, shapeError()
			}
			m.result, models = resultRow, append(models, model)
			if many {
				m.result = resultRows
			}
		}
	}

	if ds := r.target(&m, models, keyParams, tableModel); ds != nil {
		return m, ds
	}
	if ds := r.keys(&m, keyParams); ds != nil {
		return m, ds
	}
	if !m.shaped() {
		return m, shapeError()
	}
	var ds []diag.Diag
	if conflict.Found {
		ds = r.conflict(&m, conflict)
	}
	ds = append(ds, r.verbRules(&m)...)
	if !diag.HasErrors(ds) {
		ds = append(ds, r.writeRules(m)...)
	}
	return m, append(cds, ds...)
}

// target finds the model the method reads or writes, from those its result, its row, and its scopes have, or else
// from its keys, or else tableModel, and the scope type and table of that model.
func (r repoReader) target(m *repoMethod, models []*types.Named, keyParams []*types.Var, tableModel *types.Named,
) []diag.Diag {
	errorf := func(format string, args ...any) []diag.Diag {
		return []diag.Diag{diag.Errorf(m.pos, "%s: %s", m.name, fmt.Sprintf(format, args...))}
	}
	for _, other := range models {
		if !types.Identical(other, models[0]) {
			return errorf("it reads or writes both %s and %s", typeName(models[0]), typeName(other))
		}
	}
	if len(models) == 0 {
		model, ds := r.modelOfKeys(m, keyParams, tableModel)
		if ds != nil {
			return ds
		}
		models = append(models, model)
	}
	m.model = models[0]

	target, ok := r.l.scopeOf(m.model)
	if !ok {
		return errorf("no scope type is declared for %s", typeName(m.model))
	}
	table, ok := r.s.Tables[target.table]
	if !ok {
		return errorf("the snapshot has no table %s, of which %s.%s is a scope type", target.table, target.pkgName,
			target.name)
	}
	m.target, m.table = target, table
	f, _ := fields(r.pkg.Fset, scope{name: target.name, model: m.model, table: target.table}, table)
	m.columns = f.columns
	return nil
}

// modelOfKeys finds the model of a method that names it nowhere but in the types of its keys: the one model of the
// package a key's type is declared in that has a field for every key, of its name and type, or else tableModel.
func (r repoReader) modelOfKeys(m *repoMethod, keyParams []*types.Var, tableModel *types.Named) (
	*types.Named, []diag.Diag,
) {
	errorf := func(format string, args ...any) []diag.Diag {
		return []diag.Diag{diag.Errorf(m.pos, "%s: %s", m.name, fmt.Sprintf(format, args...)).
			WithHints("give it the scopes of the table, as in " + shapes[m.verb])}
	}
	var candidates []*types.Named
	seen := map[*types.Named]bool{}
	for _, p := range keyParams {
		n := asNamed(p.Type())
		if n == nil || n.Obj().Pkg() == nil {
			continue
		}
		pkg := n.Obj().Pkg()
		for _, e := range slices.Concat(r.l.loaded, r.l.idx) {
			obj, ok := pkg.Scope().Lookup(e.modelName).(*types.TypeName)
			if !ok || e.modelPath != pkg.Path() {
				continue
			}
			if model := asNamed(obj.Type()); model != nil && !seen[model] {
				seen[model] = true
				if hasFields(model, keyParams) {
					candidates = append(candidates, model)
				}
			}
		}
	}
	switch {
	case len(candidates) == 0 && tableModel != nil:
		return tableModel, nil
	case len(candidates) == 0:
		return nil, []diag.Diag{diag.Errorf(m.pos, "%s: nothing tells the table it reads or writes", m.name).
			WithHints("give it the scopes of the table, as in "+shapes[m.verb],
				"or name the table of the methods that tell none: //kanna:repository table=<table>")}
	case len(candidates) == 1:
		return candidates[0], nil
	}
	return nil, errorf("its parameters are the columns of both %s and %s", typeName(candidates[0]),
		typeName(candidates[1]))
}

// modelOfTable finds the model of the table that table= of the repository d declares names, by its scope type.
func (r repoReader) modelOfTable(d decl.Repository) (*types.Named, []diag.Diag) {
	name := d.Spec.Name.Name
	if _, ok := r.s.Tables[d.Table]; !ok {
		missing := diag.Errorf(d.Pos, "%s: the snapshot has no table %s, which table= names", name, d.Table)
		if near, ok := suggest.Closest(d.Table, slices.Sorted(maps.Keys(r.s.Tables))); ok {
			missing = missing.WithHints(fmt.Sprintf("did you mean %s?", near))
		}
		return nil, []diag.Diag{missing}
	}
	for _, sc := range r.l.scopes {
		if sc.table == d.Table {
			return sc.model, nil
		}
	}
	for _, e := range slices.Concat(r.l.loaded, r.l.idx) {
		if e.table != d.Table {
			continue
		}
		if e.model != nil {
			return e.model, nil
		}
		if p := r.imported[e.modelPath]; p != nil {
			if obj, ok := p.Scope().Lookup(e.modelName).(*types.TypeName); ok && asNamed(obj.Type()) != nil {
				return asNamed(obj.Type()), nil
			}
		}
		return nil, []diag.Diag{diag.Errorf(d.Pos, "%s: %s.%s, the model of %s, is in no package the run loads",
			name, e.modelPath, e.modelName, d.Table).
			WithHints("name it in a method of " + name + ", as its result or a row it writes")}
	}
	return nil, []diag.Diag{diag.Errorf(d.Pos, "%s: no scope type is declared for %s, which table= names", name,
		d.Table).WithHints(fmt.Sprintf("declare one where %s are queried: //kanna:scope table=%[1]s", d.Table))}
}

// hasFields reports whether model has a field for each of params, named as it is, in upper case, and of its type.
func hasFields(model *types.Named, params []*types.Var) bool {
	st, ok := model.Underlying().(*types.Struct)
	if !ok {
		return false
	}
	for _, p := range params {
		if !slices.ContainsFunc(slices.Collect(st.Fields()), func(f *types.Var) bool {
			return columnName(f.Name()) == columnName(p.Name()) && fieldTakes(f.Type(), p.Type())
		}) {
			return false
		}
	}
	return true
}

// keys matches the parameters naming columns to the fields of the model holding them.
func (r repoReader) keys(m *repoMethod, keyParams []*types.Var) []diag.Diag {
	var ds []diag.Diag
	for _, p := range keyParams {
		name := columnName(p.Name())
		i := slices.IndexFunc(m.columns, func(c column) bool { return c.col.Name == name })
		if i < 0 {
			d := diag.Errorf(m.pos, "%s: %s has no column for the parameter %s", m.name, m.target.table, p.Name())
			names := make([]string, 0, len(m.columns))
			for _, c := range m.columns {
				names = append(names, c.col.Name)
			}
			if near, ok := suggest.Closest(name, names); ok {
				c := m.columns[slices.IndexFunc(m.columns, func(c column) bool { return c.col.Name == near })]
				d = d.WithHints(fmt.Sprintf("did you mean %s (%s)?", lowerFirst(c.path[len(c.path)-1]), near))
			}
			ds = append(ds, d)
			continue
		}
		c := m.columns[i]
		if !fieldTakes(c.typ, p.Type()) {
			field := m.model.Obj().Name() + "." + strings.Join(c.path, ".")
			pt, ft := typeName(p.Type()), typeName(c.typ)
			ds = append(ds, diag.Errorf(m.pos, "%s: %s is %s %s, but %s, which holds %s, is %s %s", m.name, p.Name(),
				article(pt), pt, field, name, article(ft), ft).WithHints("declare it as "+typeName(elemOf(c.typ))))
			continue
		}
		m.keys = append(m.keys, key{param: p.Name(), col: c})
	}
	return ds
}

// fieldTakes reports whether a parameter of type p stands for a field of type field: of its type, or of the type it
// points to.
func fieldTakes(field, p types.Type) bool {
	return types.Identical(field, p) || types.Identical(elemOf(field), p)
}

func elemOf(t types.Type) types.Type {
	if ptr, ok := t.Underlying().(*types.Pointer); ok {
		return ptr.Elem()
	}
	return t
}

// shaped reports whether the parameters and the result of m are those its verb takes.
func (m repoMethod) shaped() bool {
	writes := m.verb == verbInsert || m.verb == verbUpdate || m.verb == verbUpsert
	var ok bool
	switch m.verb {
	case verbGet, verbFirst:
		ok = m.result == resultRow
	case verbList:
		ok = m.result == resultRows
	case verbCount:
		ok = m.result == resultCount
	case verbExists:
		ok = m.result == resultBool
	case verbInsert, verbUpsert:
		ok = m.result == resultNone || m.result == resultRow && !m.many || m.result == resultRows && m.many
	case verbUpdate:
		ok = m.result == resultNone && !m.many
	case verbDelete:
		ok = m.result == resultNone && !m.scoped || m.result == resultCount && (m.scoped || len(m.keys) > 0)
	}
	if writes {
		return ok && m.row != "" && len(m.keys) == 0 && !m.scoped
	}
	return ok && m.row == ""
}

// verbRules reports what else a method of its verb asks of its table.
func (r repoReader) verbRules(m *repoMethod) []diag.Diag {
	errorf := func(format string, args ...any) diag.Diag {
		return diag.Errorf(m.pos, "%s: %s", m.name, fmt.Sprintf(format, args...))
	}
	names := make([]string, 0, len(m.keys))
	for _, k := range m.keys {
		names = append(names, k.col.col.Name)
	}
	switch {
	case m.verb == verbGet && !containsKey(m.table, names):
		return []diag.Diag{errorf("%s is not a unique key of %s, so Get cannot return one row",
			strings.Join(names, ", "), m.target.table).WithHints(fmt.Sprintf(
			"use List, or add a unique index on %s (%s)", m.target.table, strings.Join(names, ", ")))}
	case m.verb == verbDelete && m.result == resultNone && !containsKey(m.table, names):
		return []diag.Diag{errorf("(%s) is no key of %s, so Delete would delete more than the one row",
			strings.Join(names, ", "), m.target.table).WithHints(
			"return (int64, error) for the number of rows deleted, or take a key of the table")}
	case m.verb == verbUpdate && len(m.table.PrimaryKey) == 0:
		return []diag.Diag{errorf("%s has no primary key to tell its rows by", m.target.table)}
	case m.verb == verbUpsert && len(m.rowKey()) == 0:
		return []diag.Diag{errorf("%s has no primary key to tell its rows by", m.target.table).WithHints(
			"name a unique key of " + m.target.table + " with //kanna:conflict")}
	case (m.verb == verbInsert || m.verb == verbUpsert) && m.many && m.result != resultNone &&
		r.s.Dialect == snapshot.MySQL:
		return []diag.Diag{errorf("MySQL has no RETURNING, so the rows of one statement cannot be read back").
			WithHints("return error alone")}
	}
	return nil
}

// containsKey reports whether columns include every column of the primary key of t, or of one of its unique keys.
func containsKey(t snapshot.Table, columns []string) bool {
	for _, k := range append([][]string{t.PrimaryKey}, t.UniqueKeys...) {
		if len(k) > 0 && !slices.ContainsFunc(k, func(c string) bool { return !slices.Contains(columns, c) }) {
			return true
		}
	}
	return false
}

// conflict reads the //kanna:conflict of an upsert: the columns of the key it meets a row by, and keep= those it
// leaves as they were.
func (r repoReader) conflict(m *repoMethod, d directive.Directive) []diag.Diag {
	errorf := func(format string, args ...any) diag.Diag {
		return diag.Errorf(d.Pos, "%s: %s", m.name, fmt.Sprintf(format, args...))
	}
	if m.verb != verbUpsert {
		return []diag.Diag{errorf("//kanna:conflict is for an Upsert")}
	}
	fields := strings.Fields(d.Args)
	if len(fields) == 0 || len(fields) > 2 || strings.Contains(fields[0], "=") {
		return []diag.Diag{errorf("//kanna:conflict names the columns of a key, and keep= those to leave as they were").
			WithHints("as in //kanna:conflict tenant_id,email keep=created_at")}
	}
	m.conflict = strings.Split(fields[0], ",")
	if len(fields) == 2 {
		keep, ok := strings.CutPrefix(fields[1], "keep=")
		if !ok || keep == "" {
			return []diag.Diag{errorf("//kanna:conflict takes keep= after the columns of the key, not %q", fields[1])}
		}
		m.keep = strings.Split(keep, ",")
	}
	for _, c := range slices.Concat(m.conflict, m.keep) {
		if !slices.ContainsFunc(m.table.Columns, func(col snapshot.Column) bool { return col.Name == c }) {
			return []diag.Diag{errorf("%s has no column %s", m.target.table, c)}
		}
	}
	if !unique(m.table, m.conflict) {
		return []diag.Diag{errorf("(%s) is neither the primary key of %s nor one of its unique keys",
			strings.Join(m.conflict, ", "), m.target.table)}
	}

	// MySQL updates on a conflict over any unique key, but for one the database assigns every column of.
	var others []string
	for _, k := range append([][]string{m.table.PrimaryKey}, m.table.UniqueKeys...) {
		assigned := !slices.ContainsFunc(k, func(c string) bool {
			i := slices.IndexFunc(m.table.Columns, func(col snapshot.Column) bool { return col.Name == c })
			return i < 0 || !m.table.Columns[i].Assigned
		})
		if len(k) > 0 && !assigned && !sameColumns(k, m.conflict) {
			others = append(others, "("+strings.Join(k, ", ")+")")
		}
	}
	if r.s.Dialect == snapshot.MySQL && len(others) > 0 {
		return []diag.Diag{diag.Warningf(m.pos, "%s: on MySQL, a conflict on %s also updates the row", m.name,
			strings.Join(others, " or ")).WithHints(fmt.Sprintf("%s has unique keys other than (%s)", m.target.table,
			strings.Join(m.conflict, ", ")))}
	}
	return nil
}

func sameColumns(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

// writeRules reports what keeps a write from writing its rows as the model holds them.
func (r repoReader) writeRules(m repoMethod) []diag.Diag {
	if m.verb != verbInsert && m.verb != verbUpdate && m.verb != verbUpsert {
		return nil
	}
	errorf := func(format string, args ...any) diag.Diag {
		return diag.Errorf(m.pos, "%s: %s", m.name, fmt.Sprintf(format, args...))
	}
	model := typeName(m.model)
	held := func(name string) bool {
		return slices.ContainsFunc(m.columns, func(c column) bool { return c.col.Name == name })
	}
	var ds []diag.Diag
	if m.verb != verbUpdate {
		for _, c := range m.table.Columns {
			if c.NotNull && !c.Default && !c.Assigned && !held(c.Name) {
				ds = append(ds, errorf("%s requires a value for %s, which %s has no field for", m.target.table, c.Name,
					model).WithHints(fmt.Sprintf("give %s a field for it, or the column a default", model)))
			}
		}
		if len(m.inserted()) == 0 {
			ds = append(ds, errorf("%s holds no column of %s but those the database assigns", model, m.target.table))
		}
	}
	if m.verb != verbInsert {
		for _, name := range m.rowKey() {
			i := slices.IndexFunc(m.table.Columns, func(c snapshot.Column) bool { return c.Name == name })
			switch {
			case !held(name):
				ds = append(ds, errorf("%s has no field for %s, which it meets its row by", model, name))
			case m.verb == verbUpsert && m.table.Columns[i].Assigned:
				ds = append(ds, errorf("the database assigns %s, so an insert never meets a row by it", name).
					WithHints(fmt.Sprintf("name another unique key of %s with //kanna:conflict", m.target.table)))
			}
		}
	}
	if m.verb == verbUpdate && len(m.updated()) == 0 {
		ds = append(ds, errorf("%s holds no column of %s to update but its key", model, m.target.table))
	}
	if m.verb == verbInsert && m.result == resultRow && r.s.Dialect == snapshot.MySQL {
		if len(m.table.PrimaryKey) == 0 {
			ds = append(ds, errorf("MySQL reads the row written back by its primary key, which %s has none",
				m.target.table).WithHints("return error alone"))
		}
		auto, _ := m.autoIncrement()
		for _, name := range m.table.PrimaryKey {
			if name != auto && !held(name) {
				ds = append(ds, errorf("MySQL reads the row written back by its primary key, but %s has no field for %s",
					model, name).WithHints("return error alone"))
			}
		}
	}
	if m.many && r.s.Dialect == snapshot.PostgreSQL {
		for _, c := range m.inserted() {
			if strings.HasSuffix(c.col.Type, "[]") {
				ds = append(ds, errorf("unnest, which writes the rows in one statement, cannot write %s, an array",
					c.col.Name).WithHints("write the rows one by one"))
			}
		}
	}
	return ds
}

// inserted are the columns a write of m inserts: those its model holds but the database does not assign.
func (m repoMethod) inserted() []column {
	return slices.DeleteFunc(slices.Clone(m.columns), func(c column) bool { return c.col.Assigned })
}

// autoIncrement names the column of the primary key of m's table that the database assigns, which MySQL tells the
// value of a row inserted by LAST_INSERT_ID().
func (m repoMethod) autoIncrement() (string, bool) {
	for _, name := range m.table.PrimaryKey {
		if i := slices.IndexFunc(m.table.Columns, func(c snapshot.Column) bool { return c.Name == name }); i >= 0 &&
			m.table.Columns[i].Assigned {
			return name, true
		}
	}
	return "", false
}

// field is the column of m's model held by the field for the column name.
func (m repoMethod) field(name string) column {
	return m.columns[slices.IndexFunc(m.columns, func(c column) bool { return c.col.Name == name })]
}

// rowKey names the columns an update or an upsert of m meets its row by: those //kanna:conflict names, or the
// primary key.
func (m repoMethod) rowKey() []string {
	if len(m.conflict) > 0 {
		return m.conflict
	}
	return m.table.PrimaryKey
}

// updated are the columns an update or an upsert of m writes over its row: those it inserts but its key, the primary
// key, and those it keeps.
func (m repoMethod) updated() []column {
	skip := slices.Concat(m.rowKey(), m.table.PrimaryKey, m.keep)
	return slices.DeleteFunc(m.inserted(), func(c column) bool { return slices.Contains(skip, c.col.Name) })
}

// rowModel tells the model a parameter or a result of type t holds: a model, or a slice of models.
func (r repoReader) rowModel(t types.Type) (model *types.Named, many bool, ok bool) {
	if s, isSlice := t.Underlying().(*types.Slice); isSlice {
		t, many = s.Elem(), true
	}
	n := asNamed(t)
	if n == nil {
		return nil, false, false
	}
	if _, ok := r.l.scopeOf(n); !ok {
		return nil, false, false
	}
	return n, many, true
}

// scopeModel tells the model of a scope type t: M, of its one field, an orm.Of[M].
func scopeModel(t types.Type) (*types.Named, bool) {
	n := asNamed(t)
	if n == nil {
		return nil, false
	}
	st, ok := n.Underlying().(*types.Struct)
	if !ok || st.NumFields() != 1 {
		return nil, false
	}
	of := asNamed(st.Field(0).Type())
	if of == nil || of.Obj().Pkg() == nil || of.Obj().Pkg().Path() != ormPath || of.Obj().Name() != "Of" {
		return nil, false
	}
	model := asNamed(of.TypeArgs().At(0))
	return model, model != nil
}

func asNamed(t types.Type) *types.Named {
	n, _ := types.Unalias(t).(*types.Named)
	return n
}

func isNamed(t types.Type, pkgPath, name string) bool {
	n := asNamed(t)
	return n != nil && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == pkgPath && n.Obj().Name() == name
}

func isBasic(t types.Type, kind types.BasicKind) bool {
	b, ok := types.Unalias(t).(*types.Basic)
	return ok && b.Kind() == kind
}

func typeName(t types.Type) string {
	return types.TypeString(t, (*types.Package).Name)
}

func lowerFirst(s string) string {
	runes := []rune(s)
	i := 0
	for i < len(runes) && unicode.IsUpper(runes[i]) && (i == 0 || i+1 == len(runes) || unicode.IsUpper(runes[i+1])) {
		runes[i] = unicode.ToLower(runes[i])
		i++
	}
	if i == 0 && len(runes) > 0 {
		runes[0] = unicode.ToLower(runes[0])
	}
	return string(runes)
}

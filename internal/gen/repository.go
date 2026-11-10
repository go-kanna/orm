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
	verbFind   verb = "Find"
	verbFirst  verb = "First"
	verbList   verb = "List"
	verbCount  verb = "Count"
	verbExists verb = "Exists"
	verbInsert verb = "Insert"
	verbUpdate verb = "Update"
	verbUpsert verb = "Upsert"
	verbDelete verb = "Delete"
	verbCopy   verb = "Copy"
	verbSum    verb = "Sum"
	verbMin    verb = "Min"
	verbMax    verb = "Max"
	verbAvg    verb = "Avg"
)

var verbs = []verb{
	verbGet, verbFind, verbFirst, verbList, verbCount, verbExists, verbInsert, verbUpdate, verbUpsert, verbDelete,
	verbCopy, verbSum, verbMin, verbMax, verbAvg,
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

// returns is what a method returns besides its error.
type returns int

const (
	returnsNone   returns = iota // error alone
	returnsRow                   // the model
	returnsRows                  // a slice of the model
	returnsCount                 // an int64
	returnsBool                  // a bool
	returnsScalar                // one column of a query written by hand
)

// repository is a //kanna:repository interface.
type repository struct {
	name  string
	local bool
	pos   token.Position
	// iface is the package declaring it.
	iface             *types.Package
	methods           []repoMethod
	publicConstructor bool
	// mapError reports whether the implementation has a mapError method, which the errors its methods return go
	// through.
	mapError bool
	// operations are the methods of the interface but Bind, when mapError takes the operation an error comes from,
	// for which the implementation declares the operations, and none when it does not.
	operations []string
	// copies reports whether a method kanna-orm writes for the interface copies rows, a Copy or the helper of one,
	// for which DBTX copies them.
	copies bool
}

// operation names the type of the operations of r, as mapError takes them: orderOperation for Order.
func (r repository) operation() string { return lowerFirst(r.name) + "Operation" }

// operationOf names the operation of the method name, a constant of r.operation().
func (r repository) operationOf(name string) string { return r.operation() + name }

// operationNames names the type of the operations of r and its constants, if mapError takes them.
func (r repository) operationNames() []string {
	if len(r.operations) == 0 {
		return nil
	}
	names := []string{r.operation()}
	for _, m := range r.operations {
		names = append(names, r.operationOf(m))
	}
	return names
}

// generates names what the implementation of r declares: its struct, its constructors, and the operations of its
// methods, if mapError takes them.
func (r repository) generates() []string {
	names := []string{r.implementation(), r.constructor()}
	if r.publicConstructor {
		names = append(names, "New"+r.name)
	}
	return append(names, r.operationNames()...)
}

// repoMethod is a method of a repository, with what it reads or writes.
type repoMethod struct {
	name string
	pos  token.Position
	fn   *types.Func
	bind *binding
	// verb is what it does, or nothing for a method a query written by hand implements.
	verb verb
	// target is the scope type holding model, the model of the rows the method reads or writes; table is its table,
	// and columns the fields of model holding its columns.
	target     indexed
	model      *types.Named
	table      snapshot.Table
	columns    []column
	pluck      *column
	aggregate  *aggregate
	projection *readProjection
	// keys are the parameters naming the columns the rows must equal, in their order.
	keys []key
	// scoped reports whether the method takes scopes, as its last parameter. row names the parameter of the row it
	// writes, and many reports whether it is a slice of rows.
	scoped   bool
	row      string
	changes  string
	many     bool
	optional bool
	returns  returns
	// conflict names the columns an upsert meets an existing row by.
	conflict []string
	// lock is the locking clause //kanna:lock gives a read, or the subquery of an Update that selects its rows, if any.
	lock string
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
	skip map[string]map[string]handMethod
	// imported are the packages the run loads, and those they import, by their import paths, among which the model
	// of a table table= names is found.
	imported map[string]*types.Package
	local    bool
	driver   driver
	declared map[string]token.Position
	// funcs are the functions the destination declares at its top level: a New of a repository among them returning
	// the repository is the repository's own constructor.
	funcs map[string]handFunc
	// calls are what the methods written by hand select on their receiver, by the type they are declared on: the
	// helpers they call.
	calls map[string]map[string]bool
}

// findRepositories reads the //kanna:repository interfaces of pkg, and what each of their methods does.
func (r repoReader) findRepositories() ([]repository, []diag.Diag) {
	var (
		repos   []repository
		ds      []diag.Diag
		methods []string
	)
	if at, ok := r.declared["DBTX"]; ok {
		ds = append(ds, diag.Errorf(at, "DBTX is declared already, and kanna-orm writes it as what the repositories "+
			"run on").WithHints("name yours otherwise"))
	}
	for _, file := range r.pkg.Syntax {
		if ast.IsGenerated(file) {
			continue
		}
		decls, _ := decl.Read(r.pkg.Fset, file) // findScopes reports what is wrong with the directives
		mds := decl.Misplaced(r.pkg.Fset, file, r.pkg.Types)
		for _, d := range decls.Repositories {
			if decl.IsMisplaced(mds, d.Pos) {
				continue
			}
			repo, rds := r.repository(d)
			ds = append(ds, rds...)
			for _, m := range repo.methods {
				methods = append(methods, m.fn.Name())
			}
			if !diag.HasErrors(rds) {
				repos = append(repos, repo)
			}
		}
		ds = append(ds, strayDirectives(r.pkg.Fset, file, decls.Repositories)...)
	}
	ds = append(ds, r.copyBinds(repos)...)
	ds = append(ds, generatedTwice(repos)...)
	ds = append(ds, unusedQueries(r.queries, r.pkg.PkgPath, methods)...)
	// What is wrong with a table or a model many methods share is told once: that of the repository's name, which each
	// method that tells no table is told, and the fields of a model.
	return repos, distinct(ds)
}

// declaredAlready reports each of names, which the implementation of repo declares, that the destination declares
// by hand.
func (r repoReader) declaredAlready(repo repository, names []string) []diag.Diag {
	var ds []diag.Diag
	for _, name := range names {
		if at, ok := r.declared[name]; ok {
			ds = append(ds, diag.Errorf(at, "%s is declared already, and kanna-orm writes it for %s", name,
				repo.name).WithHints("name yours otherwise"))
		}
	}
	return ds
}

// generatedTwice reports each name the implementations of two of repos both declare, which the second of them is told
// of.
func generatedTwice(repos []repository) []diag.Diag {
	var ds []diag.Diag
	owners := map[string]repository{}
	for _, repo := range repos {
		for _, name := range repo.generates() {
			if other, ok := owners[name]; ok {
				ds = append(ds, diag.Errorf(repo.pos, "%s: kanna-orm writes %s for it, and for %s as well", repo.name,
					name, other.name).WithHints("rename one of the repositories"))
				continue
			}
			owners[name] = repo
		}
	}
	return ds
}

func (r repoReader) repository(d decl.Repository) (repository, []diag.Diag) {
	ts := d.Spec
	pos := r.pkg.Fset.Position(ts.Name.Pos())
	repo := repository{name: ts.Name.Name, pos: pos, iface: r.pkg.Types, local: r.local}
	// A New of the repository's name that does not return it is told as any other name declared already.
	own, ok := r.funcs["New"+repo.name]
	repo.publicConstructor = !ok || !own.returns(repo)
	names := []string{repo.implementation(), repo.constructor()}
	if repo.publicConstructor {
		names = append(names, "New"+repo.name)
	}
	// The methods are read all the same, so that what is wrong with them is told, and their queries are not unused.
	ds := r.declaredAlready(repo, names)
	if r.local && !ts.Name.IsExported() {
		return repo, append(ds, diag.Errorf(pos, "%s: a repository implemented in its own package must be exported",
			repo.name))
	}
	obj, _ := r.pkg.Types.Scope().Lookup(ts.Name.Name).(*types.TypeName)
	syntax, isInterface := ts.Type.(*ast.InterfaceType)
	if obj == nil || !isInterface || ts.TypeParams != nil {
		return repo, append(ds, diag.Errorf(pos, "%s: //kanna:repository marks an interface, not generic", repo.name))
	}
	iface := obj.Type().Underlying().(*types.Interface) //nolint:forcetypeassert // its syntax is an interface's
	var fallback tableModel
	if d.Table != "" {
		fallback = r.modelOfTable(d.Pos, repo.name, d.Table, "which table= names")
		ds = append(ds, fallback.ds...)
	} else if table, ok := decl.TableOf(strings.TrimSuffix(repo.name, "Repository"), func(table string) bool {
		_, ok := r.s.Tables[table]
		return ok
	}); ok {
		fallback = r.modelOfTable(d.Pos, repo.name, table, "which "+repo.name+" is named after")
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

	implementation := repo.implementation()
	called := r.calls[implementation]
	for _, fn := range methods {
		_, overridden := r.skip[implementation][fn.Name()]
		_, generated := verbOf(fn.Name())
		_, query := r.queries[fn.Name()]
		// A method written by hand gets a helper, its name made unexported, only if it calls the helper: one
		// that runs SQL of its own needs none, and kanna-orm may not be able to write one for it.
		if overridden && (!generated && !query || !called[lowerFirst(fn.Name())]) {
			if f := docs[fn.Name()]; f != nil {
				for _, md := range methodDirectives {
					if d, _ := directive.Find(r.pkg.Fset, f.Doc, md.key); d.Found {
						ds = append(ds, hasNoGeneratedSQL(d, md.key, fn.Name(), "the method the destination declares"))
					}
				}
			}
			continue
		}
		var m repoMethod
		var mds []diag.Diag
		if fn.Name() == "Bind" {
			m, mds = r.bindMethod(repoMethod{name: fn.Name(), pos: r.pkg.Fset.Position(fn.Pos()), fn: fn},
				docs[fn.Name()], ts)
		} else {
			m, mds = r.method(fn, docs[fn.Name()], ts, fallback)
		}
		if overridden {
			m.name = lowerFirst(m.name)
			if _, ok := r.skip[implementation][m.name]; ok {
				mds = append(mds, diag.Errorf(m.pos, "%s: generated helper %s.%s is already declared", fn.Name(),
					implementation, m.name))
			}
		}
		ds = append(ds, mds...)
		repo.methods = append(repo.methods, m)
	}
	repo.copies = slices.ContainsFunc(repo.methods, func(m repoMethod) bool { return m.verb == verbCopy })
	if m, ok := r.skip[implementation][mapErrorName]; ok {
		repo.mapError = true
		takesOperation, mds := mapErrorSignature(repo, implementation, m)
		ds = append(ds, mds...)
		if takesOperation {
			for _, fn := range methods {
				if fn.Name() != "Bind" {
					repo.operations = append(repo.operations, fn.Name())
				}
			}
			ds = append(ds, r.declaredAlready(repo, repo.operationNames())...)
		}
	}
	return repo, ds
}

// method reads fn, a method of the repository ts declares, as field, its syntax, declares it. fallback is the model
// fn reads or writes when it tells no table.
func (r repoReader) method(fn *types.Func, field *ast.Field, ts *ast.TypeSpec, fallback tableModel) (
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
	lock, lds := directive.Find(r.pkg.Fset, doc, "lock")
	pluck, pds := directive.Find(r.pkg.Fset, doc, "pluck")
	cds = append(cds, lds...)
	cds = append(cds, pds...)
	if pluck.Found {
		return m, append(cds, diag.Errorf(pluck.Pos, "%s: //kanna:pluck is no more: a List of a column's values is "+
			"named after its field", m.name).WithHints("as in "+r.shapeNames(fallback.model).fill(pluckShape)))
	}
	if st, ok := r.queries[m.name]; ok {
		if conflict.Found {
			cds = append(cds, hasNoGeneratedSQL(conflict, "conflict", m.name, "-- query: "+m.name))
		}
		qds := r.query(&m, fn, st, field, ts)
		if lock.Found {
			d := hasNoGeneratedSQL(lock, "lock", m.name, "-- query: "+m.name)
			if m.scoped {
				d.Hints = []string{"lock in -- query: " + m.name + ", with the condition and the page written there, and " +
					"take no scopes", "a lock there with scopes takes the rows before the scopes narrow and page them"}
			}
			cds = append(cds, d)
		}
		return m, append(cds, qds...)
	}

	sig := fn.Signature()
	params, results := sig.Params(), sig.Results()
	v, ok := verbOf(m.name)
	if !ok {
		return m, errorf("its name starts with no verb kanna-orm knows, and no -- query: %s is written for it", m.name)
	}
	m.verb = v
	if v == verbCopy {
		if ds := r.copyDriver(m, r.signatureModel(fn.Signature(), field, ts, fallback)); ds != nil {
			return m, ds
		}
	}
	// shapeError tells that m is of no form of its verb, and why if it can, in the names of model.
	shapeError := func(model *types.Named, why func(shapeNames) string) []diag.Diag {
		names := r.shapeNames(model)
		msg := "kanna-orm cannot implement it as declared"
		if w := why(names); w != "" {
			msg += ": " + w
		}
		return []diag.Diag{diag.Errorf(m.pos, "%s: %s", m.name, msg).
			WithHints(fmt.Sprintf("declare it in a form of %s, as in %s", v, names.fill(shapes[v])))}
	}

	if params.Len() == 0 || !isNamed(params.At(0).Type(), "context", "Context") {
		return m, errorf("its first parameter is no context.Context")
	}
	if why := resultsMismatch(v, results); why != "" {
		return m, shapeError(r.signatureModel(sig, field, ts, fallback), func(shapeNames) string { return why })
	}

	models, keyParams, parameterDiags := r.methodParameters(&m, sig, field, ts)
	if parameterDiags != nil {
		return m, parameterDiags
	}

	ofColumn := false
	if results.Len() == 2 {
		taken := models
		var ok bool
		if models, ofColumn, ok = r.result(&m, results.At(0).Type(), models); !ok {
			row := results.At(0).Type()
			if p, isPointer := row.(*types.Pointer); isPointer && v == verbFind {
				row = p.Elem()
			}
			if model, unscoped := r.unscoped(row); unscoped {
				return m, noScopeType(m, model)
			}
			model := fallback.model
			if len(taken) > 0 {
				model = taken[0]
			}
			return m, shapeError(model, func(n shapeNames) string {
				return fmt.Sprintf("it returns %s, and %s %s returns %s", typeName(results.At(0).Type()), v.article(),
					v, n.fill(returnShapes[v]))
			})
		}
	}

	if ds := r.target(&m, models, fallback); ds != nil {
		return m, ds
	}
	if ds := r.keys(&m, keyParams); ds != nil {
		return m, ds
	}
	if !m.shaped() {
		result := "error alone"
		if results.Len() == 2 {
			result = typeName(results.At(0).Type())
		}
		return m, shapeError(m.model, func(n shapeNames) string { return m.mismatch(result, n) })
	}
	if ofColumn {
		if ds := r.resultColumn(&m, results.At(0).Type()); ds != nil {
			return m, ds
		}
	}
	var ds []diag.Diag
	if conflict.Found {
		ds = r.conflict(&m, conflict)
	}
	ds = append(ds, r.lock(&m, lock)...)
	ds = append(ds, r.verbRules(&m)...)
	if !diag.HasErrors(ds) {
		ds = append(ds, r.writeRules(m)...)
	}
	if !diag.HasErrors(ds) {
		ds = append(ds, r.otherKeys(m)...)
	}
	return m, append(cds, ds...)
}

// target finds the model the method reads or writes, from those its result, its row, and its scopes have, or else
// fallback, and the scope type and table of that model.
func (r repoReader) target(m *repoMethod, models []*types.Named, fallback tableModel) []diag.Diag {
	errorf := func(format string, args ...any) []diag.Diag {
		return []diag.Diag{diag.Errorf(m.pos, "%s: %s", m.name, fmt.Sprintf(format, args...))}
	}
	for _, other := range models {
		if !types.Identical(other, models[0]) {
			return errorf("it reads or writes both %s and %s", typeName(models[0]), typeName(other))
		}
	}
	if len(models) == 0 {
		switch {
		case fallback.ds != nil:
			return fallback.ds
		case fallback.model == nil:
			return []diag.Diag{diag.Errorf(m.pos, "%s: nothing tells the table it reads or writes", m.name).
				WithHints("give it the scopes of the table, as in "+r.shape(m.verb, nil),
					"or name the repository after the model of the table, as OrderRepository is after Order of orders",
					"or name the table of the methods that tell none: //kanna:repository table=<table>")}
		}
		models = append(models, fallback.model)
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
	f, fds := fields(r.pkg.Fset, scope{name: target.name, model: m.model, table: target.table}, table)
	if wrong := mistakes(fds); wrong != nil {
		return wrong // what the method says of the fields would follow from them
	}
	m.columns = f.columns
	if m.projection != nil {
		return r.projectionFields(m)
	}
	return nil
}

// tableModel is the model of the methods of a repository that tell no table themselves, or ds, why there is none.
type tableModel struct {
	model *types.Named
	ds    []diag.Diag
}

// modelOfTable finds the model of table, by its scope type, for the repository called name, whose directive is at
// pos. why says how the repository names table.
func (r repoReader) modelOfTable(pos token.Position, name, table, why string) tableModel {
	if _, ok := r.s.Tables[table]; !ok {
		missing := diag.Errorf(pos, "%s: the snapshot has no table %s, %s", name, table, why)
		if near, ok := suggest.Closest(table, slices.Sorted(maps.Keys(r.s.Tables))); ok {
			missing = missing.WithHints(fmt.Sprintf("did you mean %s?", near))
		}
		return tableModel{ds: []diag.Diag{missing}}
	}
	for _, sc := range r.l.scopes {
		if sc.table == table {
			return tableModel{model: sc.model}
		}
	}
	for _, e := range slices.Concat(r.l.loaded, r.l.idx) {
		if e.table != table {
			continue
		}
		if e.model != nil {
			return tableModel{model: e.model}
		}
		if p := r.imported[e.modelPath]; p != nil {
			if obj, ok := p.Scope().Lookup(e.modelName).(*types.TypeName); ok && asNamed(obj.Type()) != nil {
				return tableModel{model: asNamed(obj.Type())}
			}
		}
		return tableModel{ds: []diag.Diag{diag.Errorf(pos, "%s: %s.%s, the model of %s, is in no package the run loads",
			name, e.modelPath, e.modelName, table).
			WithHints("name it in a method of " + name + ", as its result or a row it writes")}}
	}
	return tableModel{ds: []diag.Diag{diag.Errorf(pos, "%s: no scope type is declared for %s, %s", name, table, why).
		WithHints(fmt.Sprintf("declare one where %s are queried: //kanna:scope table=%[1]s", table))}}
}

// keys matches the parameters naming columns to the fields of the model holding them.
func (r repoReader) keys(m *repoMethod, keyParams []*types.Var) []diag.Diag {
	var ds []diag.Diag
	// A key is named after the field holding its column, as customerID for CustomerID, whatever the column's name.
	param := func(c column) string { return lowerFirst(c.path[len(c.path)-1]) }
	for _, p := range keyParams {
		i := slices.IndexFunc(m.columns, func(c column) bool { return param(c) == p.Name() })
		if i < 0 {
			d := diag.Errorf(m.pos, "%s: %s has no field for the parameter %s", m.name, typeName(m.model), p.Name())
			names := make([]string, 0, len(m.columns))
			for _, c := range m.columns {
				names = append(names, param(c))
			}
			if near, ok := suggest.Closest(p.Name(), names); ok {
				d = d.WithHints(fmt.Sprintf("did you mean %s?", near))
			}
			ds = append(ds, d)
			continue
		}
		c := m.columns[i]
		name := c.col.Name
		if _, ptr := p.Type().Underlying().(*types.Pointer); ptr && fieldTakes(c.typ, p.Type()) {
			d := diag.Errorf(m.pos, "%s: %s is %s, but a key is compared by its value, and nil would compare by "+
				"= NULL, which matches no row", m.name, p.Name(), typeName(p.Type())).
				WithHints("declare it as " + typeName(elemOf(p.Type())))
			if !c.col.NotNull {
				d = d.WithHints(fmt.Sprintf("narrow to the rows whose %s is NULL by the scope's %sIsNull", name,
					c.path[len(c.path)-1]))
			}
			ds = append(ds, d)
			continue
		}
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
	if m.verb == verbUpdate {
		return m.updateShaped()
	}
	ok := m.returnsFit()
	switch m.verb { //nolint:exhaustive // the verbs whose parameters tell more
	case verbInsert, verbUpsert, verbCopy:
		return ok && m.row != "" && len(m.keys) == 0 && !m.scoped && (m.verb != verbCopy || m.many)
	case verbDelete:
		// A Delete returning error alone deletes the row of its key, and one returning more the rows its scopes or keys
		// select.
		selects := m.returns == returnsNone && !m.scoped || m.returns != returnsNone && (m.scoped || len(m.keys) > 0)
		return ok && selects && m.row == ""
	}
	return ok && m.row == ""
}

// returnsFit reports whether what m returns is what its verb returns, whatever its parameters.
func (m repoMethod) returnsFit() bool {
	switch m.verb {
	case verbFind:
		return m.returns == returnsRow && m.optional
	case verbGet, verbFirst, verbSum, verbMin, verbMax, verbAvg:
		return m.returns == returnsRow && !m.optional
	case verbList:
		return m.returns == returnsRows
	case verbCount, verbCopy:
		return m.returns == returnsCount
	case verbExists:
		return m.returns == returnsBool
	case verbInsert, verbUpsert:
		return m.returns == returnsNone || m.returns == returnsRow && !m.many || m.returns == returnsRows && m.many
	case verbUpdate:
		return m.returns == returnsNone || m.returns == returnsRow ||
			m.changes != "" && (m.returns == returnsCount || m.returns == returnsRows)
	case verbDelete:
		return m.returns == returnsNone || m.returns == returnsCount || m.returns == returnsRows
	}
	return false
}

// oneRowKey reports why names, the key columns of m, a Get or a Find, read no one row, if they do not.
func (m *repoMethod) oneRowKey(names []string) (diag.Diag, bool) {
	errorf := func(format string, args ...any) diag.Diag {
		return diag.Errorf(m.pos, "%s: %s", m.name, fmt.Sprintf(format, args...))
	}
	switch {
	case len(names) == 0:
		return errorf("%s reads one row by a key, but it takes none", m.verb).WithHints(fmt.Sprintf(
			"take a unique key of %s as parameters, or use First", m.target.table)), false
	case !containsKey(m.table, names):
		return errorf("%s is not a unique key of %s, so %s cannot return one row",
			strings.Join(names, ", "), m.target.table, m.verb).WithHints(fmt.Sprintf(
			"use List, or add a unique index on %s (%s)", m.target.table, strings.Join(names, ", "))), false
	}
	return diag.Diag{}, true
}

// changeSetRows reports why names, the key columns of m, an Update of a change set, select no rows it can write, if
// they do not.
func (m *repoMethod) changeSetRows(names []string) (diag.Diag, bool) {
	errorf := func(format string, args ...any) diag.Diag {
		return diag.Errorf(m.pos, "%s: %s", m.name, fmt.Sprintf(format, args...))
	}
	switch {
	case len(names) == 0 && !m.scoped:
		return errorf("an Update of a change set writes the rows its keys or scopes select, but it takes neither").
			WithHints(fmt.Sprintf("take a unique key of %s, or scopes that select the rows", m.target.table)), false
	case (m.returns == returnsNone || m.returns == returnsRow) && !containsKey(m.table, names):
		return errorf("a single-row Update requires a unique key; use (int64, error) or ([]M, error) for multiple rows"),
			false
	}
	return diag.Diag{}, true
}

// verbRules reports what else a method of its verb asks of its table.
func (r repoReader) verbRules(m *repoMethod) []diag.Diag {
	errorf := func(format string, args ...any) diag.Diag {
		return diag.Errorf(m.pos, "%s: %s", m.name, fmt.Sprintf(format, args...))
	}
	names := m.keyColumns()
	if m.verb == verbGet || m.verb == verbFind {
		if d, ok := m.oneRowKey(names); !ok {
			return []diag.Diag{d}
		}
	}
	if m.verb == verbUpdate && m.changes != "" {
		if d, ok := m.changeSetRows(names); !ok {
			return []diag.Diag{d}
		}
	}
	switch {
	case m.verb == verbDelete && m.returns == returnsNone && !containsKey(m.table, names):
		return []diag.Diag{errorf("(%s) is no key of %s, so Delete would delete more than the one row",
			strings.Join(names, ", "), m.target.table).WithHints(
			"return (int64, error) for the number of rows deleted, or take a key of the table")}
	case m.verb == verbDelete && m.returns == returnsRows && r.s.Dialect == snapshot.MySQL:
		return []diag.Diag{errorf("MySQL has no DELETE RETURNING; return error alone or (int64, error)")}
	case m.verb == verbUpdate && (m.returns == returnsRow || m.returns == returnsRows) && r.s.Dialect == snapshot.MySQL:
		return []diag.Diag{errorf("MySQL has no UPDATE RETURNING; return error alone or (int64, error)")}
	case m.verb == verbUpdate && m.changes == "" && len(m.table.PrimaryKey) == 0:
		return []diag.Diag{errorf("%s has no primary key to tell its rows by", m.target.table)}
	case m.verb == verbUpsert && len(m.rowKey()) == 0:
		return []diag.Diag{errorf("%s has no primary key to tell its rows by", m.target.table).WithHints(
			"name a unique key of " + m.target.table + " with //kanna:conflict")}
	case (m.verb == verbInsert || m.verb == verbUpsert) && m.many && m.returns != returnsNone &&
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

// conflict reads the //kanna:conflict of an upsert: the columns of the key it meets a row by.
func (r repoReader) conflict(m *repoMethod, d directive.Directive) []diag.Diag {
	errorf := func(format string, args ...any) diag.Diag {
		return diag.Errorf(d.Pos, "%s: %s", m.name, fmt.Sprintf(format, args...))
	}
	if m.verb != verbUpsert {
		return []diag.Diag{errorf("//kanna:conflict is for an Upsert")}
	}
	set, _ := changeSetNames(m.target.name)
	alone := []string{`a column an upsert leaves as it was is tagged orm:",insertonly"`,
		"an upsert writing chosen columns over the row it meets takes a change set: Upsert(ctx, row, changes " + set + ")"}
	args, ds := d.Parse(directive.Spec{Any: "<columns>", AnyWord: true, Needed: true})
	switch {
	case len(ds) > 0 && len(strings.Fields(d.Args)) == 0:
		example := "tenant_id,email"
		if len(m.table.UniqueKeys) > 0 {
			example = strings.Join(m.table.UniqueKeys[0], ",")
		}
		return hinted(methodDiags(m.name, ds), "as in //kanna:conflict "+example)
	case len(ds) > 0:
		return hinted(methodDiags(m.name, ds), alone...)
	case len(args) > 1 && strings.Contains(d.Args, ","):
		joined := strings.Join(strings.Fields(strings.ReplaceAll(d.Args, ",", " ")), ",")
		return []diag.Diag{errorf("//kanna:conflict names the columns of a key by commas alone, with no spaces").
			WithHints("as in //kanna:conflict " + joined)}
	case len(args) > 1:
		return []diag.Diag{errorf("//kanna:conflict names the columns of a key alone, not %q", args[1].Name).
			WithHints(alone...)}
	}
	m.conflict = strings.Split(args[0].Name, ",")
	for _, c := range m.conflict {
		if !slices.ContainsFunc(m.table.Columns, func(col snapshot.Column) bool { return col.Name == c }) {
			return []diag.Diag{errorf("the snapshot has no column %s.%s", m.target.table, c).WithHints(describeAgain)}
		}
	}
	if !unique(m.table, m.conflict) {
		return []diag.Diag{errorf("(%s) is neither the primary key of %s nor one of its unique keys",
			strings.Join(m.conflict, ", "), m.target.table)}
	}
	return nil
}

// otherKeys warns of m, an upsert on MySQL, which updates a row on a conflict over any unique key, of the keys besides
// the one it meets its row by that its insert may conflict over: all but one holding that key, which meets the same
// row, and one the database assigns every column of, as it does not a column the insert writes when the row gives it.
func (r repoReader) otherKeys(m repoMethod) []diag.Diag {
	if m.verb != verbUpsert || r.s.Dialect != snapshot.MySQL {
		return nil
	}
	met := m.rowKey()
	assigned := func(name string) bool {
		i := slices.IndexFunc(m.table.Columns, func(c snapshot.Column) bool { return c.Name == name })
		return i >= 0 && m.table.Columns[i].Assigned && !m.gives(name)
	}
	var others []string
	written := false
	for _, k := range append([][]string{m.table.PrimaryKey}, m.table.UniqueKeys...) {
		holds := !slices.ContainsFunc(met, func(c string) bool { return !slices.Contains(k, c) })
		if len(k) == 0 || holds || !slices.ContainsFunc(k, func(c string) bool { return !assigned(c) }) {
			continue
		}
		others = append(others, "("+strings.Join(k, ", ")+")")
		written = written || slices.ContainsFunc(k, m.gives)
	}
	if len(others) == 0 {
		return nil
	}
	d := diag.Warningf(m.pos, "%s: on MySQL, a conflict on %s also updates the row", m.name,
		strings.Join(others, " or ")).WithHints(fmt.Sprintf("%s has unique keys other than (%s)", m.target.table,
		strings.Join(met, ", ")))
	if given, _ := m.given(); written {
		d = d.WithHints(fmt.Sprintf("an insert writes %s of a row whose %s is not zero", given.col.Name,
			fieldPath(given)))
	}
	return []diag.Diag{d}
}

func sameColumns(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

// writeRules reports what keeps a write from writing its rows as the model holds them.
func (r repoReader) writeRules(m repoMethod) []diag.Diag {
	if m.verb != verbInsert && m.verb != verbUpdate && m.verb != verbUpsert && m.verb != verbCopy {
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
		if m.missingInsertColumns() {
			ds = append(ds, errorf("%s holds no column of %s but those the database assigns", model, m.target.table))
		}
	}
	ds = append(ds, m.writeKeyRules()...)
	ds = append(ds, r.unnestRules(m)...)
	if m.verb == verbUpdate && len(m.updated()) == 0 {
		ds = append(ds, errorf("%s holds no column of %s to update but its key", model, m.target.table))
	}
	if m.verb == verbInsert && m.returns == returnsRow && r.s.Dialect == snapshot.MySQL {
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
	return append(ds, m.defaultConstraints(r.s.Dialect)...)
}

// unnestRules reports what keeps m, a write of many rows on PostgreSQL but a Copy, from writing them in one statement
// of the arrays unnest takes. A Copy leaves the column the database assigns out when its rows give it none, rather
// than write what the database assigns it by, and copies an array as any other value.
func (r repoReader) unnestRules(m repoMethod) []diag.Diag {
	if !m.many || m.verb == verbCopy || r.s.Dialect != snapshot.PostgreSQL {
		return nil
	}
	var ds []diag.Diag
	if given, ok := m.given(); ok && given.col.AssignedBy == "" {
		ds = append(ds, diag.Errorf(m.pos, "%s: the snapshot does not tell how the database assigns %s.%s, which the "+
			"rows giving none get", m.name, m.target.table, given.col.Name).
			WithHints("describe the database again with go tool kanna-orm describe"))
	}
	for _, c := range m.inserted() {
		if strings.HasSuffix(c.col.Type, "[]") {
			d := diag.Errorf(m.pos, "%s: unnest, which writes the rows in one statement, cannot write %s, an array",
				m.name, c.col.Name).WithHints("write the rows one by one")
			if m.verb == verbInsert && r.driver == driverPgx {
				d = d.WithHints("or copy them: Copy(ctx context.Context, rows []" + typeName(m.model) + ") (int64, error)")
			}
			ds = append(ds, d)
		}
	}
	return ds
}

// inserted are the columns a write of m inserts: those not supplied by the database.
func (m repoMethod) inserted() []column {
	return slices.DeleteFunc(slices.Clone(m.columns), func(c column) bool { return c.col.Assigned || c.insertDefault })
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
// key, those tagged insertonly, and the creation time. An upsert of a row keeps a column default leaves out of INSERT,
// too, rather than reset it to its database default on a conflict; one of a change set writes it when the change set
// does. The update time is among them, written by the clock, but for one default leaves to the database.
func (m repoMethod) updated() []column {
	skip := slices.Concat(m.rowKey(), m.table.PrimaryKey)
	return slices.DeleteFunc(slices.Clone(m.columns), func(c column) bool {
		return c.col.Assigned || c.insertOnly || slices.Contains(skip, c.col.Name) ||
			m.verb == verbUpsert && m.changes == "" && c.insertDefault ||
			c.audit == auditCreated || c.audit == auditUpdated && c.insertDefault
	})
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

// unscoped tells the struct t is, or holds a slice of, when no scope type is declared for it: a row of no table
// kanna-orm knows of.
func (r repoReader) unscoped(t types.Type) (*types.Named, bool) {
	if s, ok := t.Underlying().(*types.Slice); ok {
		t = s.Elem()
	}
	n := asNamed(t)
	if n == nil || !isModel(n) {
		return nil, false
	}
	_, ok := r.l.scopeOf(n)
	return n, !ok
}

// noScopeType tells that m reads or writes rows of model, for which no scope type is declared.
func noScopeType(m repoMethod, model *types.Named) []diag.Diag {
	return []diag.Diag{diag.Errorf(m.pos, "%s: no scope type is declared for %s", m.name, typeName(model)).
		WithHints("declare one where its rows are queried: //kanna:scope table=<table>")}
}

// scopeModel tells the model of a scope type t: M, of its one field, an scope.Of[M].
func scopeModel(t types.Type) (*types.Named, bool) { return scopeModelKind(t, "Of") }

func scopeModelKind(t types.Type, kind string) (*types.Named, bool) {
	n := asNamed(t)
	if n == nil {
		return nil, false
	}
	st, ok := n.Underlying().(*types.Struct)
	if !ok || st.NumFields() != 1 {
		return nil, false
	}
	// A scope type holds a scope.Of, and a change set the runtime's Changes, which change.Set names.
	path := implPath
	if kind == "Of" {
		path = scopePath
	}
	of := asNamed(st.Field(0).Type())
	if of == nil || of.Obj().Pkg() == nil || of.Obj().Pkg().Path() != path || of.Obj().Name() != kind {
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

// plucks reports whether t, the result of a method of verb v, is the value of a column rather than a row: a value of
// a Get or a First, a pointer to one of a Find, or a slice of them of a List.
func plucks(v verb, t types.Type) bool {
	switch v {
	case verbFind:
		ptr, ok := t.(*types.Pointer)
		if !ok {
			return false
		}
		t = ptr.Elem()
	case verbList:
		slice, ok := t.Underlying().(*types.Slice)
		if !ok {
			return false
		}
		t = slice.Elem()
	case verbGet, verbFirst:
	case verbCount, verbExists, verbInsert, verbUpdate, verbUpsert, verbDelete, verbCopy, verbSum, verbMin, verbMax,
		verbAvg:
		return false
	}
	_, row := elemOf(t).Underlying().(*types.Struct)
	return !row || holdsColumn(elemOf(t))
}

// findOfNull reports name, a Find returning *t, which reads column, which may be NULL: nil would say both that there
// is no row and that its value is NULL.
func findOfNull(pos token.Position, name, column string, t types.Type) diag.Diag {
	return diag.Errorf(pos, "%s: a Find returns nil for no row, so it cannot read column %s, which may be NULL", name,
		column).WithHints(fmt.Sprintf("name it Get, whose (*%[1]s, error) returns nil for NULL and orm.ErrNotFound for "+
		"no row, or return (**%[1]s, error) to tell the two apart", types.TypeString(t, nil)))
}

// pluckColumn finds the column m reads the values of, as t, its result: that of the field its name says after the
// verb, as GetName reads that of Name, or the plural of after List, as ListNames and ListCustomerIDs read those of
// Name and CustomerID, before the lock it may end in, as ListIDsForKeyShare reads that of ID. The value is of the
// field's type.
func (r repoReader) pluckColumn(m *repoMethod, t types.Type) []diag.Diag {
	switch {
	case m.verb == verbList:
		t = t.Underlying().(*types.Slice).Elem() //nolint:forcetypeassert // plucks saw a slice
	case m.optional:
		t = t.(*types.Pointer).Elem() //nolint:forcetypeassert // plucks saw a pointer
	}
	rest := strings.TrimPrefix(m.name, string(m.verb))
	lock := lockedName.FindString(rest)
	rest = strings.TrimSuffix(rest, lock)
	misspelled := ""
	for _, c := range m.columns {
		field := c.path[len(c.path)-1]
		if m.verb == verbList && rest == field+"s" && !endsInAcronym(field) {
			misspelled = field
		}
		if m.verb == verbList && !pluralOf(rest, field) || m.verb != verbList && rest != field {
			continue
		}
		if p, ok := c.typ.(*types.Pointer); ok && m.optional && types.Identical(t, p.Elem()) {
			return []diag.Diag{findOfNull(m.pos, m.name, c.col.Name, t)}
		}
		if !types.Identical(t, c.typ) {
			return []diag.Diag{diag.Errorf(m.pos, "%s: it reads %s.%s, %s %s, as %s %s", m.name,
				m.model.Obj().Name(), strings.Join(c.path, "."), article(typeName(c.typ)), typeName(c.typ),
				article(typeName(t)), typeName(t)).WithHints("declare it of the field's type, " + typeName(c.typ))}
		}
		m.pluck = &c
		return nil
	}
	missing := fmt.Sprintf("%s has no field named %s", typeName(m.model), rest)
	switch {
	case rest == "" && lock == "":
		missing = "names no field after " + string(m.verb)
	case rest == "":
		missing = fmt.Sprintf("names no field between %s and %s", m.verb, lock)
	case m.verb == verbList:
		missing = fmt.Sprintf("%s has no field whose plural is %s", typeName(m.model), rest)
	}
	names := r.shapeNames(m.model)
	hint := "name it after a field, as in " + names.fill(pluckShape)
	if lock != "" {
		hint = "name it after a field, then its lock, as List{plural}" + lock + " reads {key}"
		hint = names.fill(hint)
	}
	if misspelled != "" {
		hint = fmt.Sprintf("name it List%s%s: the plural of %s is made as that of a table's name",
			camelCase(decl.Plural(misspelled)), lock, misspelled)
	}
	return []diag.Diag{diag.Errorf(m.pos, "%s: it returns the values of a column, but %s", m.name, missing).
		WithHints(hint)}
}

// pluralOf reports whether name, what follows List in the name of a method reading the values of a column, is the
// plural of field: made as that of a table's name is, as Categories of Category, or, of a field ending in an acronym,
// whose words that rule cannot tell, with an s, as CustomerIDs of CustomerID.
func pluralOf(name, field string) bool {
	return decl.SnakeCase(name) == decl.Plural(field) || endsInAcronym(field) && name == field+"s"
}

// endsInAcronym reports whether name ends in two capitals or more, as CustomerID and URL do.
func endsInAcronym(name string) bool {
	r := []rune(name)
	return len(r) >= 2 && unicode.IsUpper(r[len(r)-1]) && unicode.IsUpper(r[len(r)-2])
}

// result reads t, what m returns besides its error: an aggregate or the values of a column, which ofColumn reports and
// resultColumn finds once the model of m is known, or rows, whose model it adds to models.
func (r repoReader) result(m *repoMethod, t types.Type, models []*types.Named) (_ []*types.Named, ofColumn, ok bool) {
	switch {
	case aggregates(m.verb):
		m.returns = returnsRow
		return models, true, true
	case plucks(m.verb, t):
		m.returns, m.optional = returnsRow, m.verb == verbFind
		if m.verb == verbList {
			m.returns = returnsRows
		}
		return models, true, true
	}
	models, ok = r.resultModels(m, t, models)
	return models, false, ok
}

// resultColumn finds the column whose aggregate or values m reads as t, its result.
func (r repoReader) resultColumn(m *repoMethod, t types.Type) []diag.Diag {
	if aggregates(m.verb) {
		return r.aggregateColumn(m, t)
	}
	return r.pluckColumn(m, t)
}

func (r repoReader) resultModels(m *repoMethod, t types.Type, models []*types.Named) ([]*types.Named, bool) {
	switch {
	case isBasic(t, types.Int64):
		m.returns = returnsCount
	case isBasic(t, types.Bool):
		m.returns = returnsBool
	default:
		if ptr, ok := t.(*types.Pointer); ok && m.verb == verbFind {
			t, m.optional = ptr.Elem(), true
		}
		reads := m.verb == verbGet || m.verb == verbFind || m.verb == verbFirst || m.verb == verbList || m.verb == verbDelete
		if reads && m.scoped && len(models) == 1 {
			row, many := t, false
			if slice, ok := t.Underlying().(*types.Slice); ok {
				row, many = slice.Elem(), true
			}
			model := asNamed(row)
			if model != nil && isModel(model) && !holdsColumn(model) && !types.Identical(model, models[0]) {
				if _, registered := r.l.scopeOf(model); registered {
					return append(models, model), true
				}
				m.returns = returnsRow
				if many {
					m.returns = returnsRows
				}
				m.projection = &readProjection{model: model}
				return models, true
			}
		}
		model, many, ok := r.rowModel(t)
		if !ok {
			return nil, false
		}
		m.returns = returnsRow
		if many {
			m.returns = returnsRows
		}
		return append(models, model), true
	}
	return models, true
}

func (r repoReader) methodParameters(m *repoMethod, sig *types.Signature, field *ast.Field, ts *ast.TypeSpec,
) ([]*types.Named, []*types.Var, []diag.Diag) {
	params := sig.Params()
	errorf := func(format string, args ...any) []diag.Diag {
		return []diag.Diag{diag.Errorf(m.pos, "%s: %s", m.name, fmt.Sprintf(format, args...))}
	}
	var models []*types.Named
	keyParams := []*types.Var{}
	for i := 1; i < params.Len(); i++ {
		p := params.At(i)
		if sig.Variadic() && i == params.Len()-1 {
			model, implied, ok := r.scopeParam(p, field, ts)
			m.impliedScope = implied
			if !ok {
				return nil, nil, errorf("its last parameter, %s, takes no scope type", p.Name())
			}
			m.scoped, models = true, append(models, model)
			continue
		}
		if model, ok := r.changesParam(p, field, ts); ok {
			if m.verb != verbUpdate && m.verb != verbUpsert || m.changes != "" || m.verb == verbUpdate && m.row != "" {
				return nil, nil, errorf("a change set is taken once, by an Update without a row or by an Upsert of " +
					"one row")
			}
			m.changes, models = p.Name(), append(models, model)
			continue
		}
		if model, many, ok := r.rowModel(p.Type()); ok {
			if m.row != "" || m.changes != "" && m.verb == verbUpdate {
				return nil, nil, errorf("it takes both %s and %s, rows to write", m.row, p.Name())
			}
			m.row, m.many, models = p.Name(), many, append(models, model)
			continue
		}
		if model, ok := r.unscoped(p.Type()); ok {
			return nil, nil, noScopeType(*m, model)
		}
		if value, ok := valueOfPointers(p.Type()); ok {
			if _, _, ok := r.rowModel(value); ok {
				return nil, nil, []diag.Diag{diag.Errorf(m.pos, "%s: it takes %s, %s, and rows to write are taken "+
					"by their values", m.name, p.Name(), typeName(p.Type())).WithHints("declare it as " + typeName(value))}
			}
		}
		if p.Name() == "" || p.Name() == "_" {
			return nil, nil, errorf("its parameter %d has no name, which tells the column it stands for", i+1)
		}
		keyParams = append(keyParams, p)
	}
	if m.changes != "" && m.many {
		return nil, nil, []diag.Diag{diag.Errorf(m.pos, "%s: an Upsert of many rows takes no change set, whose values "+
			"would be written over every row it meets", m.name).WithHints("take one row", "write the values of each "+
			`row: take no change set, and tag the columns an upsert leaves as they were orm:",insertonly"`)}
	}
	return models, keyParams, nil
}

// valueOfPointers is t, a pointer or a slice of pointers, of the values they point to.
func valueOfPointers(t types.Type) (types.Type, bool) {
	if s, ok := t.Underlying().(*types.Slice); ok {
		if p, ok := s.Elem().Underlying().(*types.Pointer); ok {
			return types.NewSlice(p.Elem()), true
		}
	}
	if p, ok := t.Underlying().(*types.Pointer); ok {
		return p.Elem(), true
	}
	return nil, false
}

// keyColumns are the columns the key parameters of m name, in their order.
func (m repoMethod) keyColumns() []string {
	names := make([]string, 0, len(m.keys))
	for _, k := range m.keys {
		names = append(names, k.col.col.Name)
	}
	return names
}

// selects reports whether m, an Update of a change set, writes the rows it selects as a Delete taking scopes deletes
// them, a page of them too, selected by key in a subquery that a lock can hold: its key parameters, if any, are
// conditions, as a Delete's are, and not a unique key, which names the one row it writes, as that of a Get does.
func (m repoMethod) selects() bool {
	return m.verb == verbUpdate && m.changes != "" && !containsKey(m.table, m.keyColumns())
}

func (m repoMethod) updateShaped() bool {
	if m.changes != "" {
		return m.row == "" && (m.returns == returnsNone || m.returns == returnsRow ||
			m.returns == returnsCount || m.returns == returnsRows)
	}
	return m.row != "" && !m.many && len(m.keys) == 0 &&
		(m.returns == returnsNone || m.returns == returnsRow)
}

func (m repoMethod) writeKeyRules() []diag.Diag {
	var ds []diag.Diag
	model := typeName(m.model)
	held := func(name string) bool {
		return slices.ContainsFunc(m.columns, func(c column) bool { return c.col.Name == name })
	}
	errorf := func(format string, args ...any) diag.Diag {
		return diag.Errorf(m.pos, "%s: %s", m.name, fmt.Sprintf(format, args...))
	}
	if m.verb == verbUpsert || m.verb == verbUpdate && m.changes == "" {
		for _, name := range m.rowKey() {
			i := slices.IndexFunc(m.table.Columns, func(c snapshot.Column) bool { return c.Name == name })
			switch {
			case !held(name):
				ds = append(ds, errorf("%s has no field for %s, which it meets its row by", model, name))
			case m.verb == verbUpsert && (m.table.Columns[i].Assigned && !m.gives(name) || m.field(name).insertDefault):
				ds = append(ds, errorf("the database assigns %s, so an insert never meets a row by it", name).
					WithHints(fmt.Sprintf("name another unique key of %s with //kanna:conflict", m.target.table)))
			}
		}
	}
	return ds
}

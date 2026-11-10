package gen

import (
	"cmp"
	"fmt"
	"go/types"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/go-kanna/internal/gofile"
	"github.com/go-kanna/internal/packages"

	"github.com/go-kanna/orm/internal/snapshot"
)

// driver is the package the implementations of repositories run on.
type driver string

const (
	driverPgx driver = "pgx"
	driverSQL driver = "database/sql"
)

// repoEmitter writes the implementations of repositories into a file of their destination.
type repoEmitter struct {
	f   *gofile.File
	d   snapshot.Dialect
	drv driver
	// exec, query, and queryRow are the names of the methods of DBTX running a statement.
	exec, query, queryRow string
	database, rows        string
	// models are the models whose whole rows the repositories read, by table, and helpers the functions reading them
	// and loading their relations.
	models  map[string]readModel
	helpers map[string]tableHelpers
	// eachNames name the impl.Each of each relation the helpers load, by eachKey.
	eachNames map[string]string
	// copies reports whether a repository copies rows, for which DBTX copies them.
	copies bool
}

// tableHelpers name the functions of the implementations reading the whole rows of a table that a scope matches,
// those a query reads, and loading the relations of rows: kSelect<name>, kQuery<name>, and kPreload<name>, or
// the first of kSelect<name>2, ... that the package does not declare.
type tableHelpers struct {
	selectRows, queryRows, preload string
}

// emitRepositories writes repository_gen.go into the destination package pkgName at pkgPath: the implementation of each
// of repos on a database of d through drv, reading the whole rows of models. declared names what the destination
// declares besides.
func emitRepositories(pkgPath, pkgName string, d snapshot.Dialect, drv driver, repos []repository,
	models map[string]readModel, declared []string,
) ([]byte, []string, error) {
	f := gofile.New(generator, pkgPath, pkgName)
	f.Reserve(declared...)
	f.Reserve("DBTX")
	for _, repo := range repos {
		f.Reserve(repo.generates()...)
	}
	e := repoEmitter{f: f, d: d, drv: drv, exec: "Exec", query: "Query", queryRow: "QueryRow", models: models,
		helpers: map[string]tableHelpers{}, eachNames: map[string]string{},
		copies: drv == driverPgx && slices.ContainsFunc(repos, func(r repository) bool { return r.copies })}
	if drv == driverSQL {
		e.exec, e.query, e.queryRow = "ExecContext", "query", "queryRow"
	}
	names := readNames(models)
	for _, table := range slices.Sorted(maps.Keys(names)) {
		sc := f.Scope()
		h := tableHelpers{selectRows: sc.Name("kSelect" + names[table]), queryRows: sc.Name("kQuery" + names[table]),
			preload: sc.Name("kPreload" + names[table])}
		f.Reserve(h.selectRows, h.queryRows, h.preload)
		e.helpers[table] = h
		for _, lk := range models[table].links {
			name := sc.Name("kEach" + names[table] + strings.Join(lk.field.path, ""))
			f.Reserve(name)
			e.eachNames[eachKey(table, lk)] = name
		}
	}
	rows := "kSQLRows"
	if drv == driverPgx {
		rows = "kPgxRows"
	}
	e.database, e.rows = f.Scope().Name("kDatabase"), f.Scope().Name(rows)
	f.Reserve(e.database, e.rows)

	e.db()
	e.hooks()
	for _, repo := range repos {
		e.repository(repo)
	}
	for _, table := range slices.Sorted(maps.Keys(models)) {
		e.selectRows(models[table])
		e.queryRows(models[table])
		if len(models[table].links) > 0 {
			e.preloadRows(models[table])
		}
	}

	src, err := f.Bytes()
	if err != nil {
		return src, nil, fmt.Errorf("the code generated does not parse: %w", err)
	}
	return src, f.ModuleImports(), nil
}

// ref names name of the runtime.
func (e repoEmitter) ref(name string) string { return runtimeRef(e.f, name) }

func (e repoEmitter) fmtPkg() string {
	return e.f.Import("fmt", "fmt")
}

func (e repoEmitter) printf(format string, args ...any) {
	fmt.Fprintf(e.f, format, args...)
}

// db writes DBTX, what the repositories run on, named as sqlc names it: the one of the runtime, which the packages of
// repositories all declare theirs as, so that one value serves them all, or the runtime's CopyDBTX when they copy
// rows, so that a connection that cannot copy them does not compile.
func (e repoEmitter) db() {
	if e.drv == driverSQL {
		e.printf("// DBTX is what the repositories run on: *sql.DB, *sql.Conn, or *sql.Tx.\ntype DBTX = %s.DBTX\n\n",
			e.f.Import(stdlibPath, "stdlib"))
		return
	}
	e.f.Import("github.com/jackc/pgx/v5", "pgx") // the pgx the code reads, and the runtime's under another name
	kpgx := e.f.Import(pgxPath, "kpgx")
	if e.copies {
		e.printf("// DBTX is what the repositories run on: *pgxpool.Pool, *pgx.Conn, or pgx.Tx, whose CopyFrom a Copy "+
			"writes its rows by.\ntype DBTX = %s.CopyDBTX\n\n", kpgx)
		return
	}
	e.printf("// DBTX is what the repositories run on: *pgxpool.Pool, *pgx.Conn, or pgx.Tx.\ntype DBTX = %s.DBTX\n\n",
		kpgx)
}

// repository writes the implementation of repo: its struct, constructor, and methods.
func (e repoEmitter) repository(repo repository) {
	iface := repo.name
	if p := repo.iface; p != nil {
		if pkg := e.f.Import(p.Path(), p.Name()); pkg != "" {
			iface = pkg + "." + repo.name
		}
	}
	database := "PostgreSQL"
	if e.d == snapshot.MySQL {
		database = "MySQL"
	}
	name, constructor := repo.implementation(), repo.constructor()
	e.printf("// %[1]s implements %[2]s on %[3]s through %[4]s.\n"+
		"type %[1]s struct{ db %[5]s }\n\n", name, iface,
		database, e.drv, e.database)
	e.printf("func %[1]s(db DBTX, opts ...%[2]s) %[3]s { return %[3]s{db: %[4]s{dbtx: db, options: %[5]s(opts...)}} }\n\n",
		constructor, e.ref("Option"), name, e.database, e.ref("NewOptions"))
	if repo.publicConstructor {
		// A same-package implementation is unexported, and so the constructor returns the interface.
		result := name
		if repo.local {
			result = repo.name
		}
		e.printf("// New%[1]s returns %[2]s running on db.\nfunc New%[1]s(db DBTX, opts ...%[4]s) %[2]s { "+
			"return %[3]s(db, opts...) }\n\n", repo.name, result, constructor, e.ref("Option"))
	}
	e.printf("var _ %s = %s{}\n\n", iface, name)
	e.operations(repo)
	for _, m := range repo.methods {
		e.method(repo, m)
	}
}

func (r repository) implementation() string {
	if r.local {
		return lowerFirst(r.name)
	}
	return r.name
}

// constructor is the unexported function returning the implementation, which New<Repository> calls, and a New of
// the repository written by hand can.
func (r repository) constructor() string {
	return "new" + r.name
}

// operations writes the operations of repo, which its mapError takes, if it does: a constant of the type for each
// method, holding the method's name.
func (e repoEmitter) operations(repo repository) {
	if len(repo.operations) == 0 {
		return
	}
	typ := repo.operation()
	e.printf("// %s is the method of %s an error comes from, as %s.%s takes it.\ntype %s string\n\n", typ, repo.name,
		repo.implementation(), mapErrorName, typ)
	e.printf("const (\n")
	for _, m := range repo.operations {
		e.printf("%s %s = %q\n", repo.operationOf(m), typ, m)
	}
	e.printf(")\n\n")
}

// signature is the signature of a method as written, with the names of its parameters in the body.
type signature struct {
	text string
	// ctx, scopes, and row name the context, the scopes, and the row in the body; params, the others, by the names the
	// interface gives them.
	ctx, scopes, row string
	params           map[string]string
	// recv is the receiver, when the errors the method returns go through its mapError, and op the operation of the
	// method, when mapError takes it.
	recv, op string
	// now names the time of the clock, when the method writes a column by it.
	now string
}

// signature writes the parameters and the results of m, naming each parameter in sc.
func (e repoEmitter) signature(m repoMethod, sc gofile.Scope) signature {
	sig := m.fn.Signature()
	s := signature{params: map[string]string{}}
	params := make([]string, 0, sig.Params().Len())
	for i := range sig.Params().Len() {
		p := sig.Params().At(i)
		name, variadic := p.Name(), sig.Variadic() && i == sig.Params().Len()-1
		if name == "" || name == "_" {
			switch {
			case i == 0:
				name = "ctx"
			case variadic:
				name = "s"
			default:
				name = "row"
			}
		}
		local := sc.Name(name)
		switch {
		case i == 0:
			s.ctx = local
		case variadic && m.scoped:
			s.scopes = local
		case p.Name() == m.row && m.row != "":
			s.row = local
		default:
			s.params[p.Name()] = local
		}
		if s, ok := p.Type().(*types.Slice); ok && variadic {
			elem := types.TypeString(s.Elem(), e.f.Qualifier)
			if is := m.impliedScope; is != nil && m.scoped {
				elem = is.name
				if pkg := e.f.Import(is.pkgPath, is.pkgName); pkg != "" {
					elem = pkg + "." + is.name
				}
			}
			params = append(params, local+" ..."+elem)
			continue
		}
		typ := e.parameterType(m, p)
		params = append(params, local+" "+typ)
	}
	results := make([]string, 0, sig.Results().Len())
	for r := range sig.Results().Variables() {
		results = append(results, types.TypeString(r.Type(), e.f.Qualifier))
	}
	s.text = "(" + strings.Join(params, ", ") + ") "
	if len(results) == 1 {
		s.text += results[0]
	} else {
		s.text += "(" + strings.Join(results, ", ") + ")"
	}
	return s
}

// bind writes Bind, its parameter named as the interface names it. Its body refers to no package, so the package of
// the parameter's type keeps its name, as in Bind(tx tx.Tx) written by hand.
func (e repoEmitter) bind(repo repository, m repoMethod, sc gofile.Scope, recv string) {
	sig := m.fn.Signature()
	p := sig.Params().At(0)
	typ := "DBTX"
	if !m.bind.localDBTX {
		typ = e.parameterType(m, p)
	}
	result := types.TypeString(sig.Results().At(0).Type(), e.f.Qualifier)
	name := p.Name()
	if name == "" || name == "_" {
		name = "db"
	}
	if name == recv || name == repo.constructor() {
		name = sc.Name(name)
	}
	input := name
	if m.bind.accessor {
		input += ".DBTX()"
	}
	e.printf("func (%s %s) %s(%s %s) %s {\nreturn %s(%s, %s.db.options.Option())\n}\n\n",
		recv, repo.implementation(), m.name, name, typ, result, repo.constructor(), input, recv)
}

// dialect is the orm.Dialect of the database, as the file names it.
func (e repoEmitter) dialect() string {
	if e.d == snapshot.MySQL {
		return e.ref("MySQL")
	}
	return e.ref("Postgres")
}

// method writes the method m of repo.
func (e repoEmitter) method(repo repository, m repoMethod) {
	sc := e.f.Scope()
	recv := sc.Name("r")
	if m.bind != nil {
		e.bind(repo, m, sc, recv)
		return
	}
	sig := e.signature(m, sc)
	// A helper of a method written by hand returns its errors as they are, for the method to map.
	if repo.mapError && m.bind == nil && m.name == m.fn.Name() {
		sig.recv = recv
		if len(repo.operations) > 0 {
			sig.op = repo.operationOf(m.name)
		}
	}
	if m.query != nil {
		e.printf("// %s runs the query of its name in %s.\n", m.name, m.query.file)
	}
	e.printf("func (%s %s) %s%s {\n", recv, repo.implementation(), m.name, sig.text)
	// A write of many rows reads the clock once it has rows to write.
	if m.clocked() {
		sig.now = sc.Name("now")
		if !m.many {
			e.readClock(recv, sig)
		}
	}
	switch m.verb {
	case "":
		e.handWritten(m, sc, recv, sig)
	case verbInsert, verbUpsert:
		e.insert(m, sc, recv, sig)
	case verbUpdate:
		if m.changes != "" || m.scoped {
			e.updateChanges(m, sc, recv, sig)
		} else {
			e.update(m, sc, recv, sig)
		}
	case verbGet, verbFind, verbFirst, verbList, verbCount, verbExists, verbDelete, verbSum, verbMin, verbMax, verbAvg:
		e.read(m, sc, recv, sig)
	case verbCopy:
		e.copyRows(m, sc, recv, sig)
	}
	e.printf("}\n\n")
}

// notFound is the error saying no row of table has args, the values of the columns names.
func (e repoEmitter) notFound(table string, names, args []string) string {
	if len(names) == 0 {
		return fmt.Sprintf("%s.Errorf(%q, %s)", e.fmtPkg(), "no row of "+table+" matches: %w", e.ref("ErrNotFound"))
	}
	format := make([]string, 0, len(names))
	for _, n := range names {
		format = append(format, n+" %v")
	}
	return fmt.Sprintf("%s.Errorf(%q, %s, %s)", e.fmtPkg(),
		table+" with "+strings.Join(format, " and ")+": %w", strings.Join(args, ", "), e.ref("ErrNotFound"))
}

// read writes the body of m, a method reading or deleting the rows its keys and its scopes match.
func (e repoEmitter) read(m repoMethod, sc gofile.Scope, recv string, sig signature) {
	model := types.TypeString(m.model, e.f.Qualifier)
	table := sqlName(e.d, m.target.table)
	q := sc.Name("q")
	if m.lock != "" && m.pluck == nil {
		zero := model + "{}"
		if m.projection != nil {
			zero = types.TypeString(m.projection.model, e.f.Qualifier) + "{}"
		}
		if m.returns == returnsRows || m.optional {
			zero = "nil"
		}
		e.inTransaction(m, recv, sig, zero)
	}
	if e.constantRead(m, sc, recv, sig, q) {
		return
	}
	if m.scoped {
		qs, i, x := sc.Name("qs"), sc.Name("i"), sc.Name("x")
		e.printf("%s := make([]%s[%s], len(%s))\n", qs, e.ref("Of"), model, sig.scopes)
		e.printf("for %[1]s, %[2]s := range %[3]s {\n%[4]s[%[1]s] = %[5]s[%[6]s](%[2]s.Of())\n}\n", i, x, sig.scopes, qs,
			e.ref("Of"), model)
		e.printf("%s := %s(%s...)\n", q, e.ref("Combine"), qs)
	} else {
		e.printf("var %s %s[%s]\n", q, e.ref("Of"), model)
	}
	for _, k := range m.keys {
		e.printf("%[1]s = %[1]s.Where(%[2]s, %[3]s)\n"+
			"", q, goString(sqlName(e.d, k.col.col.Name)+" = "), sig.params[k.param])
	}

	names, args := make([]string, 0, len(m.keys)), make([]string, 0, len(m.keys))
	for _, k := range m.keys {
		names, args = append(names, k.col.col.Name), append(args, sig.params[k.param])
	}
	notFound := func() string { return sig.mapped(e.notFound(m.target.table, names, args)) }
	if m.aggregate != nil {
		e.aggregateRead(m, sc, recv, sig, q, names, args)
		return
	}
	if m.pluck != nil {
		e.pluckRead(m, sc, recv, sig, q, notFound)
		return
	}
	if m.projection != nil && len(m.projection.aggregates) > 0 {
		e.groupedRead(m, sc, recv, sig, q)
		return
	}
	if m.projection != nil && m.verb != verbDelete {
		e.projectedRead(m, sc, recv, sig, q, notFound)
		return
	}
	rd, err := sc.Name("rd"), sc.Name("err")
	render := func(zero string) {
		e.printf("%[1]s, %[2]s := %[3]s.Render(%[4]s, 1, nil)\nif %[2]s != nil {\nreturn %[5]s%[6]s\n}\n", rd, err, q,
			e.dialect(), zero, sig.mapped(err))
	}
	conditionsOnly := func(zero string) {
		e.printf("if %[1]s := %[2]s.ConditionsOnly(%[3]q); %[1]s != nil {\n"+
			"return %[4]s%[5]s\n}\n", err, q, m.name, zero, sig.mapped(err))
	}

	switch m.verb {
	case verbGet, verbFind, verbFirst:
		rows := sc.Name("rows")
		zero := model + "{}, "
		if m.optional {
			zero = "nil, "
		}
		check := "Unpaged"
		if m.verb == verbFirst {
			check = "Forward"
		}
		e.printf("if %[1]s := %[2]s.%[3]s(%[4]q); %[1]s != nil {\nreturn %[5]s%[6]s\n}\n", err, q, check, m.name, zero,
			sig.mapped(err))
		if m.verb == verbFirst {
			e.printf("%[1]s = %[1]s.DefaultOrder(%[2]s).Limit(1)\n", q, e.orderKey(m.table))
		}
		e.printf("%s, %s := %s(%s, %s.db, %s, %s)\n", rows, err, e.helpers[m.target.table].selectRows, sig.ctx, recv, q,
			lockClause(m))
		if m.optional {
			e.printf("if %s != nil { return nil, %s }\n", err, sig.mapped(err))
			e.printf("if len(%s) == 0 { return nil, nil }\nreturn &%s[0], nil\n", rows, rows)
			return
		}
		e.printf("if %[1]s != nil {\nreturn %[2]s{}, %[3]s\n}\n", err, model, sig.mapped(err))
		e.printf("if len(%[1]s) == 0 {\nreturn %[2]s{}, %[3]s\n}\nreturn %[1]s[0], nil\n", rows, model, notFound())
	case verbList:
		read := fmt.Sprintf("%s(%s, %s.db, %s, %s)", e.helpers[m.target.table].selectRows, sig.ctx, recv, q,
			lockClause(m))
		if sig.recv == "" {
			e.printf("return %s\n", read)
			break
		}
		rows := sc.Name("rows")
		e.printf("%[1]s, %[2]s := %[3]s\nif %[2]s != nil {\nreturn nil, %[4]s\n}\nreturn %[1]s, nil\n", rows, err, read,
			sig.mapped(err))
	case verbCount, verbExists:
		query, typ, zero := "`SELECT count(*) FROM "+table+"`+"+rd+".Where()", "int64", "0"
		if m.verb == verbExists {
			query, typ, zero = "`SELECT EXISTS (SELECT 1 FROM "+table+"`+"+rd+".Where()+`)`", "bool", "false"
		}
		conditionsOnly(zero + ", ")
		render(zero + ", ")
		n := sc.Name("n")
		e.printf("var %s %s\n", n, typ)
		e.printf("if %[1]s := %[2]s.db.%[3]s(%[4]s, %[5]s, %[6]s.Args()...).Scan(&%[7]s); %[1]s != nil {\n",
			err, recv, e.queryRow, sig.ctx, query, rd, n)
		e.printf("return %s, %s\n}\nreturn %s, nil\n", zero, sig.mapped(e.fmtPkg()+".Errorf("+
			strconv.Quote(strings.ToLower(string(m.verb))+" "+m.target.table+": %w")+", "+err+")"), n)
	case verbDelete:
		if m.returns == returnsRows {
			e.deleteReturning(m, sc, recv, sig, q)
			return
		}
		if m.returns == returnsCount {
			e.deleteSelected(m, sc, recv, sig, q)
			return
		}
		render("")
		res := e.execResult(sc)
		wrap := sig.mapped(e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", "delete from "+m.target.table+": %w") + ", " +
			err + ")")
		e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s, `DELETE FROM %[6]s`+%[7]s.Where(), %[7]s.Args()...)\n"+
			"if %[2]s != nil {\nreturn %[8]s%[9]s\n}\n", res, err, recv, e.exec, sig.ctx, table, rd, "", wrap)
		affected := e.rowsAffected(sc, res, err, "", wrap)
		e.printf("if %s == 0 {\nreturn %s\n}\nreturn nil\n", affected, notFound())
	case verbInsert, verbUpdate, verbUpsert, verbCopy: // method writes them
	case verbSum, verbMin, verbMax, verbAvg: // aggregateRead reads them
	}
}

// inTransaction writes the check that m, which locks the rows it reads, runs in a transaction: what it runs on is one,
// or wraps one, as the runtime's InTransaction tells.
func (e repoEmitter) inTransaction(m repoMethod, recv string, sig signature, zero string) {
	path, name, tx := stdlibPath, "stdlib", "*sql.Tx"
	if e.drv == driverPgx {
		path, name, tx = pgxPath, "kpgx", "pgx.Tx"
	}
	pkg := e.f.Import(path, name)
	e.printf("if !%s.InTransaction(%s.db.dbtx) {\nreturn %s, %s\n}\n", pkg, recv, zero,
		sig.mapped(e.fmtPkg()+".Errorf("+strconv.Quote("orm: "+m.name+" needs a transaction to hold its lock: give the "+
			"repository a "+tx+", or a DBTX wrapping one with an Unwrap method returning it")+")"))
}

// orderKey is the primary key of t as Render takes it, for an order to end in.
func (e repoEmitter) orderKey(t snapshot.Table) string {
	if len(t.PrimaryKey) == 0 {
		return "nil"
	}
	names := make([]string, 0, len(t.PrimaryKey))
	for _, name := range t.PrimaryKey {
		names = append(names, goString(sqlName(e.d, name)))
	}
	return "[]string{" + strings.Join(names, ", ") + "}"
}

// lockClause is the locking clause of m, as the argument of select<table>.
func lockClause(m repoMethod) string {
	if m.lock == "" {
		return `""`
	}
	return goString(" " + m.lock)
}

// selectRows writes the function reading the whole rows of rm that a scope matches, in its order, with the relations
// the scope loads. A method that locks them gives the locking clause, and the relations are read without it.
func (e repoEmitter) selectRows(rm readModel) {
	name := e.helpers[rm.target.table].selectRows
	model := types.TypeString(rm.model, e.f.Qualifier)
	sc := e.f.Scope()
	ctx, db, q, lock := sc.Name("ctx"), sc.Name("db"), sc.Name("q"), sc.Name("lock")
	rd, err, out := sc.Name("rd"), sc.Name("err"), sc.Name("out")

	query := "SELECT " + e.columnList(rm.columns) + " FROM " + sqlName(e.d, rm.target.table)
	e.printf("// %s reads the rows of %s that %s matches, in its order", name, rm.target.table, q)
	if len(rm.links) > 0 {
		e.printf(", with the relations it loads")
	}
	e.printf(". %s is the locking clause of the query, if any.\n", lock)
	e.printf("func %[1]s(%[2]s %[3]s.Context, %[4]s %[9]s, %[5]s %[6]s[%[7]s], %[8]s string) ([]%[7]s, error) {\n",
		name,
		ctx, e.f.Import("context", "context"), db, q, e.ref("Of"), model, lock, e.database)
	e.printf("%[1]s, %[2]s := %[3]s.Render(%[4]s, 1, %[5]s)\n"+
		"if %[2]s != nil {\nreturn nil, %[2]s\n}\n", rd, err, q, e.dialect(), e.orderKey(rm.table))
	e.printf("%[1]s, %[2]s := %[3]s(%[4]s, %[5]s, %[6]s+%[7]s.Where()+%[7]s.OrderBy()+%[7]s.Limit()+%[8]s, "+
		"%[7]s.Args()...)\nif %[2]s != nil {\nreturn nil, %[2]s\n}\n", out, err, e.helpers[rm.target.table].queryRows,
		ctx, db, goString(query), rd, lock)
	e.printf("if %s.Reversed() {\n%s.Reverse(%s)\n}\n", rd, e.f.Import("slices", "slices"), out)
	if len(rm.links) > 0 {
		e.printf("if %[1]s := %[2]s(%[3]s, %[4]s, %[5]s, %[6]s); %[1]s != nil {\nreturn nil, %[1]s\n}\n", err,
			e.helpers[rm.target.table].preload, ctx, db, out, q)
	}
	e.printf("return %s, nil\n}\n\n", out)
}

// queryRows writes the function running a query of the whole rows of rm, and reading them.
func (e repoEmitter) queryRows(rm readModel) {
	name := e.helpers[rm.target.table].queryRows
	model := types.TypeString(rm.model, e.f.Qualifier)
	sc := e.f.Scope()
	ctx, db, query, args := sc.Name("ctx"), sc.Name("db"), sc.Name("query"), sc.Name("args")
	err, rows, out, row := sc.Name("err"), sc.Name("rows"), sc.Name("out"), sc.Name("row")
	wrap := e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", "read "+rm.target.table+": %w") + ", " + err + ")"

	e.printf("// %s runs %s, a query of the rows of %s, and reads them.\n", name, query, rm.target.table)
	e.printf("func %[1]s(%[2]s %[3]s.Context, %[4]s %[5]s, %[6]s string, %[7]s ...any) ([]%[8]s, error) {\n",
		name, ctx, e.f.Import("context", "context"), db, e.database, query, args, model)
	e.printf("%[1]s, %[2]s := %[3]s.%[4]s(%[5]s, %[6]s, %[7]s...)\n", rows, err, db, e.query, ctx, query, args)
	e.printf("if %s != nil {\nreturn nil, %s\n}\ndefer %s.Close()\n", err, wrap, rows)
	e.scanRows(scanNames{rows: rows, out: out, row: row, err: err, wrap: wrap}, model, rm.columns, sc)
	e.printf("return %s, nil\n}\n\n", out)
}

// columnList is the columns, as a list in SQL.
func (e repoEmitter) columnList(columns []column) string {
	names := make([]string, 0, len(columns))
	for _, c := range columns {
		names = append(names, sqlName(e.d, c.col.Name))
	}
	return strings.Join(names, ", ")
}

// scanNames are the names in a loop reading rows: rows into out, each into row, and on an error err, wrapped by wrap,
// returned with zero, nil unless it is said.
type scanNames struct {
	rows, out, row, err, wrap, zero string
}

// scanRows writes the loop reading the rows of model, each column into the field holding it. The row and what Scan
// writes into are made once, for every row to be read into: a row is set to the zero value before it is read, and
// copied into out after.
func (e repoEmitter) scanRows(n scanNames, model string, columns []column, sc gofile.Scope) {
	zero := cmp.Or(n.zero, "nil")
	dest := sc.Name("dest")
	e.printf("%[1]s := []%[2]s{}\nvar %[3]s %[2]s\n", n.out, model, n.row)
	dests, assignments := e.scanTargets(sc, n.row, columns)
	e.printf("%s := []any{%s}\nfor %s.Next() {\n%s = %s{}\n", dest, strings.Join(dests, ", "), n.rows, n.row, model)
	e.printf("if %[1]s := %[2]s.Scan(%[3]s...); %[1]s != nil {\nreturn %[5]s, %[4]s\n}\n", n.err, n.rows, dest,
		n.wrap, zero)
	e.assignScanned(assignments)
	e.printf("%[1]s = append(%[1]s, %[2]s)\n}\n", n.out, n.row)
	e.printf("if %[1]s := %[2]s.Err(); %[1]s != nil {\nreturn %[4]s, %[3]s\n}\n", n.err, n.rows, n.wrap, zero)
}

// insert writes the body of m, an Insert or an Upsert: its rows written in one statement, and read back when m
// returns them.
func (e repoEmitter) insert(m repoMethod, sc gofile.Scope, recv string, sig signature) {
	if e.d == snapshot.MySQL {
		e.insertMySQL(m, sc, recv, sig)
		return
	}
	model := types.TypeString(m.model, e.f.Qualifier)
	zero := ""
	if m.returns == returnsRow {
		zero = model + "{}, "
	}
	set, err := e.changeValues(m, sc, sig, zero)
	inserted := m.inserted()
	tail := e.insertTail(m, len(inserted)+1)
	given, gives := m.given()
	if gives && !m.many {
		e.insertGiven(m, sc, recv, sig, given, set, err)
		return
	}
	if gives {
		inserted = slices.Concat(inserted, []column{given})
	}
	var (
		query strings.Builder
		args  []string
	)
	fmt.Fprintf(&query, "INSERT INTO %s", sqlName(e.d, m.target.table))
	if len(inserted) > 0 {
		fmt.Fprintf(&query, " (%s)\n", e.columnList(inserted))
	}
	switch {
	case len(inserted) == 0:
		query.WriteString(" DEFAULT VALUES")
	case m.many:
		none := "nil"
		if m.returns == returnsRows {
			none = "[]" + model + "{}, nil"
		}
		e.printf("if len(%s) == 0 {\nreturn %s\n}\n", sig.row, none)
		e.readClock(recv, sig)
		casts := make([]string, 0, len(inserted))
		for _, c := range inserted {
			// The type comes first, so that its package keeps its name and the local named after the field yields.
			typ := e.storageType(c)
			v := sc.Name(lowerFirst(c.path[len(c.path)-1]))
			e.printf("%s := make([]%s, len(%s))\n", v, typ, sig.row)
			args = append(args, v)
			casts = append(casts, fmt.Sprintf("$%d::%s[]", len(casts)+1, castType(c.col.Type)))
		}
		i := sc.Name("i")
		e.printf("for %s := range %s {\n", i, sig.row)
		for j, c := range inserted {
			e.printf("%s[%s] = %s\n", args[j], i, e.written(c, sig.row+"["+i+"]", sig.now))
		}
		e.printf("}\n")
		query.WriteString(e.unnestRows(inserted, casts, given, gives))
	default:
		params := make([]string, 0, len(inserted))
		for _, c := range inserted {
			args = append(args, e.written(c, sig.row, sig.now))
			params = append(params, fmt.Sprintf("$%d", len(params)+1))
		}
		fmt.Fprintf(&query, "VALUES (%s)", strings.Join(params, ", "))
	}

	query.WriteString(tail)

	q := sc.Name("q")
	wrap := sig.mapped(e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", m.action()+": %w") + ", " + err + ")")
	if set != "" {
		args = []string{"append([]any{" + strings.Join(args, ", ") + "}, " + set + "...)..."}
	}
	call := strings.Join(append([]string{sig.ctx, q}, args...), ", ")
	e.printf("const %s = %s\n", q, goString(query.String()))
	switch m.returns {
	case returnsRow:
		row := sc.Name("row")
		e.printf("var %s %s\n", row, model)
		dests, assignments := e.scanTargets(sc, row, m.columns)
		e.printf("%[1]s %[8]s %[2]s.db.%[3]s(%[4]s).\nScan(%[5]s)\nif %[1]s != nil {\nreturn %[6]s{}, %[7]s\n}\n",
			err, recv, e.queryRow, call, strings.Join(dests, ", "), model, wrap, declare(set))
		e.assignScanned(assignments)
		e.printf("return %s, nil\n", row)
	case returnsRows:
		rows, out, row := sc.Name("rows"), sc.Name("out"), sc.Name("row")
		e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s)\nif %[2]s != nil {\nreturn nil, %[6]s\n}\n"+
			"defer %[1]s.Close()\n", rows, err, recv, e.query, call, wrap)
		e.scanRows(scanNames{rows: rows, out: out, row: row, err: err, wrap: wrap}, model, m.columns, sc)
		e.printf("return %s, nil\n", out)
	case returnsNone:
		e.printf("if _, %[1]s := %[2]s.db.%[3]s(%[4]s); %[1]s != nil {\nreturn %[5]s\n}\nreturn nil\n", err, recv,
			e.exec, call, wrap)
	case returnsCount, returnsBool, returnsScalar: // shaped turns them down for a write
	}
}

// action is what m, an Insert or an Upsert, does, for its error.
func (m repoMethod) action() string {
	return strings.ToLower(string(m.verb)) + " into " + m.target.table
}

// insertTail is what follows the rows of an insert of m on PostgreSQL: the update of an upsert's row met, and the
// columns read back. The values of a change set are at placeholders from first on.
func (e repoEmitter) insertTail(m repoMethod, first int) string {
	var tail strings.Builder
	if m.verb == verbUpsert {
		key := make([]string, 0, len(m.rowKey()))
		for _, name := range m.rowKey() {
			key = append(key, sqlName(e.d, name))
		}
		// With nothing to update, the key is set to itself, so that the row met is still returned.
		sets := []string{key[0] + " = EXCLUDED." + key[0]}
		if updated := m.updated(); len(updated) > 0 {
			sets = sets[:0]
			for _, c := range updated {
				name := sqlName(e.d, c.col.Name)
				sets = append(sets, e.upsertAssignment(m, c, "EXCLUDED."+name, sqlName(e.d, m.target.table)+"."+name,
					first))
				first += upsertParams(m, c)
			}
		}
		fmt.Fprintf(&tail, "\nON CONFLICT (%s) DO UPDATE SET %s", strings.Join(key, ", "), strings.Join(sets, ", "))
	}
	if m.returns == returnsRow || m.returns == returnsRows {
		fmt.Fprintf(&tail, "\nRETURNING %s", e.columnList(m.columns))
	}
	return tail.String()
}

// update writes the body of m, an Update: its row written over the one with its key, and read back by RETURNING when
// m returns it, or orm.ErrNotFound when none has it.
func (e repoEmitter) update(m repoMethod, sc gofile.Scope, recv string, sig signature) {
	table := sqlName(e.d, m.target.table)
	updated, key := m.updated(), m.rowKey()
	sets, conds := make([]string, 0, len(updated)), make([]string, 0, len(key))
	args, keyArgs := make([]string, 0, len(updated)+len(key)), make([]string, 0, len(key))
	for _, c := range updated {
		args = append(args, e.written(c, sig.row, sig.now))
		sets = append(sets, sqlName(e.d, c.col.Name)+" = "+e.placeholder(len(args)))
	}
	for _, name := range key {
		args = append(args, sig.row+"."+fieldPath(m.field(name)))
		keyArgs = append(keyArgs, sig.row+"."+fieldPath(m.field(name)))
		conds = append(conds, sqlName(e.d, name)+" = "+e.placeholder(len(args)))
	}
	where := strings.Join(conds, " AND ")
	query := "UPDATE " + table + "\nSET " + strings.Join(sets, ", ") + "\nWHERE " + where
	notFound := sig.mapped(e.notFound(m.target.table, key, keyArgs))

	if m.returns == returnsRow { // on PostgreSQL alone, as MySQL has no UPDATE RETURNING
		model := types.TypeString(m.model, e.f.Qualifier)
		q, row, err := sc.Name("q"), sc.Name("row"), sc.Name("err")
		wrap := sig.mapped(e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", "update "+m.target.table+": %w") + ", " + err +
			")")
		e.printf("const %s = %s\n", q, goString(query+"\nRETURNING "+e.columnList(m.columns)))
		e.printf("var %s %s\n", row, model)
		dests, assignments := e.scanTargets(sc, row, m.columns)
		e.printf("%s := %s.db.%s(%s).\nScan(%s)\n", err, recv, e.queryRow,
			strings.Join(append([]string{sig.ctx, q}, args...), ", "), strings.Join(dests, ", "))
		e.printf("if %s.Is(%s, %s.ErrNoRows) {\nreturn %s{}, %s\n}\n", e.f.Import("errors", "errors"), err,
			e.driverPkg(), model, notFound)
		e.printf("if %s != nil {\nreturn %s{}, %s\n}\n", err, model, wrap)
		e.assignScanned(assignments)
		e.printf("return %s, nil\n", row)
		return
	}

	q, res, err := sc.Name("q"), e.execResult(sc), sc.Name("err")
	wrap := sig.mapped(e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", "update "+m.target.table+": %w") + ", " + err + ")")
	e.printf("const %s = %s\n", q, goString(query))
	e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s)\nif %[2]s != nil {\nreturn %[6]s\n}\n", res, err, recv, e.exec,
		strings.Join(append([]string{sig.ctx, q}, args...), ", "), wrap)
	affected := e.rowsAffected(sc, res, err, "", wrap)
	if e.d != snapshot.MySQL {
		e.printf("if %s == 0 {\nreturn %s\n}\nreturn nil\n", affected, notFound)
		return
	}
	found := sc.Name("found")
	e.printf("if %s == 0 {\n"+
		"// MySQL counts the rows an update changes, not those it finds: see whether the row is there.\n"+
		"var %s bool\n", affected, found)
	e.printf("if %[1]s := %[2]s.db.%[3]s(%[4]s, %[5]s, %[6]s).Scan(&%[7]s); %[1]s !="+
		" nil {\nreturn %[8]s\n}\n", err, recv,
		e.queryRow, sig.ctx, goString("SELECT EXISTS (SELECT 1 FROM "+table+" WHERE "+where+")"),
		strings.Join(keyArgs, ", "), found, wrap)
	e.printf("if !%s {\nreturn %s\n}\n}\nreturn nil\n", found, notFound)
}

// insertMySQL writes the body of m, an Insert or an Upsert, on MySQL: its rows written in VALUES, as many to a
// statement as its placeholders allow, and its row, when m returns it, read back by LAST_INSERT_ID() or by its key.
func (e repoEmitter) insertMySQL(m repoMethod, sc gofile.Scope, recv string, sig signature) {
	model := types.TypeString(m.model, e.f.Qualifier)
	zero := ""
	if m.returns == returnsRow {
		zero = model + "{}, "
	}
	set, err := e.changeValues(m, sc, sig, zero)
	table := sqlName(e.d, m.target.table)
	inserted := m.inserted()
	auto, hasAuto := m.autoIncrement()
	values := "()"
	if len(inserted) > 0 {
		values = "(" + strings.Repeat("?, ", len(inserted)-1) + "?)"
	}
	head := "INSERT INTO " + table + " (" + e.columnList(inserted) + ")\nVALUES " + values
	tail := ""
	if m.verb == verbUpsert {
		var sets []string
		if hasAuto && m.returns == returnsRow { // for LAST_INSERT_ID() to tell the row met
			name := sqlName(e.d, auto)
			sets = append(sets, name+" = LAST_INSERT_ID("+table+"."+name+")")
		}
		for _, c := range m.updated() {
			name := sqlName(e.d, c.col.Name)
			sets = append(sets, e.upsertAssignment(m, c, "new."+name, table+"."+name, 0))
		}
		if len(sets) == 0 { // the key set to itself, as an update of nothing
			name := sqlName(e.d, m.rowKey()[0])
			sets = append(sets, name+" = new."+name)
		}
		tail = " AS new\nON DUPLICATE KEY UPDATE " + strings.Join(sets, ", ")
	}
	if given, ok := m.given(); ok {
		e.insertGivenMySQL(m, sc, recv, sig, given, tail, set, err)
		return
	}

	wrap := sig.mapped(e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", m.action()+": %w") + ", " + err + ")")
	if m.many {
		chunk, args, i := sc.Name("chunk"), sc.Name("args"), sc.Name("i")
		fields := make([]string, 0, len(inserted))
		for _, c := range inserted {
			fields = append(fields, e.written(c, chunk+"["+i+"]", sig.now))
		}
		capacity := "len(" + chunk + ")"
		if len(inserted) > 1 {
			capacity += "*" + strconv.Itoa(len(inserted))
		}
		query := goString(head) + "+" + e.f.Import("strings", "strings") + ".Repeat(" + goString(", "+values) +
			", len(" + chunk + ")-1)"
		if tail != "" {
			query += "+" + goString(tail)
		}
		e.printf("if len(%s) == 0 {\nreturn nil\n}\n", sig.row)
		e.readClock(recv, sig)
		e.printf("// As many rows to a statement as its 65,535 placeholders take.\n")
		e.printf("for %[1]s := range %[2]s.Chunk(%[3]s, %[4]d) {\n%[5]s := make([]any, 0, %[6]s)\n"+
			"for %[7]s := range %[1]s {\n%[5]s = append(%[5]s, %[8]s)\n}\n", chunk, e.f.Import("slices", "slices"), sig.row,
			65535/len(inserted), args, capacity, i, strings.Join(fields, ", "))
		e.printf("if _, %[1]s := %[2]s.db.%[3]s(%[4]s, %[5]s, %[6]s...); %[1]s != nil {\nreturn %[7]s\n}\n}\n"+
			"return nil\n", err, recv, e.exec, sig.ctx, query, args, wrap)
		return
	}

	q := sc.Name("q")
	rowArgs := make([]string, 0, len(inserted))
	for _, c := range inserted {
		rowArgs = append(rowArgs, e.written(c, sig.row, sig.now))
	}
	if set != "" {
		rowArgs = []string{"append([]any{" + strings.Join(rowArgs, ", ") + "}, " + set + "...)..."}
	}
	call := strings.Join(append([]string{sig.ctx, q}, rowArgs...), ", ")
	e.printf("const %s = %s\n", q, goString(head+tail))
	if m.returns != returnsRow {
		e.printf("if _, %[1]s := %[2]s.db.%[3]s(%[4]s); %[1]s != nil {\nreturn %[5]s\n}\nreturn nil\n", err, recv,
			e.exec, call, wrap)
		return
	}

	// The row read back by the value LAST_INSERT_ID() tells, or else by its key.
	key := m.table.PrimaryKey
	if m.verb == verbUpsert && !hasAuto {
		key = m.rowKey()
	}
	conds := make([]string, 0, len(key))
	keyArgs := make([]string, 0, len(key))
	id := sc.Name("id")
	assign := declare(set)
	if hasAuto {
		res := e.execResult(sc)
		e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s)\nif %[2]s != nil {\nreturn %[6]s%[7]s\n}\n", res, err, recv,
			e.exec, call, zero, wrap)
		e.printf("%[1]s, %[2]s := %[3]s.LastInsertId()\nif %[2]s != nil {\nreturn %[4]s%[5]s\n}\n", id, err, res, zero,
			wrap)
		key, assign = []string{auto}, "="
	} else {
		e.printf("if _, %[1]s := %[2]s.db.%[3]s(%[4]s); %[1]s != nil {\n"+
			"return %[5]s%[6]s\n}\n", err, recv, e.exec, call,
			zero, wrap)
	}
	for _, name := range key {
		conds = append(conds, sqlName(e.d, name)+" = ?")
		if name == auto {
			keyArgs = append(keyArgs, id)
		} else {
			keyArgs = append(keyArgs, sig.row+"."+fieldPath(m.field(name)))
		}
	}
	row := sc.Name("row")
	query := "SELECT " + e.columnList(m.columns) + " FROM " + table + " WHERE " + strings.Join(conds, " AND ")
	e.printf("var %s %s\n", row, model)
	dests, assignments := e.scanTargets(sc, row, m.columns)
	e.printf("%[1]s %[2]s %[3]s.db.%[4]s(%[5]s, %[6]s, %[7]s).\nScan(%[8]s)\n"+
		"if %[1]s != nil {\nreturn %[9]s%[10]s\n}\n",
		err, assign, recv, e.queryRow, sig.ctx, goString(query), strings.Join(keyArgs, ", "),
		strings.Join(dests, ", "), zero, wrap)
	e.assignScanned(assignments)
	e.printf("return %s, nil\n", row)
}

// placeholder is the placeholder of the nth parameter of a statement.
func (e repoEmitter) placeholder(n int) string {
	if e.d == snapshot.MySQL {
		return "?"
	}
	return "$" + strconv.Itoa(n)
}

// handWritten writes the body of m, which a query written by hand implements: the rows of its result read into its
// fields, in their order, or the number of rows it affects.
func (e repoEmitter) handWritten(m repoMethod, sc gofile.Scope, recv string, sig signature) {
	if m.scoped {
		e.scopedQuery(m, sc, recv, sig)
		return
	}
	q, err := sc.Name("q"), sc.Name("err")
	args := make([]string, 0, 2+len(m.query.args))
	args = append(args, sig.ctx, q)
	for _, a := range m.query.args {
		value := sig.params[a.parameter]
		if a.field != nil {
			value = e.storageValue(*a.field, value+"."+fieldPath(*a.field))
		}
		args = append(args, value)
	}
	what := fmt.Sprintf("%q", "query "+m.name+": %w")
	wrap := sig.mapped(e.fmtPkg() + ".Errorf(" + what + ", " + err + ")")
	if m.query.pieces == nil {
		e.printf("const %s = %s\n", q, goString(m.query.sql))
	} else {
		values := args[2:]
		for i, a := range m.query.args {
			if expands(a.typ) {
				values[i] = e.ref(listOf(m.query.pieces[i], m.query.pieces[i+1])) + "(" + values[i] + ")"
			}
		}
		pieces := make([]string, 0, len(m.query.pieces))
		for _, p := range m.query.pieces {
			pieces = append(pieces, goString(p))
		}
		expanded := sc.Name("args")
		e.printf("%s, %s := %s([]string{%s}, %s)\n", q, expanded, e.ref("Expand"), strings.Join(pieces, ", "),
			strings.Join(values, ", "))
		args = []string{sig.ctx, q, expanded + "..."}
	}
	call := strings.Join(args, ", ")
	switch m.returns {
	case returnsNone:
		e.printf("if _, %[1]s := %[2]s.db.%[3]s(%[4]s); %[1]s != nil {\nreturn %[5]s\n}\nreturn nil\n", err, recv,
			e.exec, call, wrap)
	case returnsCount:
		res := e.execResult(sc)
		e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s)\nif %[2]s != nil {\nreturn 0, %[6]s\n}\n", res, err, recv,
			e.exec, call, wrap)
		e.printf("return %s, nil\n", e.rowsAffected(sc, res, err, "0, ", wrap))
	case returnsRow, returnsScalar:
		row := sc.Name("row")
		typ := types.TypeString(m.query.row, e.f.Qualifier)
		e.printf("var %s %s\n", row, typ)
		dests, assignments := []string{"&" + row}, []string(nil)
		if !m.query.scalar {
			dests, assignments = e.scanTargets(sc, row, m.query.fields)
		}
		e.printf("%[1]s := %[2]s.db.%[3]s(%[4]s).\nScan(%[5]s)\n", err, recv, e.queryRow, call,
			strings.Join(dests, ", "))
		zeroDeclaration, zeroValue := "", typ+"{}"
		if m.query.scalar {
			zeroValue = sc.Name("zero")
			zeroDeclaration = "var " + zeroValue + " " + typ + "\n"
		}
		if m.optional {
			e.printf("if %s.Is(%s, %s.ErrNoRows) { return nil, nil }\n",
				e.f.Import("errors", "errors"), err, e.driverPkg())
			e.printf("if %s != nil { return nil, %s }\n", err, wrap)
			e.assignScanned(assignments)
			e.printf("return &%s, nil\n", row)
			return
		}
		e.printf("if %[1]s.Is(%[2]s, %[3]s.ErrNoRows) {\n%[4]s"+
			"return %[5]s, %[6]s\n}\n",
			e.f.Import("errors", "errors"), err, e.driverPkg(), zeroDeclaration, zeroValue,
			sig.mapped(e.fmtPkg()+".Errorf("+what+", "+e.ref("ErrNotFound")+")"))
		e.printf("if %[1]s != nil {\n%[2]sreturn %[3]s, %[4]s\n}\n", err, zeroDeclaration, zeroValue, wrap)
		e.assignScanned(assignments)
		e.printf("return %s, nil\n", row)
	case returnsRows:
		rows, out, row := sc.Name("rows"), sc.Name("out"), sc.Name("row")
		typ := types.TypeString(m.query.row, e.f.Qualifier)
		e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s)\nif %[2]s != nil {\nreturn nil, %[6]s\n}\n"+
			"defer %[1]s.Close()\n", rows, err, recv, e.query, call, wrap)
		n := scanNames{rows: rows, out: out, row: row, err: err, wrap: wrap}
		if m.query.scalar {
			e.scalarRows(n, typ, sc)
		} else {
			e.scanRows(n, typ, m.query.fields, sc)
		}
		e.printf("return %s, nil\n", out)
	case returnsBool: // a query written by hand returns no bool
	}
}

// execResult names, in a function, the result of a statement Exec runs.
func (e repoEmitter) execResult(sc gofile.Scope) string {
	if e.drv == driverSQL {
		return sc.Name("res")
	}
	return sc.Name("tag")
}

// rowsAffected is the number of rows the statement whose result is res affected. On database/sql, it first writes
// what reads the number, returning zero and wrap, of err, when that fails.
func (e repoEmitter) rowsAffected(sc gofile.Scope, res, err, zero, wrap string) string {
	if e.drv == driverPgx {
		return res + ".RowsAffected()"
	}
	n := sc.Name("n")
	e.printf("%[1]s, %[2]s := %[3]s.RowsAffected()\nif %[2]s != nil {\nreturn %[4]s%[5]s\n}\n", n, err, res, zero, wrap)
	return n
}

// driverPkg is the name the file imports the package of the driver by.
func (e repoEmitter) driverPkg() string {
	if e.drv == driverSQL {
		return e.f.Import("database/sql", "sql")
	}
	return e.f.Import("github.com/jackc/pgx/v5", "pgx")
}

// inChunk is how many keys MySQL is given in one statement, for it to take far fewer than the 65,535 placeholders it
// can.
const inChunk = 1000

// preloadNames are the names in a function loading relations.
type preloadNames struct {
	// table is the table of the rows the function loads relations into.
	table, ctx, db, rows, q, sub, ok string
}

// preloadRows writes the function loading into rows of rm the relations a scope loads, each by the keys of the rows.
func (e repoEmitter) preloadRows(rm readModel) {
	name := e.helpers[rm.target.table].preload
	model := types.TypeString(rm.model, e.f.Qualifier)
	sc := e.f.Scope()
	n := preloadNames{
		table: rm.target.table, ctx: sc.Name("ctx"), db: sc.Name("db"), rows: sc.Name("rows"), q: sc.Name("q"),
		sub: sc.Name("sub"), ok: sc.Name("ok"),
	}
	for _, lk := range rm.links {
		e.each(lk, e.eachNames[eachKey(n.table, lk)])
	}
	e.printf("// %s loads into %s the relations %s loads, each by the keys of the rows.\n", name, n.rows, n.q)
	e.printf("func %[1]s(%[2]s %[3]s.Context, %[4]s %[9]s, %[5]s []%[6]s, %[7]s %[8]s[%[6]s]) error {\n",
		name, n.ctx,
		e.f.Import("context", "context"), n.db, n.rows, model, n.q, e.ref("Of"), e.database)
	e.printf("if len(%s) == 0 {\nreturn nil\n}\n", n.rows)
	for _, lk := range rm.links {
		e.preload(lk, n)
	}
	e.printf("return nil\n}\n\n")
}

// fieldPath is how a row reaches the field holding c.
func fieldPath(c column) string {
	return strings.Join(c.path, ".")
}

func isPointer(t types.Type) bool {
	_, ok := t.Underlying().(*types.Pointer)
	return ok
}

// pluckRead writes the body of m, which reads the values of one column of the rows its keys and its scopes match, as
// a projection reads its columns: with no relation loaded.
func (e repoEmitter) pluckRead(m repoMethod, sc gofile.Scope, recv string, sig signature, q string,
	notFound func() string,
) {
	column := m.pluck
	typ := types.TypeString(column.typ, e.f.Qualifier)
	rd, err, rows, out, row := sc.Name("rd"), sc.Name("err"), sc.Name("rows"), sc.Name("out"), sc.Name("row")
	zero := "nil"
	if !m.optional && m.verb != verbList {
		zero = sc.Name("zero")
		e.printf("var %s %s\n", zero, typ)
	}
	if m.lock != "" {
		e.inTransaction(m, recv, sig, zero)
	}
	e.printf("if %[1]s := %[2]s.ProjectionOnly(%[3]q); %[1]s != nil {\nreturn %[4]s, %[5]s\n}\n", err, q, m.name, zero,
		sig.mapped(err))
	switch m.verb { //nolint:exhaustive // a value is read by these alone
	case verbFirst:
		e.printf("if %[1]s := %[2]s.Forward(%[3]q); %[1]s != nil {\nreturn %[4]s, %[5]s\n}\n", err, q, m.name, zero,
			sig.mapped(err))
		e.printf("%[1]s = %[1]s.DefaultOrder(%[2]s).Limit(1)\n", q, e.orderKey(m.table))
	case verbGet, verbFind:
		e.printf("if %[1]s := %[2]s.Unpaged(%[3]q); %[1]s != nil {\nreturn %[4]s, %[5]s\n}\n", err, q, m.name, zero,
			sig.mapped(err))
	}
	query := "SELECT " + sqlName(e.d, column.col.Name) + " FROM " + sqlName(e.d, m.target.table)
	wrap := sig.mapped(e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", "read "+m.target.table+"."+column.col.Name+": %w") +
		", " + err + ")")
	e.printf("%[1]s, %[2]s := %[3]s.Render(%[4]s, 1, %[5]s)\nif %[2]s != nil {\nreturn %[6]s, %[7]s\n}\n", rd, err, q,
		e.dialect(), e.orderKey(m.table), zero, sig.mapped(err))
	text := goString(query) + "+" + rd + ".Where()+" + rd + ".OrderBy()+" + rd + ".Limit()"
	if m.lock != "" {
		text += "+" + lockClause(m)
	}
	e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s, %[6]s, %[7]s.Args()...)\n", rows, err, recv, e.query, sig.ctx, text,
		rd)
	e.printf("if %s != nil {\nreturn %s, %s\n}\ndefer %s.Close()\n", err, zero, wrap, rows)
	none, dest := sc.Name("none"), sc.Name("dest")
	e.printf("%[1]s := []%[2]s{}\nvar %[3]s, %[4]s %[5]s\n%[6]s := []any{&%[3]s}\nfor %[7]s.Next() {\n%[3]s = %[4]s\n",
		out, typ, row, none, e.storageType(*column), dest, rows)
	e.printf("if %[1]s := %[2]s.Scan(%[3]s...); %[1]s != nil {\nreturn %[4]s, %[5]s\n}\n", err, rows, dest, zero,
		wrap)
	value := row
	if column.nullzero {
		value = row + ".V"
	}
	e.printf("%[1]s = append(%[1]s, %[2]s)\n}\n", out, value)
	e.printf("if %[1]s := %[2]s.Err(); %[1]s != nil {\nreturn %[3]s, %[4]s\n}\n", err, rows, zero, wrap)
	e.printf("if %s.Reversed() {\n%s.Reverse(%s)\n}\n", rd, e.f.Import("slices", "slices"), out)
	switch {
	case m.verb == verbList:
		e.printf("return %s, nil\n", out)
	case m.optional:
		e.printf("if len(%[1]s) == 0 {\nreturn nil, nil\n}\nreturn &%[1]s[0], nil\n", out)
	default:
		e.printf("if len(%[1]s) == 0 {\nreturn %[2]s, %[3]s\n}\nreturn %[1]s[0], nil\n", out, zero, notFound())
	}
}

func (e repoEmitter) parameterType(m repoMethod, p *types.Var) string {
	typ := types.TypeString(p.Type(), e.f.Qualifier)
	if p.Name() == m.changes && m.changes != "" && packages.Invalid(p.Type()) {
		typ, _ = changeSetNames(m.target.name)
		if pkg := e.f.Import(m.target.pkgPath, m.target.pkgName); pkg != "" {
			typ = pkg + "." + typ
		}
	}
	return typ
}

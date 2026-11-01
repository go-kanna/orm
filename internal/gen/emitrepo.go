package gen

import (
	"fmt"
	"go/types"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/go-kanna/internal/gofile"

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
	// models are the models whose whole rows the repositories read, by table, and names the names of the functions
	// reading them, select<name>, and loading their relations, preload<name>.
	models map[string]readModel
	names  map[string]string
}

// emitRepositories writes orm_gen.go into the destination, the package pkgName at pkgPath: the implementation of each
// of repos on a database of d through drv, reading the whole rows of models. declared names what the destination
// declares besides.
func emitRepositories(pkgPath, pkgName string, d snapshot.Dialect, drv driver, repos []repository,
	models map[string]readModel, declared []string,
) ([]byte, []string, error) {
	f := gofile.New(generator, pkgPath, pkgName)
	f.Reserve(declared...)
	f.Reserve("DBTX")
	for _, repo := range repos {
		f.Reserve(repo.implementation(), repo.constructor())
		if repo.publicConstructor {
			f.Reserve("New" + repo.name)
		}
	}
	e := repoEmitter{f: f, d: d, drv: drv, exec: "Exec", query: "Query", queryRow: "QueryRow", models: models,
		names: readNames(models)}
	if drv == driverSQL {
		e.exec, e.query, e.queryRow = "ExecContext", "QueryContext", "QueryRowContext"
	}
	for _, name := range e.names {
		f.Reserve("select"+name, "preload"+name)
	}

	e.db()
	for _, repo := range repos {
		e.repository(repo)
	}
	for _, table := range slices.Sorted(maps.Keys(models)) {
		e.selectRows(models[table])
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

func (e repoEmitter) ormPkg() string {
	return e.f.Import(ormPath, "orm")
}

func (e repoEmitter) fmtPkg() string {
	return e.f.Import("fmt", "fmt")
}

func (e repoEmitter) printf(format string, args ...any) {
	fmt.Fprintf(e.f, format, args...)
}

// db writes DBTX, what the repositories run on, named as sqlc names it.
func (e repoEmitter) db() {
	ctx := e.f.Import("context", "context")
	if e.drv == driverSQL {
		sql := e.f.Import("database/sql", "sql")
		e.printf("// DBTX is what the repositories run on: *sql.DB, *sql.Conn, or *sql.Tx.\ntype DBTX interface {\n")
		e.printf("ExecContext(ctx %[1]s.Context, query string, args ...any) (%[2]s.Result, error)\n", ctx, sql)
		e.printf("QueryContext(ctx %[1]s.Context, query string, args ...any) (*%[2]s.Rows, error)\n", ctx, sql)
		e.printf("QueryRowContext(ctx %[1]s.Context, query string, args ...any) *%[2]s.Row\n}\n\n", ctx, sql)
		return
	}
	pgconn := e.f.Import("github.com/jackc/pgx/v5/pgconn", "pgconn")
	pgx := e.f.Import("github.com/jackc/pgx/v5", "pgx")
	e.printf("// DBTX is what the repositories run on: *pgxpool.Pool, *pgx.Conn, or pgx.Tx.\ntype DBTX interface {\n")
	e.printf("Exec(ctx %[1]s.Context, sql string, args ...any) (%[2]s.CommandTag, error)\n", ctx, pgconn)
	e.printf("Query(ctx %[1]s.Context, sql string, args ...any) (%[2]s.Rows, error)\n", ctx, pgx)
	e.printf("QueryRow(ctx %[1]s.Context, sql string, args ...any) %[2]s.Row\n}\n\n", ctx, pgx)
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
	e.printf("// %[1]s implements %[2]s on %[3]s through %[4]s.\ntype %[1]s struct{ db DBTX }\n\n", name, iface,
		database, e.drv)
	if repo.local {
		e.printf("func %s(db DBTX) *%s { return &%s{db: db} }\n\n", constructor, name, name)
		if repo.publicConstructor {
			input := "DBTX"
			if repo.constructorDB != nil {
				input = types.TypeString(repo.constructorDB, e.f.Qualifier)
			}
			e.printf("// New%[1]s returns %[1]s running on db.\nfunc New%[1]s(db %[2]s) %[1]s { return %[3]s(db) }\n\n",
				repo.name, input, constructor)
		}
		e.printf("var _ %s = (*%s)(nil)\n\n", iface, name)
	} else {
		e.printf("// New%[1]s returns %[2]s %[1]s running on db.\nfunc New%[1]s(db DBTX) %[1]s { return %[1]s{db: db} }\n\n",
			repo.name, article(repo.name))
		e.printf("var _ %s = %s{}\n\n", iface, repo.name)
	}
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

func (r repository) constructor() string {
	if r.local {
		return "new" + r.name
	}
	return "New" + r.name
}

// signature is the signature of a method as written, with the names of its parameters in the body.
type signature struct {
	text string
	// ctx, scopes, and row name the context, the scopes, and the row in the body; params, the others, by the names the
	// interface gives them.
	ctx, scopes, row string
	params           map[string]string
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
			case i == 0 && m.bind != nil:
				name = "db"
			case i == 0:
				name = "ctx"
			case variadic:
				name = "s"
			default:
				name = "row"
			}
		}
		local := sc.Name(name)
		if m.bind != nil && m.bind.localDBTX {
			s.ctx = local
			params = append(params, local+" DBTX")
			continue
		}
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
		params = append(params, local+" "+types.TypeString(p.Type(), e.f.Qualifier))
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

// dialect is the orm.Dialect of the database, as the file names it.
func (e repoEmitter) dialect() string {
	if e.d == snapshot.MySQL {
		return e.ormPkg() + ".MySQL"
	}
	return e.ormPkg() + ".Postgres"
}

// method writes the method m of repo.
func (e repoEmitter) method(repo repository, m repoMethod) {
	sc := e.f.Scope()
	recv := sc.Name("r")
	sig := e.signature(m, sc)
	if m.query != nil {
		e.printf("// %s runs the query of its name in %s.\n", m.name, m.query.file)
	}
	receiver := repo.implementation()
	if repo.local {
		receiver = "*" + receiver
	}
	e.printf("func (%s %s) %s%s {\n", recv, receiver, m.name, sig.text)
	if m.bind != nil {
		input := sig.ctx
		if m.bind.accessor {
			input += ".DBTX()"
		}
		e.printf("return %s(%s)\n}\n\n", repo.constructor(), input)
		return
	}
	switch m.verb {
	case "":
		e.handWritten(m, sc, recv, sig)
	case verbInsert, verbUpsert:
		e.insert(m, sc, recv, sig)
	case verbUpdate:
		e.update(m, sc, recv, sig)
	case verbGet, verbFirst, verbList, verbCount, verbExists, verbDelete:
		e.read(m, sc, recv, sig)
	}
	e.printf("}\n\n")
}

// notFound is the error saying no row of table has args, the values of the columns names.
func (e repoEmitter) notFound(table string, names, args []string) string {
	if len(names) == 0 {
		return fmt.Sprintf("%s.Errorf(%q, %s.ErrNotFound)", e.fmtPkg(), "no row of "+table+" matches: %w", e.ormPkg())
	}
	format := make([]string, 0, len(names))
	for _, n := range names {
		format = append(format, n+" %v")
	}
	return fmt.Sprintf("%s.Errorf(%q, %s, %s.ErrNotFound)", e.fmtPkg(),
		table+" with "+strings.Join(format, " and ")+": %w", strings.Join(args, ", "), e.ormPkg())
}

// read writes the body of m, a method reading or deleting the rows its keys and its scopes match.
func (e repoEmitter) read(m repoMethod, sc gofile.Scope, recv string, sig signature) {
	model := "any"
	if m.model != nil {
		model = types.TypeString(m.model, e.f.Qualifier)
	}
	table := sqlName(e.d, m.target.table)
	q := sc.Name("q")
	if m.lock != "" {
		e.inTransaction(m, sc, recv, model)
	}
	if m.scoped {
		qs, i, x := sc.Name("qs"), sc.Name("i"), sc.Name("x")
		e.printf("%s := make([]%s.Of[%s], len(%s))\n", qs, e.ormPkg(), model, sig.scopes)
		e.printf("for %[1]s, %[2]s := range %[3]s {\n%[4]s[%[1]s] = %[2]s.Of()\n}\n", i, x, sig.scopes, qs)
		e.printf("%s := %s.Join(%s...)\n", q, e.ormPkg(), qs)
	} else {
		e.printf("var %s %s.Of[%s]\n", q, e.ormPkg(), model)
	}
	for _, k := range m.keys {
		e.printf("%[1]s = %[1]s.Where(%[2]s, %[3]s)\n"+
			"", q, goString(sqlName(e.d, k.col.col.Name)+" = "), sig.params[k.param])
	}

	names, args := make([]string, 0, len(m.keys)), make([]string, 0, len(m.keys))
	for _, k := range m.keys {
		names, args = append(names, k.col.col.Name), append(args, sig.params[k.param])
	}
	notFound := e.notFound(m.target.table, names, args)
	rd, err := sc.Name("rd"), sc.Name("err")
	render := func(zero string) {
		e.printf("%[1]s, %[2]s := %[3]s.Render(%[4]s, 1)\nif %[2]s != nil {\nreturn %[5]s%[2]s\n}\n", rd, err, q,
			e.dialect(), zero)
	}
	conditionsOnly := func(zero string) {
		e.printf("if %[1]s := %[2]s.ConditionsOnly(%[3]q); %[1]s != nil {\n"+
			"return %[4]s%[1]s\n}\n", err, q, m.name, zero)
	}

	switch m.verb {
	case verbGet, verbFirst:
		rows := sc.Name("rows")
		if m.verb == verbFirst {
			e.printf("%[1]s = %[1]s.Limit(1)\n", q)
		}
		e.printf("%s, %s := select%s(%s, %s.db, %s, %s)\n", rows, err, e.names[m.target.table], sig.ctx, recv, q,
			lockClause(m))
		e.printf("if %[1]s != nil {\nreturn %[2]s{}, %[1]s\n}\n", err, model)
		e.printf("if len(%[1]s) == 0 {\nreturn %[2]s{}, %[3]s\n}\nreturn %[1]s[0], nil\n", rows, model, notFound)
	case verbList:
		e.printf("return select%s(%s, %s.db, %s, %s)\n", e.names[m.target.table], sig.ctx, recv, q, lockClause(m))
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
		e.printf("return %s, %s.Errorf(%q, %s)\n}\nreturn %s, nil\n", zero, e.fmtPkg(),
			strings.ToLower(string(m.verb))+" "+m.target.table+": %w", err, n)
	case verbDelete:
		zero := ""
		if m.result == resultCount {
			zero = "0, "
			conditionsOnly(zero)
		}
		render(zero)
		if m.result == resultCount {
			e.printf("if %s.Where() == \"\" {\nreturn 0, %s.Errorf(%q)\n}\n", rd, e.fmtPkg(),
				"orm: "+m.name+" would delete every row of "+m.target.table+": give it a condition")
		}
		res := e.execResult(sc)
		wrap := e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", "delete from "+m.target.table+": %w") + ", " + err + ")"
		e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s, `DELETE FROM %[6]s`+%[7]s.Where(), %[7]s.Args()...)\n"+
			"if %[2]s != nil {\nreturn %[8]s%[9]s\n}\n", res, err, recv, e.exec, sig.ctx, table, rd, zero, wrap)
		affected := e.rowsAffected(sc, res, err, zero, wrap)
		if m.result == resultCount {
			e.printf("return %s, nil\n", affected)
		} else {
			e.printf("if %s == 0 {\nreturn %s\n}\nreturn nil\n", affected, notFound)
		}
	case verbInsert, verbUpdate, verbUpsert: // method writes them
	}
}

// inTransaction writes the check that m, which locks rows, runs on what can hold a lock: a transaction, which alone has
// a Commit among what DBTX is implemented by.
func (e repoEmitter) inTransaction(m repoMethod, sc gofile.Scope, recv, model string) {
	commit, tx := "Commit() error", "*sql.Tx"
	if e.drv == driverPgx {
		commit, tx = "Commit("+e.f.Import("context", "context")+".Context) error", "pgx.Tx"
	}
	zero := model + "{}"
	if m.result == resultRows {
		zero = "nil"
	}
	e.printf("if _, %[1]s := %[2]s.db.(interface{ %[3]s }); !%[1]s {\nreturn %[4]s, %[5]s.Errorf(%[6]q)\n}\n",
		sc.Name("ok"), recv, commit, zero, e.fmtPkg(),
		"orm: "+m.name+" needs a transaction to hold its lock: give the repository a "+tx)
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
	name := "select" + e.names[rm.target.table]
	model := types.TypeString(rm.model, e.f.Qualifier)
	sc := e.f.Scope()
	ctx, db, q, lock := sc.Name("ctx"), sc.Name("db"), sc.Name("q"), sc.Name("lock")
	rd, err, rows, out, row := sc.Name("rd"), sc.Name("err"), sc.Name("rows"), sc.Name("out"), sc.Name("row")

	query := "SELECT " + e.columnList(rm.columns) + " FROM " + sqlName(e.d, rm.target.table)
	wrap := e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", "read "+rm.target.table+": %w") + ", " + err + ")"

	e.printf("// %s reads the rows of %s that %s matches, in its order", name, rm.target.table, q)
	if len(rm.links) > 0 {
		e.printf(", with the relations it loads")
	}
	e.printf(". %s is the locking clause of the query, if any.\n", lock)
	e.printf("func %[1]s(%[2]s %[3]s.Context, %[4]s DBTX, %[5]s %[6]s.Of[%[7]s], %[8]s string) ([]%[7]s, error) {\n", name,
		ctx, e.f.Import("context", "context"), db, q, e.ormPkg(), model, lock)
	e.printf("%[1]s, %[2]s := %[3]s.Render(%[4]s, 1)\n"+
		"if %[2]s != nil {\nreturn nil, %[2]s\n}\n", rd, err, q, e.dialect())
	e.printf("%[1]s, %[2]s := %[3]s.%[4]s(%[5]s, %[6]s+%[7]s.Where()+%[7]s.OrderBy()+%[7]s.Limit()+%[8]s, "+
		"%[7]s.Args()...)\n", rows, err, db, e.query, ctx, goString(query), rd, lock)
	e.printf("if %s != nil {\nreturn nil, %s\n}\ndefer %s.Close()\n", err, wrap, rows)
	e.scanRows(scanNames{rows: rows, out: out, row: row, err: err, wrap: wrap}, model, rm.columns, sc)
	e.printf("if %s.Reversed() {\n%s.Reverse(%s)\n}\n", rd, e.f.Import("slices", "slices"), out)
	if len(rm.links) > 0 {
		e.printf("if %[1]s := preload%[2]s(%[3]s, %[4]s, %[5]s, %[6]s); %[1]s != nil {\nreturn nil, %[1]s\n}\n", err,
			e.names[rm.target.table], ctx, db, out, q)
	}
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

// scanNames are the names in a loop reading rows: rows into out, each into row, and on an error err, wrapped by wrap.
type scanNames struct {
	rows, out, row, err, wrap string
}

// scanRows writes the loop reading the rows of model, each column into the field holding it.
func (e repoEmitter) scanRows(n scanNames, model string, columns []column, sc gofile.Scope) {
	e.printf("%[1]s := []%[2]s{}\nfor %[3]s.Next() {\nvar %[4]s %[2]s\n", n.out, model, n.rows, n.row)
	dests, assignments := e.scanTargets(sc, n.row, columns)
	e.printf("if %[1]s := %[2]s.Scan(%[3]s); %[1]s != nil {\nreturn nil, %[4]s\n}\n", n.err, n.rows,
		strings.Join(dests, ", "), n.wrap)
	e.assignScanned(assignments)
	e.printf("%[1]s = append(%[1]s, %[2]s)\n}\n", n.out, n.row)
	e.printf("if %[1]s := %[2]s.Err(); %[1]s != nil {\nreturn nil, %[3]s\n}\n", n.err, n.rows, n.wrap)
}

// insert writes the body of m, an Insert or an Upsert: its rows written in one statement, and read back when m
// returns them.
func (e repoEmitter) insert(m repoMethod, sc gofile.Scope, recv string, sig signature) {
	if e.d == snapshot.MySQL {
		e.insertMySQL(m, sc, recv, sig)
		return
	}
	model := types.TypeString(m.model, e.f.Qualifier)
	inserted := m.inserted()
	var (
		query strings.Builder
		args  []string
	)
	fmt.Fprintf(&query, "INSERT INTO %s (%s)\n", sqlName(e.d, m.target.table), e.columnList(inserted))
	if m.many {
		none := "nil"
		if m.result == resultRows {
			none = "[]" + model + "{}, nil"
		}
		e.printf("if len(%s) == 0 {\nreturn %s\n}\n", sig.row, none)
		casts := make([]string, 0, len(inserted))
		for _, c := range inserted {
			v := sc.Name(lowerFirst(c.path[len(c.path)-1]))
			e.printf("%s := make([]%s, len(%s))\n", v, e.storageType(c), sig.row)
			args = append(args, v)
			casts = append(casts, fmt.Sprintf("$%d::%s[]", len(casts)+1, castType(c.col.Type)))
		}
		i := sc.Name("i")
		e.printf("for %s := range %s {\n", i, sig.row)
		for j, c := range inserted {
			e.printf("%s[%s] = %s\n", args[j], i, e.storageValue(c, sig.row+"["+i+"]."+fieldPath(c)))
		}
		e.printf("}\n")
		fmt.Fprintf(&query, "SELECT * FROM unnest(%s)", //nolint:unqueryvet // unnest gives the columns listed
			strings.Join(casts, ", "))
	} else {
		params := make([]string, 0, len(inserted))
		for _, c := range inserted {
			args = append(args, e.storageValue(c, sig.row+"."+fieldPath(c)))
			params = append(params, fmt.Sprintf("$%d", len(params)+1))
		}
		fmt.Fprintf(&query, "VALUES (%s)", strings.Join(params, ", "))
	}

	what := "insert into " + m.target.table
	if m.verb == verbUpsert {
		what = "upsert into " + m.target.table
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
				sets = append(sets, name+" = EXCLUDED."+name)
			}
		}
		fmt.Fprintf(&query, "\nON CONFLICT (%s) DO UPDATE SET %s", strings.Join(key, ", "), strings.Join(sets, ", "))
	}
	if m.result != resultNone {
		fmt.Fprintf(&query, "\nRETURNING %s", e.columnList(m.columns))
	}

	q, err := sc.Name("q"), sc.Name("err")
	wrap := e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", what+": %w") + ", " + err + ")"
	call := strings.Join(append([]string{sig.ctx, q}, args...), ", ")
	e.printf("const %s = %s\n", q, goString(query.String()))
	switch m.result {
	case resultRow:
		row := sc.Name("row")
		e.printf("var %s %s\n", row, model)
		dests, assignments := e.scanTargets(sc, row, m.columns)
		e.printf("%[1]s := %[2]s.db.%[3]s(%[4]s).\nScan(%[5]s)\nif %[1]s != nil {\nreturn %[6]s{}, %[7]s\n}\n",
			err, recv, e.queryRow, call, strings.Join(dests, ", "), model, wrap)
		e.assignScanned(assignments)
		e.printf("return %s, nil\n", row)
	case resultRows:
		rows, out, row := sc.Name("rows"), sc.Name("out"), sc.Name("row")
		e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s)\nif %[2]s != nil {\nreturn nil, %[6]s\n}\n"+
			"defer %[1]s.Close()\n", rows, err, recv, e.query, call, wrap)
		e.scanRows(scanNames{rows: rows, out: out, row: row, err: err, wrap: wrap}, model, m.columns, sc)
		e.printf("return %s, nil\n", out)
	case resultNone:
		e.printf("if _, %[1]s := %[2]s.db.%[3]s(%[4]s); %[1]s != nil {\nreturn %[5]s\n}\nreturn nil\n", err, recv,
			e.exec, call, wrap)
	case resultCount, resultBool, resultScalar: // shaped turns them down for a write
	}
}

// update writes the body of m, an Update: its row written over the one with its key, or orm.ErrNotFound when none
// has it.
func (e repoEmitter) update(m repoMethod, sc gofile.Scope, recv string, sig signature) {
	table := sqlName(e.d, m.target.table)
	updated, key := m.updated(), m.rowKey()
	sets, conds := make([]string, 0, len(updated)), make([]string, 0, len(key))
	args, keyArgs := make([]string, 0, len(updated)+len(key)), make([]string, 0, len(key))
	for _, c := range updated {
		args = append(args, e.storageValue(c, sig.row+"."+fieldPath(c)))
		sets = append(sets, sqlName(e.d, c.col.Name)+" = "+e.placeholder(len(args)))
	}
	for _, name := range key {
		args = append(args, sig.row+"."+fieldPath(m.field(name)))
		keyArgs = append(keyArgs, sig.row+"."+fieldPath(m.field(name)))
		conds = append(conds, sqlName(e.d, name)+" = "+e.placeholder(len(args)))
	}
	where := strings.Join(conds, " AND ")
	query := "UPDATE " + table + "\nSET " + strings.Join(sets, ", ") + "\nWHERE " + where

	q, res, err := sc.Name("q"), e.execResult(sc), sc.Name("err")
	wrap := e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", "update "+m.target.table+": %w") + ", " + err + ")"
	e.printf("const %s = %s\n", q, goString(query))
	e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s)\nif %[2]s != nil {\nreturn %[6]s\n}\n", res, err, recv, e.exec,
		strings.Join(append([]string{sig.ctx, q}, args...), ", "), wrap)
	affected, notFound := e.rowsAffected(sc, res, err, "", wrap), e.notFound(m.target.table, key, keyArgs)
	if e.d != snapshot.MySQL {
		e.printf("if %s == 0 {\nreturn %s\n}\nreturn nil\n", affected, notFound)
		return
	}
	found := sc.Name("found")
	e.printf("if %s == 0 {\n"+
		"// MySQL counts the rows an update changes, not those it finds: see whether the row is there.\n"+
		"var %s bool\n", affected, found)
	e.printf("if %[1]s := %[2]s.db.%[3]s(%[4]s, %[5]s, %[6]s).Scan(&%[7]s); %[1]s != nil {\nreturn %[8]s\n}\n", err, recv,
		e.queryRow, sig.ctx, goString("SELECT EXISTS (SELECT 1 FROM "+table+" WHERE "+where+")"),
		strings.Join(keyArgs, ", "), found, wrap)
	e.printf("if !%s {\nreturn %s\n}\n}\nreturn nil\n", found, notFound)
}

// insertMySQL writes the body of m, an Insert or an Upsert, on MySQL: its rows written in VALUES, as many to a
// statement as its placeholders allow, and its row, when m returns it, read back by LAST_INSERT_ID() or by its key.
func (e repoEmitter) insertMySQL(m repoMethod, sc gofile.Scope, recv string, sig signature) {
	model := types.TypeString(m.model, e.f.Qualifier)
	table := sqlName(e.d, m.target.table)
	inserted := m.inserted()
	auto, hasAuto := m.autoIncrement()
	values := "(" + strings.Repeat("?, ", len(inserted)-1) + "?)"
	head := "INSERT INTO " + table + " (" + e.columnList(inserted) + ")\nVALUES " + values
	what, tail := "insert into "+m.target.table, ""
	if m.verb == verbUpsert {
		what = "upsert into " + m.target.table
		var sets []string
		if hasAuto && m.result == resultRow { // for LAST_INSERT_ID() to tell the row met
			name := sqlName(e.d, auto)
			sets = append(sets, name+" = LAST_INSERT_ID("+name+")")
		}
		for _, c := range m.updated() {
			name := sqlName(e.d, c.col.Name)
			sets = append(sets, name+" = new."+name)
		}
		if len(sets) == 0 { // the key set to itself, as an update of nothing
			name := sqlName(e.d, m.rowKey()[0])
			sets = append(sets, name+" = new."+name)
		}
		tail = " AS new\nON DUPLICATE KEY UPDATE " + strings.Join(sets, ", ")
	}

	err := sc.Name("err")
	wrap := e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", what+": %w") + ", " + err + ")"
	if m.many {
		chunk, args, i := sc.Name("chunk"), sc.Name("args"), sc.Name("i")
		fields := make([]string, 0, len(inserted))
		for _, c := range inserted {
			fields = append(fields, e.storageValue(c, chunk+"["+i+"]."+fieldPath(c)))
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
		e.printf("// As many rows to a statement as its 65,535 placeholders take.\n")
		e.printf("for %[1]s := range %[2]s.Chunk(%[3]s, %[4]d) {\n%[5]s := make([]any, 0, %[6]s)\n"+
			"for %[7]s := range %[1]s {\n%[5]s = append(%[5]s, %[8]s)\n}\n", chunk, e.f.Import("slices", "slices"), sig.row,
			65535/len(inserted), args, capacity, i, strings.Join(fields, ", "))
		e.printf("if _, %[1]s := %[2]s.db.%[3]s(%[4]s, %[5]s, %[6]s...); %[1]s != nil {\nreturn %[7]s\n}\n}\n"+
			"return nil\n", err, recv, e.exec, sig.ctx, query, args, wrap)
		return
	}

	q := sc.Name("q")
	args := make([]string, 0, 2+len(inserted))
	args = append(args, sig.ctx, q)
	for _, c := range inserted {
		args = append(args, e.storageValue(c, sig.row+"."+fieldPath(c)))
	}
	call := strings.Join(args, ", ")
	e.printf("const %s = %s\n", q, goString(head+tail))
	if m.result != resultRow {
		e.printf("if _, %[1]s := %[2]s.db.%[3]s(%[4]s); %[1]s != nil {\nreturn %[5]s\n}\nreturn nil\n", err, recv,
			e.exec, call, wrap)
		return
	}

	// The row read back by the value LAST_INSERT_ID() tells, or else by its key.
	zero := model + "{}, "
	key := m.table.PrimaryKey
	if m.verb == verbUpsert && !hasAuto {
		key = m.rowKey()
	}
	conds := make([]string, 0, len(key))
	keyArgs := make([]string, 0, len(key))
	id := sc.Name("id")
	assign := ":="
	if hasAuto {
		res := e.execResult(sc)
		e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s)\nif %[2]s != nil {\nreturn %[6]s%[7]s\n}\n", res, err, recv,
			e.exec, call, zero, wrap)
		e.printf("%[1]s, %[2]s := %[3]s.LastInsertId()\nif %[2]s != nil {\nreturn %[4]s%[5]s\n}\n", id, err, res, zero,
			wrap)
		key, assign = []string{auto}, "="
	} else {
		e.printf("if _, %[1]s := %[2]s.db.%[3]s(%[4]s); %[1]s != nil {\nreturn %[5]s%[6]s\n}\n", err, recv, e.exec, call,
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
	e.printf("%[1]s %[2]s %[3]s.db.%[4]s(%[5]s, %[6]s, %[7]s).\nScan(%[8]s)\nif %[1]s != nil {\nreturn %[9]s%[10]s\n}\n",
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
	call := strings.Join(args, ", ")
	what := fmt.Sprintf("%q", "query "+m.name+": %w")
	wrap := e.fmtPkg() + ".Errorf(" + what + ", " + err + ")"
	e.printf("const %s = %s\n", q, goString(m.query.sql))
	switch m.result {
	case resultNone:
		e.printf("if _, %[1]s := %[2]s.db.%[3]s(%[4]s); %[1]s != nil {\nreturn %[5]s\n}\nreturn nil\n", err, recv,
			e.exec, call, wrap)
	case resultCount:
		res := e.execResult(sc)
		e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s)\nif %[2]s != nil {\nreturn 0, %[6]s\n}\n", res, err, recv,
			e.exec, call, wrap)
		e.printf("return %s, nil\n", e.rowsAffected(sc, res, err, "0, ", wrap))
	case resultRow, resultScalar:
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
		e.printf("if %[1]s.Is(%[2]s, %[3]s.ErrNoRows) {\n%[4]s"+
			"return %[5]s, %[6]s.Errorf(%[7]s, %[8]s.ErrNotFound)\n}\n",
			e.f.Import("errors", "errors"), err, e.driverPkg(), zeroDeclaration, zeroValue, e.fmtPkg(), what, e.ormPkg())
		e.printf("if %[1]s != nil {\n%[2]sreturn %[3]s, %[4]s\n}\n", err, zeroDeclaration, zeroValue, wrap)
		e.assignScanned(assignments)
		e.printf("return %s, nil\n", row)
	case resultRows:
		rows, out, row := sc.Name("rows"), sc.Name("out"), sc.Name("row")
		typ := types.TypeString(m.query.row, e.f.Qualifier)
		e.printf("%[1]s, %[2]s := %[3]s.db.%[4]s(%[5]s)\nif %[2]s != nil {\nreturn nil, %[6]s\n}\n"+
			"defer %[1]s.Close()\n", rows, err, recv, e.query, call, wrap)
		n := scanNames{rows: rows, out: out, row: row, err: err, wrap: wrap}
		if m.query.scalar {
			e.scalarRows(n, typ)
		} else {
			e.scanRows(n, typ, m.query.fields, sc)
		}
		e.printf("return %s, nil\n", out)
	case resultBool: // a query written by hand returns no bool
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

// inChunk is how many keys MySQL is given in one IN (...), for a statement to take far fewer than the 65,535
// placeholders it can.
const inChunk = 1000

// preloadNames are the names in a function loading relations.
type preloadNames struct {
	ctx, db, rows, q, sub, ok, keys, seen, chunk, related, err, byKey, i, j, found string
	pairs, args, key, pk, tk, targetKeys, parentsOf                                string
}

// preloadRows writes the function loading into rows of rm the relations a scope loads, each by the keys of the rows.
func (e repoEmitter) preloadRows(rm readModel) {
	name := "preload" + e.names[rm.target.table]
	model := types.TypeString(rm.model, e.f.Qualifier)
	sc := e.f.Scope()
	n := preloadNames{
		ctx: sc.Name("ctx"), db: sc.Name("db"), rows: sc.Name("rows"), q: sc.Name("q"), sub: sc.Name("sub"),
		ok: sc.Name("ok"), keys: sc.Name("keys"), seen: sc.Name("seen"), chunk: sc.Name("chunk"),
		related: sc.Name("related"), err: sc.Name("err"), byKey: sc.Name("byKey"), i: sc.Name("i"), j: sc.Name("j"),
		found: sc.Name("found"),
	}
	if slices.ContainsFunc(rm.links, func(lk link) bool { return lk.way.kind == manyToMany }) {
		n.pairs, n.args, n.key, n.pk, n.tk = sc.Name("pairs"), sc.Name("args"), sc.Name("v"), sc.Name("pk"), sc.Name("tk")
		n.targetKeys, n.parentsOf = sc.Name("targetKeys"), sc.Name("parentsOf")
	}

	e.printf("// %s loads into %s the relations %s loads, each by the keys of the rows.\n", name, n.rows, n.q)
	e.printf("func %[1]s(%[2]s %[3]s.Context, %[4]s DBTX, %[5]s []%[6]s, %[7]s %[8]s.Of[%[6]s]) error {\n", name, n.ctx,
		e.f.Import("context", "context"), n.db, n.rows, model, n.q, e.ormPkg())
	e.printf("if len(%s) == 0 {\nreturn nil\n}\n", n.rows)
	composite := func(lk link) bool { return len(lk.keys) > 1 || len(lk.targetKeys) > 1 }
	var c compositeNames
	if slices.ContainsFunc(rm.links, composite) {
		c = newCompositeNames(sc, rm.links)
	}
	for _, lk := range rm.links {
		if composite(lk) {
			e.preloadComposite(lk, n, c)
			continue
		}
		e.preload(lk, n)
	}
	e.printf("return nil\n}\n\n")
}

// preload writes the block loading the relation lk into the rows, when the scope loads it: the keys of the rows, each
// once; the rows related to them, in the order of the scope given for the relation; and these handed to the rows they
// are related to. On MySQL, the keys go in chunks, so that the rows related to one row come in one chunk, in their
// order.
func (e repoEmitter) preload(lk link, n preloadNames) {
	own, other := lk.keys[0], lk.targetKeys[0]
	keyType := types.TypeString(elemOf(own.typ), e.f.Qualifier)
	elemType := types.TypeString(lk.model, e.f.Qualifier)
	if s, ok := lk.field.typ.Underlying().(*types.Slice); ok {
		elemType = types.TypeString(s.Elem(), e.f.Qualifier)
	}
	many := lk.way.kind == hasMany || lk.way.kind == manyToMany
	valueType := elemType
	if many {
		valueType = "[]" + elemType
	}

	e.printf("if %[1]s, %[2]s := %[3]s.Preloaded[%[4]s](%[5]s, %[6]q); %[2]s {\n", n.sub, n.ok, e.ormPkg(),
		types.TypeString(lk.model, e.f.Qualifier), n.q, strings.Join(lk.field.path, "."))
	e.printf("%s := make([]%s, 0, len(%s))\n", n.keys, keyType, n.rows)
	add := func(v string) string { return n.keys + " = append(" + n.keys + ", " + v + ")" }
	if lk.way.kind == belongsTo { // the foreign keys of the rows, which may repeat
		e.printf("%s := map[%s]bool{}\n", n.seen, keyType)
		add = func(v string) string {
			return "if !" + n.seen + "[" + v + "] {\n" + n.seen + "[" + v + "] = true\n" + n.keys + " = append(" + n.keys +
				", " + v + ")\n}"
		}
	}
	e.printf("for %s := range %s {\n%s\n}\n", n.i, n.rows,
		ifSet(n.rows+"["+n.i+"]."+fieldPath(own), isPointer(own.typ), add))
	e.printf("%s := map[%s]%s{}\n", n.byKey, keyType, valueType)

	keys := n.keys
	if e.d == snapshot.MySQL {
		e.printf("for %s := range %s.Chunk(%s, %d) {\n", n.chunk, e.f.Import("slices", "slices"), n.keys, inChunk)
		keys = n.chunk
	}
	if lk.way.kind == manyToMany {
		e.throughPairs(lk, n, keyType, keys)
		keys = n.targetKeys
	}
	cond := goString(sqlName(e.d, other.col.Name)+" = ANY(") + ", " + keys
	if e.d == snapshot.MySQL {
		cond = goString(sqlName(e.d, other.col.Name)+" IN (") + ", " + e.ormPkg() + ".List(" + keys + ")"
	}
	e.printf("%[1]s, %[2]s := select%[3]s(%[4]s, %[5]s, %[6]s.Where(%[7]s, %[8]s), \"\")\n"+
		"if %[2]s != nil {\nreturn %[2]s\n}\n", n.related, n.err, e.names[lk.target.table], n.ctx, n.db, n.sub, cond,
		goString(")"))

	elem := n.related + "[" + n.j + "]"
	if strings.HasPrefix(elemType, "*") {
		elem = "&" + elem
	}
	key := n.related + "[" + n.j + "]." + fieldPath(other)
	e.printf("for %s := range %s {\n", n.j, n.related)
	switch {
	case lk.way.kind == manyToMany:
		e.printf("for _, %[1]s := range %[2]s[%[3]s] {\n%[4]s[%[1]s] = append(%[4]s[%[1]s], %[5]s)\n}\n", n.pk,
			n.parentsOf, key, n.byKey, elem)
	case many:
		e.printf("%s\n", ifSet(key, isPointer(other.typ), func(v string) string {
			return n.byKey + "[" + v + "] = append(" + n.byKey + "[" + v + "], " + elem + ")"
		}))
	default:
		e.printf("%s\n", ifSet(key, isPointer(other.typ), func(v string) string {
			return n.byKey + "[" + v + "] = " + elem
		}))
	}
	e.printf("}\n")
	if e.d == snapshot.MySQL {
		e.printf("}\n")
	}

	dest := n.rows + "[" + n.i + "]." + strings.Join(lk.field.path, ".")
	value := n.found
	if !many && isPointer(lk.field.typ) {
		value = "&" + n.found
	}
	e.printf("for %s := range %s {\n", n.i, n.rows)
	if many {
		e.printf("%s = []%s{}\n", dest, elemType)
	}
	e.printf("%s\n}\n}\n", ifSet(n.rows+"["+n.i+"]."+fieldPath(own), isPointer(own.typ), func(v string) string {
		return "if " + n.found + ", " + n.ok + " := " + n.byKey + "[" + v + "]; " + n.ok + " {\n" + dest + " = " +
			value + "\n}"
	}))
}

// throughPairs writes the query reading, from the table a many-to-many relation lk goes through, the keys of the
// rows keys holds and those of the rows related to them: n.targetKeys, each once, and n.parentsOf, the keys of the
// rows each is related to.
func (e repoEmitter) throughPairs(lk link, n preloadNames, keyType, keys string) {
	targetType := types.TypeString(elemOf(lk.targetKeys[0].typ), e.f.Qualifier)
	fkColumn := sqlName(e.d, lk.way.fk.Columns[0])
	query := "SELECT " + fkColumn + ", " + sqlName(e.d, lk.way.throughFK.Columns[0]) + " FROM " +
		sqlName(e.d, lk.way.table) + " WHERE " + fkColumn
	wrap := e.fmtPkg() + ".Errorf(" + fmt.Sprintf("%q", "read "+lk.way.table+": %w") + ", " + n.err + ")"
	if e.d == snapshot.MySQL {
		e.printf("%[1]s := make([]any, 0, len(%[2]s))\nfor _, %[3]s := range %[2]s {\n%[1]s = append(%[1]s, %[3]s)\n}\n",
			n.args, keys, n.key)
		e.printf("%[1]s, %[2]s := %[3]s.%[4]s(%[5]s, %[6]s+%[7]s.Repeat(%[8]s, len(%[9]s)-1)+%[10]s, %[11]s...)\n",
			n.pairs, n.err, n.db, e.query, n.ctx, goString(query+" IN (?"), e.f.Import("strings", "strings"),
			goString(", ?"), keys, goString(")"), n.args)
	} else {
		e.printf("%[1]s, %[2]s := %[3]s.%[4]s(%[5]s, %[6]s, %[7]s)\n", n.pairs, n.err, n.db, e.query, n.ctx,
			goString(query+" = ANY($1)"), keys)
	}
	e.printf("if %[1]s != nil {\nreturn %[2]s\n}\n", n.err, wrap)
	e.printf("%[1]s := map[%[2]s][]%[3]s{}\nvar %[4]s []%[2]s\n"+
		"for %[5]s.Next() {\nvar %[6]s %[3]s\nvar %[7]s %[2]s\n", n.parentsOf,
		targetType, keyType, n.targetKeys, n.pairs, n.pk, n.tk)
	e.printf("if %[1]s := %[2]s.Scan(&%[3]s, &%[4]s); %[1]s != nil {\n%[2]s.Close()\nreturn %[5]s\n}\n", n.err, n.pairs,
		n.pk, n.tk, wrap)
	e.printf("if _, %[1]s := %[2]s[%[3]s]; !%[1]s {\n%[4]s = append(%[4]s, %[3]s)\n}\n"+
		"%[2]s[%[3]s] = append(%[2]s[%[3]s], %[5]s)\n}\n", n.ok, n.parentsOf, n.tk, n.targetKeys, n.pk)
	e.printf("if %[1]s := %[2]s.Err(); %[1]s != nil {\nreturn %[3]s\n}\n", n.err, n.pairs, wrap)
}

// fieldPath is how a row reaches the field holding c.
func fieldPath(c column) string {
	return strings.Join(c.path, ".")
}

func isPointer(t types.Type) bool {
	_, ok := t.Underlying().(*types.Pointer)
	return ok
}

// ifSet is what do says of the value of expr, guarded, when it is a pointer, by a check that it is set.
func ifSet(expr string, pointer bool, do func(v string) string) string {
	if !pointer {
		return do(expr)
	}
	return "if " + expr + " != nil {\n" + do("*"+expr) + "\n}"
}

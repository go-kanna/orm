package gen

import (
	"fmt"
	"go/types"
	"maps"
	"slices"
	"strings"

	"github.com/go-kanna/internal/gofile"

	"github.com/go-kanna/orm/internal/snapshot"
)

// repoEmitter writes the implementations of repositories into a file of their destination.
type repoEmitter struct {
	f *gofile.File
	d snapshot.Dialect
	// orm, fmt, and slices are the names the file imports those packages by.
	orm, fmt, slices string
	// selects holds, by table, the function reading the whole rows of a table, and a method reading them.
	selects map[string]selectFunc
}

type selectFunc struct {
	name string
	m    repoMethod
}

// emitRepositories writes orm_gen.go into the destination, the package pkgName at pkgPath: the implementation of each
// of repos on a PostgreSQL database through pgx. declared names what the destination declares besides.
func emitRepositories(pkgPath, pkgName string, d snapshot.Dialect, repos []repository, declared []string) (
	[]byte, []string, error,
) {
	f := gofile.New(generator, pkgPath, pkgName)
	f.Reserve(declared...)
	f.Reserve("DB")
	for _, repo := range repos {
		f.Reserve(repo.name, "New"+repo.name)
	}
	e := repoEmitter{f: f, d: d, selects: map[string]selectFunc{}}
	for _, repo := range repos {
		for _, m := range repo.methods {
			switch {
			case m.query != nil:
				return nil, nil, fmt.Errorf("%s.%s: a query written by hand is not generated yet", repo.name, m.name)
			case m.verb == verbInsert || m.verb == verbUpdate || m.verb == verbUpsert:
				return nil, nil, fmt.Errorf("%s.%s: %s is not generated yet", repo.name, m.name, m.verb)
			case m.verb == verbGet || m.verb == verbFirst || m.verb == verbList:
				if _, ok := e.selects[m.target.table]; !ok {
					name, ok := funcName(m.target.table)
					if !ok {
						name = m.target.name
					}
					e.selects[m.target.table] = selectFunc{name: "select" + name, m: m}
					f.Reserve("select" + name)
				}
			}
		}
	}
	e.orm, e.fmt, e.slices = f.Import(ormPath, "orm"), f.Import("fmt", "fmt"), f.Import("slices", "slices")

	e.db()
	for _, repo := range repos {
		e.repository(repo)
	}
	for _, table := range slices.Sorted(maps.Keys(e.selects)) {
		e.selectRows(e.selects[table])
	}

	src, err := f.Bytes()
	if err != nil {
		return src, nil, fmt.Errorf("the code generated does not parse: %w", err)
	}
	return src, f.ModuleImports(), nil
}

func (e repoEmitter) printf(format string, args ...any) {
	fmt.Fprintf(e.f, format, args...)
}

// db writes DB, what the repositories run on.
func (e repoEmitter) db() {
	ctx := e.f.Import("context", "context")
	pgconn := e.f.Import("github.com/jackc/pgx/v5/pgconn", "pgconn")
	pgx := e.f.Import("github.com/jackc/pgx/v5", "pgx")
	e.printf("// DB is what the repositories run on: *pgxpool.Pool, *pgx.Conn, or pgx.Tx.\ntype DB interface {\n")
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
	e.printf("// %s implements %s on PostgreSQL through pgx.\ntype %[1]s struct{ db DB }\n\n", repo.name, iface)
	e.printf("// New%[1]s returns %[2]s %[1]s running on db.\nfunc New%[1]s(db DB) %[1]s { return %[1]s{db: db} }\n\n",
		repo.name, article(repo.name))
	e.printf("var _ %s = %s{}\n\n", iface, repo.name)
	for _, m := range repo.methods {
		e.method(repo, m)
	}
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
			params = append(params, local+" ..."+types.TypeString(s.Elem(), e.f.Qualifier))
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
		return e.orm + ".MySQL"
	}
	return e.orm + ".Postgres"
}

// method writes the method m of repo.
func (e repoEmitter) method(repo repository, m repoMethod) {
	sc := e.f.Scope()
	recv := sc.Name("r")
	sig := e.signature(m, sc)
	model := types.TypeString(m.model, e.f.Qualifier)
	table := sqlName(e.d, m.target.table)
	e.printf("func (%s %s) %s%s {\n", recv, repo.name, m.name, sig.text)

	q := sc.Name("q")
	if m.scoped {
		qs, i, x := sc.Name("qs"), sc.Name("i"), sc.Name("x")
		e.printf("%s := make([]%s.Of[%s], len(%s))\n", qs, e.orm, model, sig.scopes)
		e.printf("for %[1]s, %[2]s := range %[3]s {\n%[4]s[%[1]s] = %[2]s.Of()\n}\n", i, x, sig.scopes, qs)
		e.printf("%s := %s.Join(%s...)\n", q, e.orm, qs)
	} else {
		e.printf("var %s %s.Of[%s]\n", q, e.orm, model)
	}
	for _, k := range m.keys {
		e.printf("%[1]s = %[1]s.Where(%s, %s)\n", q, goString(sqlName(e.d, k.col.col.Name)+" = "), sig.params[k.param])
	}

	// notFound says which row was not found, by the keys.
	notFound := func() string {
		if len(m.keys) == 0 {
			return fmt.Sprintf("%s.Errorf(%q, %s.ErrNotFound)", e.fmt, "no row of "+m.target.table+" matches: %w", e.orm)
		}
		var format, args []string
		for _, k := range m.keys {
			format = append(format, k.col.col.Name+" %v")
			args = append(args, sig.params[k.param])
		}
		return fmt.Sprintf("%s.Errorf(%q, %s, %s.ErrNotFound)", e.fmt,
			m.target.table+" with "+strings.Join(format, " and ")+": %w", strings.Join(args, ", "), e.orm)
	}
	rd, err, n := sc.Name("rd"), sc.Name("err"), sc.Name("n")
	render := func(zero string) {
		e.printf("%[1]s, %[2]s := %[3]s.Render(%[4]s, 1)\nif %[2]s != nil {\nreturn %[5]s%[2]s\n}\n", rd, err, q,
			e.dialect(), zero)
	}
	conditionsOnly := func(zero string) {
		e.printf("if %[1]s := %[2]s.ConditionsOnly(%[3]q); %[1]s != nil {\nreturn %[4]s%[1]s\n}\n", err, q, m.name, zero)
	}

	switch m.verb {
	case verbGet, verbFirst:
		rows := sc.Name("rows")
		if m.verb == verbFirst {
			e.printf("%[1]s = %[1]s.Limit(1)\n", q)
		}
		e.printf("%s, %s := %s(%s, %s.db, %s)\n", rows, err, e.selects[m.target.table].name, sig.ctx, recv, q)
		e.printf("if %s != nil {\nreturn %s{}, %[1]s\n}\n", err, model)
		e.printf("if len(%s) == 0 {\nreturn %s{}, %s\n}\nreturn %[1]s[0], nil\n", rows, model, notFound())
	case verbList:
		e.printf("return %s(%s, %s.db, %s)\n", e.selects[m.target.table].name, sig.ctx, recv, q)
	case verbCount, verbExists:
		query, typ, zero := "`SELECT count(*) FROM "+table+"`+"+rd+".Where()", "int64", "0"
		if m.verb == verbExists {
			query, typ, zero = "`SELECT EXISTS (SELECT 1 FROM "+table+"`+"+rd+".Where()+`)`", "bool", "false"
		}
		conditionsOnly(zero + ", ")
		render(zero + ", ")
		e.printf("var %s %s\n", n, typ)
		e.printf("if %[1]s := %[2]s.db.QueryRow(%[3]s, %[4]s, %[5]s.Args()...).Scan(&%[6]s); %[1]s != nil {\n",
			err, recv, sig.ctx, query, rd, n)
		e.printf("return %s, %s.Errorf(%q, %s)\n}\nreturn %s, nil\n", zero, e.fmt,
			strings.ToLower(string(m.verb))+" "+m.target.table+": %w", err, n)
	case verbDelete:
		zero := ""
		if m.result == resultCount {
			zero = "0, "
			conditionsOnly(zero)
		}
		render(zero)
		if m.result == resultCount {
			e.printf("if %s.Where() == \"\" {\nreturn 0, %s.Errorf(%q)\n}\n", rd, e.fmt,
				"orm: "+m.name+" would delete every row of "+m.target.table+": give it a condition")
		}
		tag := sc.Name("tag")
		e.printf("%s, %s := %s.db.Exec(%s, `DELETE FROM %s`+%s.Where(), %[6]s.Args()...)\n", tag, err, recv, sig.ctx,
			table, rd)
		e.printf("if %s != nil {\nreturn %s%s.Errorf(%q, %[1]s)\n}\n", err, zero, e.fmt,
			"delete from "+m.target.table+": %w")
		if m.result == resultCount {
			e.printf("return %s.RowsAffected(), nil\n", tag)
		} else {
			e.printf("if %s.RowsAffected() == 0 {\nreturn %s\n}\nreturn nil\n", tag, notFound())
		}
	case verbInsert, verbUpdate, verbUpsert: // emitRepositories has turned them down
	}
	e.printf("}\n\n")
}

// selectRows writes the function reading the whole rows of a table that a scope matches, in its order.
func (e repoEmitter) selectRows(s selectFunc) {
	m := s.m
	model := types.TypeString(m.model, e.f.Qualifier)
	sc := e.f.Scope()
	ctx, db, q := sc.Name("ctx"), sc.Name("db"), sc.Name("q")
	rd, err, rows, out, row := sc.Name("rd"), sc.Name("err"), sc.Name("rows"), sc.Name("out"), sc.Name("row")

	columns := make([]string, 0, len(m.columns))
	dests := make([]string, 0, len(m.columns))
	for _, c := range m.columns {
		columns = append(columns, sqlName(e.d, c.col.Name))
		dests = append(dests, "&"+row+"."+strings.Join(c.path, "."))
	}
	query := "SELECT " + strings.Join(columns, ", ") + " FROM " + sqlName(e.d, m.target.table)
	wrap := e.fmt + ".Errorf(" + fmt.Sprintf("%q", "read "+m.target.table+": %w") + ", " + err + ")"

	e.printf("// %s reads the rows of %s that %s matches, in its order.\n", s.name, m.target.table, q)
	e.printf("func %s(%s %s.Context, %s DB, %s %s.Of[%s]) ([]%[7]s, error) {\n", s.name, ctx,
		e.f.Import("context", "context"), db, q, e.orm, model)
	e.printf("%s, %s := %s.Render(%s, 1)\nif %[2]s != nil {\nreturn nil, %[2]s\n}\n", rd, err, q, e.dialect())
	e.printf("%s, %s := %s.Query(%s, %s+%s.Where()+%[6]s.OrderBy()+%[6]s.Limit(), %[6]s.Args()...)\n", rows, err, db,
		ctx, goString(query), rd)
	e.printf("if %s != nil {\nreturn nil, %s\n}\ndefer %s.Close()\n", err, wrap, rows)
	e.printf("%s := []%s{}\nfor %s.Next() {\nvar %s %[2]s\n", out, model, rows, row)
	e.printf("if %[1]s := %[2]s.Scan(%[3]s); %[1]s != nil {\nreturn nil, %[4]s\n}\n", err, rows,
		strings.Join(dests, ", "), wrap)
	e.printf("%[1]s = append(%[1]s, %[2]s)\n}\n", out, row)
	e.printf("if %[1]s := %[2]s.Err(); %[1]s != nil {\nreturn nil, %[3]s\n}\n", err, rows, wrap)
	e.printf("if %s.Reversed() {\n%s.Reverse(%s)\n}\nreturn %[3]s, nil\n}\n\n", rd, e.slices, out)
}

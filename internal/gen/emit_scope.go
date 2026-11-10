package gen

import (
	"fmt"
	"go/types"
	"strconv"
	"strings"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/gofile"
	"github.com/go-kanna/internal/packages"
	"github.com/go-kanna/internal/valuetype"

	"github.com/go-kanna/orm/internal/snapshot"
	"github.com/go-kanna/orm/internal/sqlfile"
)

const generator = "kanna-orm"

// scopePlan is what is generated for a scope type.
type scopePlan struct {
	scope

	definition snapshot.Table
	columns    []column
	links      []link
	// primaryKey holds the fields holding the columns of the table's primary key, in its order, or nothing when the
	// table has none or the model leaves a column of it out, keyless naming that column: a page continues from a row by
	// it.
	primaryKey []column
	keyless    string
	// written are the scopes written by hand in the .sql files of the package.
	written []sqlScope
	// unchecked names the fields of the model whose types do not type-check, which the run generates nothing for yet.
	unchecked []string
}

// plan finds the scope types of pkg, checks them against s and against the others of the module in idx, and matches
// the fields of their models to the columns and the relations of their tables.
func plan(pkg *packages.Package, s snapshot.Snapshot, idx []indexed, stmts []sqlfile.Statement) (
	[]scopePlan, []diag.Diag,
) {
	scopes, ds := findScopes(pkg, s, idx)
	ds = append(ds, checkUnique(pkg, scopes, idx)...)
	l := linker{fset: pkg.Fset, pkgPath: pkg.PkgPath, pkgName: pkg.Name, scopes: scopes, tables: s.Tables, idx: idx}
	plans := make([]scopePlan, 0, len(scopes))
	changeSets := map[string]string{}
	for _, sc := range scopes {
		set, fn := changeSetNames(sc.name)
		if other, ok := changeSets[set]; ok {
			ds = append(ds, diag.Errorf(sc.pos, "%s: its change set would be %s, as that of %s is", sc.name, set, other).
				WithHints("name one of the two scope types otherwise"))
		}
		changeSets[set] = sc.name
		for _, name := range []string{fn, set} {
			if at, ok := declared(pkg, name); ok {
				ds = append(ds, diag.Errorf(at, "%s is reserved for generated scope helpers", name))
			}
		}
	}
	for _, sc := range scopes {
		f, fds := fields(pkg.Fset, sc, s.Tables[sc.table])
		ds = append(ds, fds...)
		ds = append(ds, uuidsBeforeGo127(pkg, sc, f.columns)...)
		links, lds := l.link(sc, f)
		ds = append(ds, lds...)
		ds = append(ds, sameNames(sc, f.columns, links)...)
		key, missing := held(f.columns, s.Tables[sc.table].PrimaryKey)
		if missing != "" {
			key = nil
		}
		plans = append(plans, scopePlan{scope: sc, definition: s.Tables[sc.table], columns: f.columns,
			links: links, primaryKey: key, keyless: missing,
			unchecked: f.unchecked})
	}
	written, wds := sqlScopes(pkg, s, plans, stmts)
	for i := range plans {
		plans[i].written = written[plans[i].name]
	}
	ds = append(ds, wds...)
	// The fields of a model another package declares the scope type of are told once, however many relations reach it.
	return plans, distinct(append(ds, checkNames(pkg, plans, s.Dialect)...))
}

// sameNames reports a field whose name is that of another of its kind, columns or links, so that their methods would
// be named alike: a field of a struct embedded, named as one of the model's own.
func sameNames(sc scope, columns []column, links []link) []diag.Diag {
	var ds []diag.Diag
	model := sc.model.Obj().Name()
	seen := map[string][]string{}
	for _, c := range columns {
		name := c.path[len(c.path)-1]
		if other, ok := seen[name]; ok {
			ds = append(ds, diag.Errorf(c.pos, "%s.%s: its methods would be named as those of %s.%s, such as %sEq",
				model, strings.Join(c.path, "."), model, strings.Join(other, "."), name).
				WithHints(`leave one of them out with orm:"-"`))
			continue
		}
		seen[name] = c.path
	}
	seen = map[string][]string{}
	for _, lk := range links {
		name := lk.field.path[len(lk.field.path)-1]
		if other, ok := seen[name]; ok {
			ds = append(ds, diag.Errorf(lk.field.pos, "%s.%s: its method would be named as that of %s.%s, With%s",
				model, strings.Join(lk.field.path, "."), model, strings.Join(other, "."), name).
				WithHints(`leave one of them out with orm:"-"`))
			continue
		}
		seen[name] = lk.field.path
	}
	return ds
}

// emit writes scopes_gen.go for pkg: the methods of the scope types plans has, for dialect d. declared names
// what pkg declares at its top level besides. It returns the file, and what it imports from outside the standard
// library.
func emit(pkg *packages.Package, d snapshot.Dialect, plans []scopePlan, declared []string) ([]byte, []string, error) {
	f := gofile.New(generator, pkg.PkgPath, pkg.Name)
	f.Reserve(declared...)
	for _, p := range plans {
		set, fn := changeSetNames(p.name)
		f.Reserve(p.name, fn, set)
		if p.fn != "" {
			f.Reserve(p.fn)
		}
	}
	var toRuntime, toScope string
	if len(plans) > 0 {
		sc := f.Scope()
		toRuntime, toScope = sc.Name("kRuntime"), sc.Name("kScope")
		f.Reserve(toRuntime, toScope)
		scope, impl := f.Import(scopePath, "scope"), f.Import(implPath, "impl")
		fmt.Fprintf(f, "// %[3]s is q, the field of a scope type, as the runtime reads it, and %[4]s is the "+
			"runtime's scope as the\n// field again.\nfunc %[3]s[M any](q %[1]s.Of[M]) %[2]s.Of[M] { return "+
			"%[2]s.Of[M](q) }\n\nfunc %[4]s[M any](q %[2]s.Of[M]) %[1]s.Of[M] { return %[1]s.Of[M](q) }\n\n", scope, impl,
			toRuntime, toScope)
	}
	for _, p := range plans {
		e := emitter{f: f, d: d, p: p, toRuntime: toRuntime, toScope: toScope}
		e.scope()
	}
	src, err := f.Bytes()
	if err != nil {
		return src, nil, fmt.Errorf("the code generated does not parse: %w", err)
	}
	return src, f.ModuleImports(), nil
}

type emitter struct {
	f *gofile.File
	d snapshot.Dialect
	// toRuntime and toScope name the functions turning the field of a scope type into the runtime's scope, and back.
	toRuntime, toScope string
	p                  scopePlan
}

// ref names name of the runtime.
func (e emitter) ref(name string) string { return runtimeRef(e.f, name) }

// printf writes to the file, a method or a function at a time.
func (e emitter) printf(format string, args ...any) {
	fmt.Fprintf(e.f, format, args...)
}

func (e emitter) scope() {
	p := e.p
	model := types.TypeString(p.model, e.f.Qualifier)
	unpaged := e.unpaged()
	if p.implied {
		e.printf("// %[1]s is the scope of %[2]s, whose rows are read into %[3]s, as //kanna:scopes implies it.\n%[5]s"+
			"type %[1]s struct{ q %[4]s.Of[%[3]s] }\n\n", p.name, p.table, model, e.f.Import(scopePath, "scope"), unpaged)
	}
	// What the scope lacks is said where the run writes a doc of it: the function returning it, or, for a type
	// declared by hand, which has none, Of.
	ofDoc := unpaged
	if p.fn != "" {
		e.printf("// %[1]s returns a scope over all the rows of %[2]s.\n%[4]sfunc %[1]s() %[3]s { return %[3]s{} }\n\n",
			p.fn, p.table, p.name, unpaged)
		ofDoc = ""
	}
	s := e.f.Scope().Name("s")
	e.printf("// Of returns the scope as a repository reads it.\n%sfunc (%s %s) Of() %s[%s] { return %s }\n\n",
		ofDoc, s, p.name, e.f.Import(scopePath, "scope")+".Of", model, s+"."+p.field)

	e.changes()
	for _, c := range p.columns {
		e.column(c)
	}
	for _, lk := range p.links {
		e.with(lk)
	}
	for _, w := range p.written {
		e.written(w)
	}

	sc := e.f.Scope()
	s, n := sc.Name("s"), sc.Name("n")
	e.method("keeps the first "+n+" rows.", s, "Limit", n+" int", e.q(s)+".Limit("+n+")")
	e.method("skips the first "+n+" rows.", s, "Offset", n+" int", e.q(s)+".Offset("+n+")")

	sc = e.f.Scope()
	s, alternatives, qs, i, a := sc.Name("s"), sc.Name("alternatives"), sc.Name("qs"), sc.Name("i"), sc.Name("a")
	e.printf("// Or narrows to the rows any of %s matches, each holding conditions alone.\n", alternatives)
	e.printf("func (%[1]s %[2]s) Or(%[3]s ...%[2]s) %[2]s {\n", s, p.name, alternatives)
	e.printf("%s := make([]%s[%s], len(%s))\n", qs, e.ref("Of"), model, alternatives)
	e.printf("for %[1]s, %[2]s := range %[3]s {\n%[4]s[%[1]s] = %[5]s\n}\n", i, a, alternatives, qs, e.q(a))
	e.printf("return %s{%s(%s.Or(%s...))}\n}\n\n", p.name, e.toScope, e.q(s), qs)

	sc = e.f.Scope()
	s, other := sc.Name("s"), sc.Name("other")
	e.method("narrows to the rows for which "+other+", holding conditions alone, is false, not unknown as by a NULL.",
		s, "Not", other+" "+p.name, e.q(s)+".Not("+e.q(other)+")")

	if len(p.primaryKey) > 0 {
		e.paging(model)
	}
}

// unpaged is the doc telling why the scope type has no After, Before, Cursor, or AfterCursor, if it has none: they
// continue a page from a row by the primary key, which the table or the model may not have.
func (e emitter) unpaged() string {
	p := e.p
	var why string
	switch {
	case len(p.primaryKey) > 0:
		return ""
	case p.keyless == "":
		why = p.table + " has no primary key"
	default:
		why = types.TypeString(p.model, e.f.Qualifier) + " holds no field of " + p.keyless + ", of the primary key of " +
			p.table
	}
	return "//\n// It has no After, Before, Cursor, or AfterCursor, which continue a page from a row by the primary " +
		"key:\n// " + why + ".\n"
}

// with writes the method loading the relation lk, which takes scopes of the related rows.
func (e emitter) with(lk link) {
	if lk.scalar != nil {
		name := lk.field.path[len(lk.field.path)-1]
		e.printf("func (s %s) With%s() %s {\n", e.p.name, name, e.p.name)
		e.printf("return %s{%s(%s(%s, %q, %s[%s]{}))}\n}\n\n", e.p.name, e.toScope, e.ref("Preload"), e.q("s"),
			strings.Join(lk.field.path, "."), e.ref("Of"), types.TypeString(lk.scalar.typ, e.f.Qualifier))
		return
	}
	name := lk.field.path[len(lk.field.path)-1]
	target := lk.target.name
	if pkg := e.f.Import(lk.target.pkgPath, lk.target.pkgName); pkg != "" {
		target = pkg + "." + target
	}
	by := "by " + strings.Join(lk.way.fk.Columns, " and ")
	if lk.way.kind == manyToMany {
		by = "through " + lk.way.table
	}

	sc := e.f.Scope()
	s, sub, qs, i, x := sc.Name("s"), sc.Name("sub"), sc.Name("qs"), sc.Name("i"), sc.Name("x")
	preload, each := "Preload", ""
	if lk.way.kind == hasMany || lk.way.kind == manyToMany {
		preload, each = "PreloadEach", " A limit or an offset of the scopes keeps a page of the "+name+" of each row."
	}
	e.printf("// With%s loads %s, from %s %s.%s\n", name, name, lk.target.table, by, each)
	e.printf("func (%[1]s %[2]s) With%[3]s(%[4]s ...%[5]s) %[2]s {\n", s, e.p.name, name, sub, target)
	e.printf("%s := make([]%s[%s], len(%s))\n", qs, e.ref("Of"), types.TypeString(lk.model, e.f.Qualifier), sub)
	e.printf("for %[1]s, %[2]s := range %[3]s {\n%[4]s[%[1]s] = %[5]s(%[2]s.Of())\n}\n", i, x, sub, qs, e.toRuntime)
	e.printf("return %s{%s(%s(%s, %s, %s(%s...)))}\n}\n\n", e.p.name, e.toScope, e.ref(preload), e.q(s),
		strconv.Quote(strings.Join(lk.field.path, ".")), e.ref("Combine"), qs)
}

// written writes the method of w, a scope written by hand, which narrows the scope by its condition.
func (e emitter) written(w sqlScope) {
	sc := e.f.Scope()
	locals, typeOf := map[string]string{}, map[string]types.Type{}
	params := make([]string, 0, len(w.params))
	for _, p := range w.params {
		locals[p.name], typeOf[p.name] = sc.Name(p.name), p.typ
		if s, ok := p.typ.(*types.Slice); ok && p.variadic {
			params = append(params, locals[p.name]+" ..."+types.TypeString(s.Elem(), e.f.Qualifier))
			continue
		}
		params = append(params, locals[p.name]+" "+types.TypeString(p.typ, e.f.Qualifier))
	}
	s := sc.Name("s")

	parts := []string{goString(w.pieces[0])}
	for i, a := range w.args {
		if e.d == snapshot.MySQL && expands(typeOf[a]) {
			parts = append(parts, e.ref(listOf(w.pieces[i], w.pieces[i+1]))+"("+locals[a]+")")
		} else {
			parts = append(parts, locals[a])
		}
		if piece := w.pieces[i+1]; piece != "" || i < len(w.args)-1 {
			parts = append(parts, goString(piece))
		}
	}
	if w.doc == "" {
		e.method("narrows the scope as "+w.file+" writes it.", s, w.name, strings.Join(params, ", "),
			e.where(s, parts...))
		return
	}
	for line := range strings.SplitSeq(w.doc, "\n") {
		e.printf("%s\n", strings.TrimRight("// "+line, " "))
	}
	e.fn(s, w.name, strings.Join(params, ", "), e.where(s, parts...))
}

// paging writes After, Before, Cursor, and AfterCursor, which continue a page from a row of model by the primary key,
// and values, which they read a row by.
func (e emitter) paging(model string) {
	p := e.p
	names := make([]string, 0, len(p.primaryKey))
	for _, c := range p.primaryKey {
		names = append(names, goString(sqlName(e.d, c.col.Name)))
	}
	key := "[]string{" + strings.Join(names, ", ") + "}"

	row := e.f.Scope().Name("row")
	e.printf("// values holds the value of each column of %s, for After, Before, and Cursor. One that may be NULL\n"+
		"// holds MayBeNull, since no page continues by it.\n", row)
	e.printf("func (%s) values(%s %s) map[string]any {\nreturn map[string]any{\n", p.name, row, model)
	for _, c := range p.columns {
		v := row + "." + strings.Join(c.path, ".")
		switch {
		case !c.col.NotNull:
			v = e.ref("MayBeNull")
		case uuidOf(c.typ):
			v = e.f.Import("uuid", "uuid") + ".UUID(" + v + ")"
		}
		e.printf("%s: %s,\n", goString(sqlName(e.d, c.col.Name)), v)
	}
	e.printf("}\n}\n\n")

	sc := e.f.Scope()
	s, row := sc.Name("s"), sc.Name("row")
	e.method(fmt.Sprintf("narrows to the rows after %[1]s in the scope's order, for the page after the one %[1]s is "+
		"the last of.", row), s, "After", row+" "+model, e.q(s)+".After("+key+", "+s+".values("+row+"))")
	e.method(fmt.Sprintf("narrows to the rows before %[1]s in the scope's order, for the page before the one %[1]s is "+
		"the first of.", row), s, "Before", row+" "+model, e.q(s)+".Before("+key+", "+s+".values("+row+"))")
	e.printf("// Cursor encodes the position of %s in the scope's order, for a client to hand back to AfterCursor.\n",
		row)
	e.printf("func (%[1]s %[2]s) Cursor(%[3]s %[4]s) (string, error) {\nreturn %[5]s.Cursor(%[6]s, %[1]s.values(%[3]s))\n"+
		"}\n\n", s, p.name, row, model, e.q(s), key)

	sc = e.f.Scope()
	s, cursor, row, err := sc.Name("s"), sc.Name("cursor"), sc.Name("row"), sc.Name("err")
	e.printf("// AfterCursor is After the row %s holds, as Cursor made it in the same order. An empty cursor is the "+
		"start.\n", cursor)
	e.printf("func (%[1]s %[2]s) AfterCursor(%[3]s string) (%[2]s, error) {\nif %[3]s == \"\" {\nreturn %[1]s, nil\n}\n",
		s, p.name, cursor)
	e.printf("var %s %s\nif %s := %s.DecodeCursor(%s, %s, map[string]any{\n", row, model, err, e.q(s), cursor, key)
	for _, c := range p.columns {
		if !c.col.NotNull {
			continue
		}
		dest := "&" + row + "." + strings.Join(c.path, ".")
		if uuidOf(c.typ) {
			dest = "(*" + e.f.Import("uuid", "uuid") + ".UUID)(" + dest + ")"
		}
		e.printf("%s: %s,\n", goString(sqlName(e.d, c.col.Name)), dest)
	}
	e.printf("}); %[1]s != nil {\nreturn %[2]s{}, %[1]s\n}\nreturn %[3]s.After(%[4]s), nil\n}\n\n", err, p.name, s, row)
}

// uuidOf reports whether a cursor holds a value of type t as a uuid.UUID, converted to one so that it encodes as text,
// as a uuid.UUID does and the type declared of [16]byte does not.
func uuidOf(t types.Type) bool {
	return isUUID(t) && !isStdUUID(t)
}

// method writes a method of the scope type, as fn does, with doc after its name as its doc.
func (e emitter) method(doc, s, name, params, expr string) {
	e.printf("// %s %s\n", name, doc)
	e.fn(s, name, params, expr)
}

// fn writes a method of the scope type, on the receiver s, named name and taking params, which returns the scope made
// of the scope.Of expr returns.
func (e emitter) fn(s, name, params, expr string) {
	e.printf("func (%[1]s %[2]s) %[3]s(%[4]s) %[2]s {\nreturn %[2]s{%[6]s(%[5]s)}\n}\n\n", s, e.p.name, name, params,
		expr, e.toScope)
}

// q is the scope of the receiver s, as the runtime reads it.
func (e emitter) q(s string) string {
	return e.toRuntime + "(" + s + "." + e.p.field + ")"
}

// where is the scope.Of of the receiver s narrowed by the condition parts make.
func (e emitter) where(s string, parts ...string) string {
	return e.q(s) + ".Where(" + strings.Join(parts, ", ") + ")"
}

// column writes the methods of the column c, named after its field.
func (e emitter) column(c column) {
	name := c.path[len(c.path)-1]
	col := c.col.Name
	sql := sqlName(e.d, col)
	t := c.typ
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	typ := types.TypeString(t, e.f.Qualifier)
	k := kindOf(t)

	sc := e.f.Scope()
	s, v := sc.Name("s"), sc.Name("v")
	e.method(fmt.Sprintf("narrows to the rows whose %s is %s.", col, v), s, name+"Eq", v+" "+typ,
		e.where(s, goString(sql+" = "), v))
	nulls := ""
	if !c.col.NotNull {
		nulls = ", leaving out those whose " + col + " is NULL"
	}
	e.method(fmt.Sprintf("narrows to the rows whose %s is not %s%s.", col, v, nulls), s, name+"Ne", v+" "+typ,
		e.where(s, goString(sql+" <> "), v))

	if !k.array {
		sc = e.f.Scope()
		s, vs := sc.Name("s"), sc.Name("vs")
		in, notIn := e.where(s, goString(sql+" = ANY("), vs, goString(")")),
			e.where(s, goString(sql+" <> ALL("), vs, goString(")"))
		// In of no values is the empty set, which matches no row, so that Not of it matches every row: PostgreSQL
		// is given an empty array, as a nil slice would go as NULL, and MySQL a subquery of no rows.
		none := fmt.Sprintf("if %[1]s == nil {\n%[1]s = []%[2]s{}\n}\n", vs, typ)
		if e.d == snapshot.MySQL {
			list := e.ref("InList") + "(" + vs + ")"
			in, notIn = e.where(s, goString(sql+" IN ("), list, goString(")")),
				e.where(s, goString(sql+" NOT IN ("), list, goString(")"))
			none = ""
		}
		e.printf("// %sIn narrows to the rows whose %s is one of %s.\n", name, col, vs)
		e.printf("func (%[1]s %[2]s) %[3]sIn(%[4]s ...%[5]s) %[2]s {\n%[6]sreturn %[2]s{%[8]s(%[7]s)}\n}\n\n", s,
			e.p.name, name, vs, typ, none, in, e.toScope)
		notInNulls := ""
		if !c.col.NotNull {
			notInNulls = nulls + " unless " + vs + " is empty"
		}
		e.printf("// %sNotIn narrows to the rows whose %s is none of %s%s.\n", name, col, vs, notInNulls)
		e.printf("func (%[1]s %[2]s) %[3]sNotIn(%[4]s ...%[5]s) %[2]s {\nif len(%[4]s) == 0 {\nreturn %[1]s\n}\n"+
			"return %[2]s{%[7]s(%[6]s)}\n}\n\n", s, e.p.name, name, vs, typ, notIn, e.toScope)
	}

	if k.ordered {
		sc = e.f.Scope()
		s, v := sc.Name("s"), sc.Name("v")
		for _, m := range []struct{ suffix, op, doc string }{
			{"Lt", "<", "less than"}, {"Lte", "<=", "at most"}, {"Gt", ">", "greater than"}, {"Gte", ">=", "at least"},
		} {
			e.method(fmt.Sprintf("narrows to the rows whose %s is %s %s.", col, m.doc, v), s, name+m.suffix,
				v+" "+typ, e.where(s, goString(sql+" "+m.op+" "), v))
		}
		sc = e.f.Scope()
		s, lo, hi := sc.Name("s"), sc.Name("lo"), sc.Name("hi")
		e.method(fmt.Sprintf("narrows to the rows whose %s is between %s and %s, both included.", col, lo, hi), s,
			name+"Between", lo+", "+hi+" "+typ, e.where(s, goString(sql+" BETWEEN "), lo, goString(" AND "), hi))
	}

	if k.text {
		sc = e.f.Scope()
		s, pattern := sc.Name("s"), sc.Name("pattern")
		e.method(fmt.Sprintf("narrows to the rows whose %s matches %s, as LIKE does.", col, pattern), s,
			name+"Like", pattern+" string", e.where(s, goString(sql+" LIKE "), pattern))
		if e.d == snapshot.PostgreSQL {
			e.method(fmt.Sprintf("narrows to the rows whose %s matches %s, as ILIKE does, ignoring case.", col,
				pattern), s, name+"ILike", pattern+" string", e.where(s, goString(sql+" ILIKE "), pattern))
		}
		escape := goString(" ESCAPE '!'")
		sc = e.f.Scope()
		s, prefix := sc.Name("s"), sc.Name("prefix")
		e.method(fmt.Sprintf("narrows to the rows whose %s starts with %s.", col, prefix), s, name+"HasPrefix",
			prefix+" string", e.where(s, goString(sql+" LIKE "), e.ref("EscapeLike")+"("+prefix+`)+"%"`, escape))
		sc = e.f.Scope()
		s, substr := sc.Name("s"), sc.Name("substr")
		e.method(fmt.Sprintf("narrows to the rows whose %s contains %s.", col, substr), s, name+"Contains",
			substr+" string", e.where(s, goString(sql+" LIKE "), `"%"+`+e.ref("EscapeLike")+"("+substr+`)+"%"`, escape))
	}

	sc = e.f.Scope()
	s = sc.Name("s")
	if !c.col.NotNull {
		e.method(fmt.Sprintf("narrows to the rows whose %s is NULL.", col), s, name+"IsNull", "",
			e.where(s, goString(sql+" IS NULL")))
		e.method(fmt.Sprintf("narrows to the rows whose %s is not NULL.", col), s, name+"IsNotNull", "",
			e.where(s, goString(sql+" IS NOT NULL")))
	}
	asc, desc := "in ascending order", "in descending order"
	switch {
	case k.time:
		asc, desc = "earliest first", "latest first"
	case k.number:
		asc, desc = "smallest first", "largest first"
	}
	e.method(fmt.Sprintf("orders the rows by %s, %s.", col, asc), s, name+"Asc", "",
		e.q(s)+".OrderBy("+goString(sql)+", false)")
	e.method(fmt.Sprintf("orders the rows by %s, %s.", col, desc), s, name+"Desc", "",
		e.q(s)+".OrderBy("+goString(sql)+", true)")
}

// kind is what a column's methods can do with it, by the type of the field holding it.
type kind struct {
	// ordered is a number, a string, or a time; text a string; number and time the others.
	ordered, text, number, time bool
	// array is a slice other than []byte, which holds an array.
	array bool
}

// kindOf tells the kind of a column held by a t, which is not a pointer. A value type is of the kind of the value it
// holds.
func kindOf(t types.Type) kind {
	if s, ok := t.Underlying().(*types.Slice); ok {
		b, ok := s.Elem().Underlying().(*types.Basic)
		return kind{array: !ok || b.Kind() != types.Byte}
	}
	if v, ok := valuetype.Of(t); ok {
		t = v.Held
	}
	if isTime(t) {
		return kind{ordered: true, time: true}
	}
	b, ok := t.Underlying().(*types.Basic)
	switch {
	case ok && b.Info()&types.IsString != 0:
		return kind{ordered: true, text: true}
	case ok && b.Info()&types.IsNumeric != 0:
		return kind{ordered: true, number: true}
	}
	return kind{}
}

// goString writes s as a Go string literal: a raw one, as SQL reads best, unless s holds a backquote.
func goString(s string) string {
	if strings.Contains(s, "`") {
		return strconv.Quote(s)
	}
	return "`" + s + "`"
}

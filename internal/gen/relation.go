package gen

import (
	"fmt"
	"go/token"
	"go/types"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/packages"

	"github.com/go-kanna/orm/internal/decl"
	"github.com/go-kanna/orm/internal/snapshot"
)

type relationKind int

const (
	hasMany relationKind = iota
	hasOne
	belongsTo
	manyToMany
)

func (k relationKind) String() string {
	switch k {
	case hasMany:
		return "has many"
	case hasOne:
		return "has one"
	case belongsTo:
		return "belongs to"
	case manyToMany:
		return "many to many"
	}
	return "relationKind(" + strconv.Itoa(int(k)) + ")"
}

// link is a relation, with the foreign key it follows found.
type link struct {
	field relation
	way   way
	// target is the scope type of the models the relation reaches, and model their model.
	target indexed
	model  *types.Named
	// keys are the fields of the model holding the columns the relation joins by, and targetKeys those of the related
	// model: the foreign key's columns and the ones it references, in the order of the key.
	keys, targetKeys []column
	scalar           *column
}

// way is a way a relation can be read: by a foreign key, and for manyToMany, through a table.
type way struct {
	kind relationKind
	// fk is the foreign key followed, and table the table it is of: the model's own table, for belongsTo; the related
	// one, for hasOne and hasMany; and the one between, for manyToMany.
	fk    snapshot.ForeignKey
	table string
	// throughFK is, for manyToMany, the foreign key of the table between to the related one.
	throughFK snapshot.ForeignKey
}

// linker finds the foreign keys the relations of the models of a package's scope types follow.
type linker struct {
	fset *token.FileSet
	// pkgPath and pkgName are those of the package, and scopes its scope types.
	pkgPath, pkgName string
	scopes           []scope
	// loaded are the scope types of the other packages the run loads, which the index leaves out when they are
	// under testdata.
	loaded []indexed
	tables map[string]snapshot.Table
	idx    []indexed
	// driver is the one the repositories run on, empty in a run that generates none.
	driver driver
}

// link finds the foreign key each relation of f, the fields of the model of sc, follows, and the fields holding the
// keys it joins by: columns of f, and those of the related model.
func (l linker) link(sc scope, f matched) ([]link, []diag.Diag) {
	var (
		links []link
		ds    []diag.Diag
	)
	for _, r := range f.relations {
		lk, rds, ok := l.linkOne(sc, f, r)
		ds = append(ds, rds...)
		if ok {
			links = append(links, lk)
		}
	}
	return links, ds
}

func (l linker) linkOne(sc scope, f matched, r relation) (link, []diag.Diag, bool) {
	if r.table != "" {
		return l.scalarLink(sc, f.columns, r)
	}
	field := r.path[len(r.path)-1]
	errorf := func(format string, args ...any) diag.Diag {
		label := sc.model.Obj().Name() + "." + strings.Join(r.path, ".")
		return diag.Errorf(r.pos, "%s: %s", label, fmt.Sprintf(format, args...))
	}
	model, many := relatedModel(r.typ)
	modelName := types.TypeString(model, (*types.Package).Name)

	target, ok := l.scopeOf(model)
	if !ok {
		if t := l.referenced(sc.table, field); t != "" {
			return link{}, []diag.Diag{errorf("no scope type is declared for %s", modelName).
				WithHints(fmt.Sprintf("declare one where %s are queried: //kanna:scope table=%[1]s", t))}, false
		}
		hint := "declare one where its rows are queried: //kanna:scope table=<table>"
		if t := decl.Plural(model.Obj().Name()); !l.has(t) {
			// A table added since the snapshot was taken implies no scope type of the model named after it yet.
			return link{}, []diag.Diag{errorf("no scope type is declared for %s, and the snapshot has no table %s, "+
				"which it is named after", modelName, t).WithHints(describeAgain, "or "+hint)}, false
		}
		return link{}, []diag.Diag{errorf("no scope type is declared for %s", modelName).WithHints(hint)}, false
	}
	related, ok := l.tables[target.table]
	switch {
	case !ok && target.pkgPath == l.pkgPath:
		return link{}, nil, false // findScopes reports it
	case !ok:
		return link{}, []diag.Diag{errorf("the snapshot has no table %s, of which %s.%s is a scope type",
			target.table, target.pkgName, target.name)}, false
	}

	n := naming{
		table: sc.table, model: sc.model.Obj().Name(), field: field, related: target.table,
		relatedModel: model.Obj().Name(), fk: r.fk,
	}
	ways := n.ways(l.tables, many)
	switch {
	case len(ways) == 0:
		return link{}, []diag.Diag{errorf("%s", n.missing(many)).WithHints(n.missingHints(l.tables, many)...)}, false
	case len(ways) > 1:
		msg, hints := n.ambiguity(l.tables, ways)
		return link{}, []diag.Diag{errorf("%s", msg).WithHints(hints...)}, false
	}

	w := ways[0]
	own, other := w.keys()
	keys, missing := held(f.columns, own)
	switch {
	case slices.Contains(f.failed, missing):
		return link{}, nil, false // fields reports the field holding it
	case missing != "":
		return link{}, []diag.Diag{errorf("no field of %s holds %s.%s, which %s is loaded by",
			sc.model.Obj().Name(), sc.table, missing, field)}, false
	}
	targetFields, tds := fields(l.fset, scope{name: target.name, model: model, table: target.table}, related)
	targetKeys, missing := held(targetFields.columns, other)
	switch {
	case slices.Contains(targetFields.failed, missing) && target.pkgPath == l.pkgPath:
		return link{}, nil, false // fields reports the field holding it, of a scope type of the package
	case slices.Contains(targetFields.failed, missing):
		return link{}, mistakes(tds), false
	case missing != "":
		return link{}, []diag.Diag{errorf("no field of %s holds %s.%s, which %s is loaded by",
			modelName, target.table, missing, field)}, false
	}
	return link{field: r, way: w, target: target, model: model, keys: keys, targetKeys: targetKeys}, nil, true
}

// relatedModel returns the model a relation of type t reaches, and whether it reaches a slice of them.
func relatedModel(t types.Type) (*types.Named, bool) {
	many := false
	if s, ok := t.Underlying().(*types.Slice); ok {
		t, many = s.Elem(), true
	}
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	return types.Unalias(t).(*types.Named), many //nolint:forcetypeassert // fields takes nothing else for a relation
}

// scopeOf finds the scope type of model among those of the package first, as their types tell them, then among those
// of the other packages loaded, and then in the index.
func (l linker) scopeOf(model *types.Named) (indexed, bool) {
	for _, sc := range l.scopes {
		if types.Identical(sc.model, model) {
			return indexed{
				pkgPath: l.pkgPath, pkgName: l.pkgName, name: sc.name,
				modelPath: model.Obj().Pkg().Path(), modelName: model.Obj().Name(), table: sc.table, pos: sc.pos,
			}, true
		}
	}
	for _, e := range slices.Concat(l.loaded, l.idx) {
		if e.modelPath == model.Obj().Pkg().Path() && e.modelName == model.Obj().Name() {
			return e, true
		}
	}
	return indexed{}, false
}

func (l linker) has(table string) bool {
	_, ok := l.tables[table]
	return ok
}

// loadedScopes returns the scope types of pkgs as the index has them, for a linker to look them up in.
func loadedScopes(pkgs []*packages.Package, s snapshot.Snapshot, idx []indexed) []indexed {
	var all []indexed
	for _, pkg := range pkgs {
		scopes, _ := findScopes(pkg, s, idx) // plan reports what is wrong with them
		for _, sc := range scopes {
			all = append(all, indexed{
				pkgPath: pkg.PkgPath, pkgName: pkg.Name, name: sc.name,
				modelPath: sc.model.Obj().Pkg().Path(), modelName: sc.model.Obj().Name(), table: sc.table, pos: sc.pos,
			})
		}
	}
	return all
}

// referenced guesses the table a field of table, named field, reaches: the one its foreign key of the field's name
// and _id references, if it has one.
func (l linker) referenced(table, field string) string {
	for _, fk := range l.tables[table].ForeignKeys {
		if slices.Equal(fk.Columns, []string{columnName(field) + "_id"}) {
			return fk.References
		}
	}
	return ""
}

// naming is what the names of a relation, a field of a model, say of the foreign key it follows.
type naming struct {
	// table and model are those of the field, and related and relatedModel those it reaches.
	table, model, field, related, relatedModel string
	// fk are the columns the field's tag names, if it names any.
	fk []string
}

// columns are those of fk, or else the one named for name and _id.
func (n naming) columns(name string) []string {
	if n.fk != nil {
		return n.fk
	}
	return []string{columnName(name) + "_id"}
}

// through is the table a many to many goes through: <model>_<field>, as order_tags for Order.Tags.
func (n naming) through() string {
	return columnName(n.model) + "_" + columnName(n.field)
}

// ways finds the ways the relation can be read, as its names say. A pointer is a belongs to, by the foreign key of
// <field>_id of the model's table, or a has one, by a unique one of <model>_id of the related table. A slice is a has
// many, by the foreign key of <model>_id of the related table, or a many to many, through <model>_<field> by its
// foreign keys of <model>_id and <related model>_id. The columns a tag names stand for those of a belongs to, a has
// one, or a has many, and leave out a many to many, as does a model related to itself, whose two columns would be one.
func (n naming) ways(tables map[string]snapshot.Table, many bool) []way {
	var ways []way
	if !many {
		for _, fk := range foreignKeys(tables[n.table], n.columns(n.field), n.related) {
			ways = append(ways, way{kind: belongsTo, fk: fk, table: n.table})
		}
		for _, fk := range foreignKeys(tables[n.related], n.columns(n.model), n.table) {
			if unique(tables[n.related], fk.Columns) {
				ways = append(ways, way{kind: hasOne, fk: fk, table: n.related})
			}
		}
		return ways
	}
	for _, fk := range foreignKeys(tables[n.related], n.columns(n.model), n.table) {
		ways = append(ways, way{kind: hasMany, fk: fk, table: n.related})
	}
	own, other := columnName(n.model)+"_id", columnName(n.relatedModel)+"_id"
	if n.fk != nil || own == other {
		return ways
	}
	through := n.through()
	for _, fk := range foreignKeys(tables[through], []string{own}, n.table) {
		for _, tfk := range foreignKeys(tables[through], []string{other}, n.related) {
			ways = append(ways, way{kind: manyToMany, fk: fk, table: through, throughFK: tfk})
		}
	}
	return ways
}

// foreignKeys are the foreign keys of t of columns, in any order, that refer to the table references.
func foreignKeys(t snapshot.Table, columns []string, references string) []snapshot.ForeignKey {
	var fks []snapshot.ForeignKey
	for _, fk := range t.ForeignKeys {
		if fk.References == references && sameColumns(fk.Columns, columns) {
			fks = append(fks, fk)
		}
	}
	return fks
}

// missing says what the names of the relation look for, when the snapshot has none of it.
func (n naming) missing(many bool) string {
	switch {
	case !many:
		return fmt.Sprintf("no foreign key of %s to %s reads it, nor a unique one of %s to %s",
			qualifiedColumns(n.table, n.columns(n.field)), n.related, qualifiedColumns(n.related, n.columns(n.model)),
			n.table)
	case n.fk != nil || columnName(n.model) == columnName(n.relatedModel):
		return fmt.Sprintf("no foreign key of %s to %s reads it", qualifiedColumns(n.related, n.columns(n.model)),
			n.table)
	}
	return fmt.Sprintf("no foreign key of %s to %s reads it, nor a table between, %s, with foreign keys of %s_id to %s "+
		"and of %s_id to %s", qualifiedColumns(n.related, n.columns(n.model)), n.table, n.through(),
		columnName(n.model), n.table, columnName(n.relatedModel), n.related)
}

// missingHints say how the relation would be read, when its names read it by none: tagged with the columns of a
// foreign key that the snapshot has between the two tables, or declared the other way, a pointer or a slice, if either
// would read it by one way alone. Of a slice, they say how a table between is read, when its names are not those a many
// to many takes.
func (n naming) missingHints(tables map[string]snapshot.Table, many bool) []string {
	tags := [][]string{nil}
	for _, t := range []string{n.table, n.related} {
		for _, fk := range tables[t].ForeignKeys {
			if (fk.References == n.table || fk.References == n.related) && !slices.Equal(fk.Columns, n.fk) &&
				!slices.ContainsFunc(tags, func(tag []string) bool { return slices.Equal(tag, fk.Columns) }) {
				tags = append(tags, fk.Columns)
			}
		}
	}
	var (
		hints  []string
		hinted []way
	)
	for _, tag := range tags {
		tagged := n
		if tag != nil {
			tagged.fk = tag
		}
		tagging := ""
		if tagged.fk != nil {
			tagging = fmt.Sprintf("orm:%q", "fk="+strings.Join(tagged.fk, ","))
		}
		if ways := tagged.ways(tables, many); len(ways) == 1 && tag != nil {
			hints, hinted = append(hints, fmt.Sprintf("%s reads it: tag the field %s", ways[0], tagging)),
				append(hinted, ways[0])
		} else if ways := tagged.ways(tables, !many); len(ways) == 1 && !slices.ContainsFunc(hinted, ways[0].same) {
			hint := shapeHint(ways[0], !many, n.related)
			if tagging != "" {
				hint += ", tagged " + tagging
			}
			hints, hinted = append(hints, hint), append(hinted, ways[0])
		}
	}
	if many && n.fk == nil {
		if columnName(n.model) == columnName(n.relatedModel) {
			hints = append(hints, fmt.Sprintf("a table between rows of %s, whose two columns would both be %s_id, is "+
				"read as a model of its own, by a has many and a belongs to, or by SQL written by hand", n.table,
				columnName(n.model)))
		} else if between := tablesBetween(tables, n.table, n.related); len(between) > 0 {
			hints = append(hints, fmt.Sprintf("a table between, as %s, whose names are not %s, %s_id, and %s_id, is "+
				"read as a model of its own, by a has many and a belongs to, or by SQL written by hand",
				strings.Join(between, " or "), n.through(), columnName(n.model), columnName(n.relatedModel)))
		}
	}
	return hints
}

// shapeHint says that w would read the relation, to rows of related, were it declared as a slice, if many, or else as
// a pointer.
func shapeHint(w way, many bool, related string) string {
	if many {
		return fmt.Sprintf("%s relates many rows of %s to each: declare the field as a slice", w, related)
	}
	return fmt.Sprintf("%s relates one row of %s to each: declare the field as a pointer", w, related)
}

// tablesBetween are the tables but table and related with foreign keys to both.
func tablesBetween(tables map[string]snapshot.Table, table, related string) []string {
	var between []string
	for _, name := range slices.Sorted(maps.Keys(tables)) {
		refers := func(to string) bool {
			return slices.ContainsFunc(tables[name].ForeignKeys, func(fk snapshot.ForeignKey) bool {
				return fk.References == to
			})
		}
		if name != table && name != related && refers(table) && refers(related) {
			between = append(between, name)
		}
	}
	return between
}

// ambiguity says that more than one of ways read the relation, and how the one meant is read: a has many by a tag
// naming its columns, and a many to many by its table between as a model of its own; anything else by SQL written by
// hand.
func (n naming) ambiguity(tables map[string]snapshot.Table, ways []way) (string, []string) {
	hand := "a relation its names cannot tell is read by SQL written by hand: a join returning a struct that embeds " +
		"the model, or a query of the related rows by a slice of keys, put together in Go"
	if !slices.ContainsFunc(ways, func(w way) bool { return w.kind != ways[0].kind || w.table != ways[0].table }) {
		return fmt.Sprintf("%s has %s foreign keys of %s to %s", ways[0].table, count(len(ways)),
			strings.Join(ways[0].fk.Columns, ", "), ways[0].fk.References), []string{hand}
	}
	described := make([]string, 0, len(ways))
	for _, w := range ways {
		if w.kind == manyToMany {
			described = append(described, "as a many to many, through "+w.table)
		} else {
			described = append(described, fmt.Sprintf("as a %s, by %s", w.kind, w))
		}
	}
	msg := "its names read it both " + strings.Join(described, ", and ")
	if len(ways) > 2 {
		msg = fmt.Sprintf("its names read it in %s ways: %s", count(len(ways)), strings.Join(described, "; "))
	}
	var hints []string
	for _, w := range ways {
		switch w.kind {
		case hasMany:
			tagged := n
			tagged.fk = w.fk.Columns
			if len(tagged.ways(tables, true)) == 1 {
				hints = append(hints, fmt.Sprintf("for the has many, tag the field orm:%q",
					"fk="+strings.Join(w.fk.Columns, ",")))
			}
		case manyToMany:
			hints = append(hints, fmt.Sprintf("for the many to many, read %s as a model of its own, by a has many and "+
				"a belongs to", w.table))
		case belongsTo, hasOne:
		}
	}
	if len(hints) == 0 {
		hints = append(hints, hand)
	}
	return msg, hints
}

// unique reports whether columns are the primary key of t, or one of its unique keys.
func unique(t snapshot.Table, columns []string) bool {
	sorted := slices.Sorted(slices.Values(columns))
	for _, key := range append([][]string{t.PrimaryKey}, t.UniqueKeys...) {
		if slices.Equal(slices.Sorted(slices.Values(key)), sorted) {
			return true
		}
	}
	return false
}

// String says what w follows.
func (w way) String() string {
	s := qualifiedColumns(w.table, w.fk.Columns) + " to " + w.fk.References
	if w.kind == manyToMany {
		s += ", and " + qualifiedColumns(w.table, w.throughFK.Columns) + " to " + w.throughFK.References
	}
	return s
}

// same reports whether w and o follow the same foreign keys.
func (w way) same(o way) bool {
	return w.kind == o.kind && w.table == o.table && slices.Equal(w.fk.Columns, o.fk.Columns) &&
		w.fk.References == o.fk.References && slices.Equal(w.throughFK.Columns, o.throughFK.Columns)
}

func qualifiedColumns(table string, columns []string) string {
	if len(columns) == 1 {
		return table + "." + columns[0]
	}
	return table + " (" + strings.Join(columns, ", ") + ")"
}

// keys names the columns of the model's table, and of the related table, that w joins by.
func (w way) keys() (own, related []string) {
	switch w.kind {
	case belongsTo:
		return w.fk.Columns, w.fk.ReferencedColumns
	case manyToMany:
		return w.fk.ReferencedColumns, w.throughFK.ReferencedColumns
	case hasMany, hasOne:
		return w.fk.ReferencedColumns, w.fk.Columns
	}
	panic("gen: " + w.kind.String())
}

// held returns the fields among columns that hold names, in order, or the first of names none holds.
func held(columns []column, names []string) ([]column, string) {
	keys := make([]column, 0, len(names))
	for _, name := range names {
		i := slices.IndexFunc(columns, func(c column) bool { return c.col.Name == name })
		if i < 0 {
			return nil, name
		}
		keys = append(keys, columns[i])
	}
	return keys, ""
}

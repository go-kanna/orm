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
}

// link finds the foreign key each of relations, fields of the model of sc, follows, and the fields holding the keys
// it joins by: columns, of the model of sc, and those of the related model.
func (l linker) link(sc scope, columns []column, relations []relation) ([]link, []diag.Diag) {
	var (
		links []link
		ds    []diag.Diag
	)
	for _, r := range relations {
		lk, rds, ok := l.linkOne(sc, columns, r)
		ds = append(ds, rds...)
		if ok {
			links = append(links, lk)
		}
	}
	return links, ds
}

func (l linker) linkOne(sc scope, columns []column, r relation) (link, []diag.Diag, bool) {
	field := r.path[len(r.path)-1]
	errorf := func(format string, args ...any) diag.Diag {
		label := sc.model.Obj().Name() + "." + strings.Join(r.path, ".")
		return diag.Errorf(r.pos, "%s: %s", label, fmt.Sprintf(format, args...))
	}
	model, many := relatedModel(r.typ)
	modelName := types.TypeString(model, (*types.Package).Name)

	target, ok := l.scopeOf(model)
	if !ok {
		hint := "declare one where its rows are queried: //kanna:scope table=<table>"
		if t := l.referenced(sc.table, field); t != "" {
			hint = fmt.Sprintf("declare one where %s are queried: //kanna:scope table=%[1]s", t)
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

	ways := l.ways(sc.table, target.table, many)
	if r.fk != "" {
		tagged := slices.DeleteFunc(slices.Clone(ways), func(w way) bool { return !w.follows(r.fk) })
		if len(tagged) == 0 {
			d := errorf("orm:%q names no foreign key that relates %s and %s", "fk="+r.fk, sc.table, target.table)
			for _, w := range ways {
				d = d.WithHints("one that does: " + w.String())
			}
			return link{}, []diag.Diag{d}, false
		}
		ways = tagged
	}

	switch {
	case len(ways) == 0:
		return link{}, []diag.Diag{errorf("no foreign key relates %s and %s", sc.table, target.table).
			WithHints(l.noWayHint(sc.table, target.table, many))}, false
	case len(ways) > 1:
		d := errorf("%s", ambiguity(sc.table, target.table, ways))
		for _, w := range ways {
			d = d.WithHints(w.String())
		}
		return link{}, []diag.Diag{d.WithHints(fmt.Sprintf(`tag the field with the one to follow: orm:"fk=%s"`,
			tagFor(ways, field)))}, false
	}

	w := ways[0]
	own, other := w.keys()
	keys, missing := held(columns, own)
	if missing != "" {
		return link{}, []diag.Diag{errorf("no field of %s holds %s.%s, which %s is loaded by",
			sc.model.Obj().Name(), sc.table, missing, field)}, false
	}
	targetFields, _ := fields(l.fset, scope{name: target.name, model: model, table: target.table}, related)
	targetKeys, missing := held(targetFields.columns, other)
	if missing != "" {
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

// loadedScopes returns the scope types of pkgs as the index has them, for a linker to look them up in.
func loadedScopes(pkgs []*packages.Package, s snapshot.Snapshot) []indexed {
	var all []indexed
	for _, pkg := range pkgs {
		scopes, _ := findScopes(pkg, s) // plan reports what is wrong with them
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

// ways finds the ways a relation of table to related can be read: by a slice when many, and by a pointer otherwise.
func (l linker) ways(table, related string, many bool) []way {
	var ways []way
	if !many {
		for _, fk := range l.tables[table].ForeignKeys {
			if fk.References == related {
				ways = append(ways, way{kind: belongsTo, fk: fk, table: table})
			}
		}
	}
	for _, fk := range l.tables[related].ForeignKeys {
		if fk.References != table {
			continue
		}
		if many {
			ways = append(ways, way{kind: hasMany, fk: fk, table: related})
		} else if unique(l.tables[related], fk.Columns) {
			ways = append(ways, way{kind: hasOne, fk: fk, table: related})
		}
	}
	if !many {
		return ways
	}
	for _, through := range slices.Sorted(maps.Keys(l.tables)) {
		if through == table || through == related {
			continue
		}
		fks := l.tables[through].ForeignKeys
		for i, fk := range fks {
			for j, tfk := range fks {
				if i != j && fk.References == table && tfk.References == related {
					ways = append(ways, way{kind: manyToMany, fk: fk, table: through, throughFK: tfk})
				}
			}
		}
	}
	return ways
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

// noWayHint says how a relation of table to related would be read, when no foreign key reads it, and which it would
// be read by were the field declared otherwise.
func (l linker) noWayHint(table, related string, many bool) string {
	if other := l.ways(table, related, !many); len(other) > 0 {
		if many {
			return fmt.Sprintf("%s relates one row of %s to each: declare the field as a pointer",
				other[0].String(), related)
		}
		return fmt.Sprintf("%s relates many rows of %s to each: declare the field as a slice",
			other[0].String(), related)
	}
	if many {
		return fmt.Sprintf("a slice is loaded by a foreign key of %s to %s, or through a table with foreign keys to both",
			related, table)
	}
	return fmt.Sprintf("a pointer is loaded by a foreign key of %s to %s, or by a unique one of %s to %s",
		table, related, related, table)
}

// ambiguity says that more than one of ways could read a relation of table to related.
func ambiguity(table, related string, ways []way) string {
	w := ways[0]
	if w.kind != manyToMany && !slices.ContainsFunc(ways, func(o way) bool { return o.kind != w.kind }) {
		cols := make([]string, 0, len(ways))
		for _, o := range ways {
			cols = append(cols, strings.Join(o.fk.Columns, " and "))
		}
		return fmt.Sprintf("%s has %s foreign keys to %s (%s)", w.table, count(len(ways)), w.fk.References,
			strings.Join(cols, ", "))
	}
	return fmt.Sprintf("%s foreign keys could relate %s and %s", count(len(ways)), table, related)
}

func count(n int) string {
	if words := []string{"two", "three", "four", "five"}; n >= 2 && n-2 < len(words) {
		return words[n-2]
	}
	return strconv.Itoa(n)
}

// tagFor says what a tag for a relation, named field, picks one of ways by: the column of a foreign key named for the
// field, if one is, and otherwise one of the first way's that no other way's has. The column comes after its table when
// another way's foreign key has it too.
func tagFor(ways []way, field string) string {
	col := columnName(field) + "_id"
	i := slices.IndexFunc(ways, func(w way) bool { return slices.Contains(w.fk.Columns, col) })
	if i < 0 {
		i, col = 0, ways[0].fk.Columns[0]
		if j := slices.IndexFunc(ways[0].fk.Columns, func(c string) bool { return !anyHas(ways[1:], c) }); j >= 0 {
			col = ways[0].fk.Columns[j]
		}
	}
	if anyHas(slices.Delete(slices.Clone(ways), i, i+1), col) {
		return ways[i].table + "." + col
	}
	return col
}

// anyHas reports whether the foreign key of any of ways has the column col.
func anyHas(ways []way, col string) bool {
	return slices.ContainsFunc(ways, func(w way) bool { return slices.Contains(w.fk.Columns, col) })
}

// String says what w follows.
func (w way) String() string {
	s := qualifiedColumns(w.table, w.fk.Columns) + " to " + w.fk.References
	if w.kind == manyToMany {
		s += ", and " + qualifiedColumns(w.table, w.throughFK.Columns) + " to " + w.throughFK.References
	}
	return s
}

// follows reports whether w follows the foreign key a tag names by fk: a column it has, or its table and the column,
// as in order_lines.order_id.
func (w way) follows(fk string) bool {
	if i := strings.LastIndex(fk, "."); i >= 0 {
		return fk[:i] == w.table && slices.Contains(w.fk.Columns, fk[i+1:])
	}
	return slices.Contains(w.fk.Columns, fk)
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

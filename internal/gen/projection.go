package gen

import (
	"fmt"
	"go/token"
	"go/types"
	"reflect"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"

	"github.com/go-kanna/orm/internal/snapshot"
)

// readProjection is what a method reading a projection selects: the columns its fields hold of the scope's table, and
// the tables its nested structs join, or, of a projection of aggregates, the columns it groups the rows by and the
// aggregates of each group.
type readProjection struct {
	model      *types.Named
	columns    []column
	joins      []projectionJoin
	aggregates []aggregate
}

// projectionJoin is a table a projection joins, by the foreign key of the scope's table that a nested struct follows:
// an INNER JOIN of a struct, and a LEFT JOIN of a pointer to one, nil when no row joins.
type projectionJoin struct {
	// field is the nested field, and alias the name the table goes by, the field's in snake case.
	field, alias string
	// typ is the struct the field holds, through a pointer if left.
	typ  types.Type
	left bool
	fk   snapshot.ForeignKey
	// columns are those the struct's fields hold, their paths from the projection.
	columns []column
}

// projectionField is a field of a projection, or of a struct nested in it.
type projectionField struct {
	f    *types.Var
	tag  fieldTag
	pos  token.Position
	path []string
	// written is the orm key of its tag, as it is written.
	written string
}

// projectionFields matches the fields of the projection of m to the columns they hold: a field of the model of the
// scope's table by its name, or by the column its tag names; a nested struct to a table joined by the foreign key of
// <field>_id, or of the columns its tag names, whose fields are matched to the fields of that table's model alike.
func (r repoReader) projectionFields(m *repoMethod) []diag.Diag {
	var ds []diag.Diag
	errorf := func(pos token.Position, format string, args ...any) {
		ds = append(ds, diag.Errorf(pos, "%s: %s", m.name, fmt.Sprintf(format, args...)))
	}
	p := m.projection
	aliases := map[string]string{m.target.table: "the table " + m.target.table}
	selected := map[string]bool{}
	for _, pf := range r.projectionStructFields(p.model, nil, errorf) {
		f := pf.f
		if a, ok, ads := projectionAggregate(m, pf); ok {
			ds = append(ds, ads...)
			p.aggregates = append(p.aggregates, a)
			continue
		}
		if !isNested(f.Type()) {
			c, ok := projectionColumn(pf, m.columns, m.target.table, selected, errorf)
			if ok {
				p.columns = append(p.columns, c)
			}
			continue
		}
		if model := asNamed(elemOf(f.Type())); model != nil {
			if _, ok := r.l.scopeOf(model); ok {
				ds = append(ds, diag.Errorf(pf.pos, "%s: %s is a model, whose rows a relation reads, and a projection "+
					"reads none", m.name, f.Name()).WithHints("join its table by a struct of the columns to read, as "+
					"struct{ Name string }"))
				continue
			}
		}
		alias := columnName(f.Name())
		if held, ok := aliases[alias]; ok {
			errorf(pf.pos, "%s joins its table as %s, as which %s is read already", f.Name(), alias, held)
			continue
		}
		aliases[alias] = f.Name()
		switch {
		case m.verb == verbDelete:
			errorf(pf.pos, "a Delete reading a projection joins no table, as %s would", f.Name())
			continue
		case pf.tag.column != "" || pf.tag.nullzero:
			errorf(pf.pos, "orm:%q names a column, and %s holds the columns of a table joined", pf.written, f.Name())
			continue
		}
		if j, jds := r.projectionJoin(m, pf, alias); jds != nil {
			ds = append(ds, jds...)
		} else {
			p.joins = append(p.joins, j)
		}
	}
	switch {
	case len(p.aggregates) > 0 && m.verb != verbList:
		errorf(m.pos, "a projection of aggregates is read by a List, which returns those of each group")
	case len(p.aggregates) > 0 && len(p.joins) > 0:
		errorf(m.pos, "%s joins a table, and a projection of aggregates joins none", p.joins[0].field)
	case len(p.aggregates) > 0 && len(p.columns) == 0:
		ds = append(ds, diag.Errorf(m.pos, "%s: a projection of aggregates groups the rows by its other fields, and "+
			"%s has none", m.name, typeName(p.model)).WithHints("give it a field to group the rows by",
			"or read an aggregate of all the rows by its verb, as in "+r.shape(verbSum, m.model)))
	case len(p.columns) == 0 && len(p.joins) == 0 && len(p.aggregates) == 0:
		errorf(m.pos, "a projection must select at least one model column")
	}
	return ds
}

// projectionStructFields are the fields of st, at path, that hold columns or nested structs. Those it leaves out, an
// unexported or embedded one, and one a tag cannot be read of, it reports by errorf.
func (r repoReader) projectionStructFields(t types.Type, path []string,
	errorf func(token.Position, string, ...any),
) []projectionField {
	st, _ := t.Underlying().(*types.Struct)
	if st == nil {
		return nil
	}
	var fields []projectionField
	for i := range st.NumFields() {
		f := st.Field(i)
		if f.Name() == "_" {
			continue
		}
		pos := r.pkg.Fset.Position(f.Pos())
		tag, err := parseTag(st.Tag(i))
		switch {
		case err != nil:
			errorf(pos, "%s: %v", f.Name(), err)
			continue
		case tag.skip:
			continue
		case !f.Exported():
			errorf(pos, "%s: the code kanna-orm writes cannot reach an unexported field", f.Name())
			continue
		case f.Embedded():
			errorf(pos, "%s is embedded, and the fields of a projection are those of the models it reads", f.Name())
			continue
		case holdsModels(f.Type()) && !isNested(f.Type()), tag.relation != "":
			errorf(pos, "a projection cannot contain relations")
			continue
		case tag.fk != nil && !isNested(f.Type()):
			errorf(pos, "orm:%q is for a struct of a table joined, and %s holds a column",
				reflect.StructTag(st.Tag(i)).Get("orm"), f.Name())
			continue
		}
		fields = append(fields, projectionField{f: f, tag: tag, pos: pos, path: append(slices.Clip(path), f.Name()),
			written: reflect.StructTag(st.Tag(i)).Get("orm")})
	}
	return fields
}

// isNested reports whether a field of type t holds the columns of a table joined: t is a struct, or a pointer to one,
// that is no column.
func isNested(t types.Type) bool {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	return isStruct(t) && !scalar(t)
}

// projectionColumn matches pf to the field of model, columns of table, of its name, or holding the column its tag
// names, of the same type. It reports by errorf the field it matches none of, or a column selected already.
func projectionColumn(pf projectionField, columns []column, table string, selected map[string]bool,
	errorf func(token.Position, string, ...any),
) (column, bool) {
	var matches []column
	for _, c := range columns {
		if pf.tag.column != "" && c.col.Name == pf.tag.column || pf.tag.column == "" && c.path[len(c.path)-1] == pf.f.Name() {
			matches = append(matches, c)
		}
	}
	name := strings.Join(pf.path, ".")
	switch {
	case len(matches) == 0 && pf.tag.column != "":
		errorf(pf.pos, "no field of the model of %s holds a column %s, as %s names", table, pf.tag.column, name)
		return column{}, false
	case len(matches) == 0:
		errorf(pf.pos, "the model of %s has no field %s", table, pf.f.Name())
		return column{}, false
	case len(matches) > 1:
		errorf(pf.pos, "projection name %s matches multiple model columns", name)
		return column{}, false
	}
	source := matches[0]
	if !types.Identical(pf.f.Type(), source.typ) {
		errorf(pf.pos, "projection field %s has type %s; the model field has type %s", name, typeName(pf.f.Type()),
			typeName(source.typ))
		return column{}, false
	}
	identity := table + "." + source.col.Name
	if len(pf.path) > 1 {
		identity = pf.path[0] + "." + source.col.Name
	}
	if selected[identity] {
		errorf(pf.pos, "projection selects column %s more than once", source.col.Name)
		return column{}, false
	}
	selected[identity] = true
	return column{path: pf.path, typ: pf.f.Type(), col: source.col, pos: pf.pos,
		nullzero: pf.tag.nullzero || source.nullzero}, true
}

// projectionJoin reads pf, a nested struct of the projection of m, as the table it joins as alias: the one the foreign
// key of the scope's table of <field>_id, or of the columns its tag names, refers to. Its fields are matched to the
// fields of the model of the one scope type of that table.
func (r repoReader) projectionJoin(m *repoMethod, pf projectionField, alias string) (projectionJoin, []diag.Diag) {
	errorf := func(format string, args ...any) diag.Diag {
		return diag.Errorf(pf.pos, "%s: %s", m.name, fmt.Sprintf(format, args...))
	}
	columns := pf.tag.fk
	if columns == nil {
		columns = []string{alias + "_id"}
	}
	var fks []snapshot.ForeignKey
	for _, fk := range m.table.ForeignKeys {
		if sameColumns(fk.Columns, columns) {
			fks = append(fks, fk)
		}
	}
	switch {
	case len(fks) == 0:
		d := errorf("no foreign key of %s joins the table of %s", qualifiedColumns(m.target.table, columns), pf.f.Name())
		for _, fk := range m.table.ForeignKeys {
			if !slices.Equal(fk.Columns, columns) && len(slices.DeleteFunc(slices.Clone(m.table.ForeignKeys),
				func(o snapshot.ForeignKey) bool { return !sameColumns(o.Columns, fk.Columns) })) == 1 {
				d = d.WithHints(fmt.Sprintf("%s to %s joins it: tag the field orm:%q",
					qualifiedColumns(m.target.table, fk.Columns), fk.References, "fk="+strings.Join(fk.Columns, ",")))
			}
		}
		return projectionJoin{}, []diag.Diag{d}
	case len(fks) > 1:
		tables := make([]string, 0, len(fks))
		for _, fk := range fks {
			tables = append(tables, fk.References)
		}
		return projectionJoin{}, []diag.Diag{errorf("%s foreign keys of %s refer to %s", count(len(fks)),
			qualifiedColumns(m.target.table, columns), strings.Join(tables, " and ")).WithHints(
			"join the table meant by SQL written by hand")}
	}
	fk := fks[0]
	models := r.scopeModels(fk.References)
	switch {
	case len(models) == 0:
		return projectionJoin{}, []diag.Diag{errorf("no scope type is declared for %s, whose model the fields of %s "+
			"are matched to", fk.References, pf.f.Name()).WithHints(fmt.Sprintf(
			"declare one where %s are queried: //kanna:scope table=%[1]s", fk.References))}
	case len(models) > 1:
		names := make([]string, 0, len(models))
		for _, model := range models {
			names = append(names, typeName(model))
		}
		return projectionJoin{}, []diag.Diag{errorf("%s are all models of %s, whose fields those of %s would be "+
			"matched to", strings.Join(names, " and "), fk.References, pf.f.Name())}
	}
	matched, _ := fields(r.pkg.Fset, scope{model: models[0], table: fk.References}, r.s.Tables[fk.References])

	var ds []diag.Diag
	collect := func(pos token.Position, format string, args ...any) {
		ds = append(ds, diag.Errorf(pos, "%s: %s", m.name, fmt.Sprintf(format, args...)))
	}
	t := pf.f.Type()
	j := projectionJoin{field: pf.f.Name(), alias: alias, typ: t, fk: fk}
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t, j.left = p.Elem(), true
	}
	selected := map[string]bool{}
	for _, nested := range r.projectionStructFields(t, pf.path, collect) {
		if isNested(nested.f.Type()) {
			collect(nested.pos, "%s joins a table through %s, and a projection joins tables of its scope's alone",
				strings.Join(nested.path, "."), pf.f.Name())
			continue
		}
		if c, ok := projectionColumn(nested, matched.columns, fk.References, selected, collect); ok {
			j.columns = append(j.columns, c)
		}
	}
	if len(j.columns) == 0 && ds == nil {
		collect(pf.pos, "%s selects no column of %s", pf.f.Name(), fk.References)
	}
	return j, ds
}

// scopeModels are the models of the scope types of table: those of the package, and those of the others the run
// loads or the index has.
func (r repoReader) scopeModels(table string) []*types.Named {
	var models []*types.Named
	add := func(model *types.Named) {
		if !slices.ContainsFunc(models, func(m *types.Named) bool { return types.Identical(m, model) }) {
			models = append(models, model)
		}
	}
	for _, sc := range r.l.scopes {
		if sc.table == table {
			add(sc.model)
		}
	}
	for _, e := range slices.Concat(r.l.loaded, r.l.idx) {
		switch {
		case e.table != table:
		case e.model != nil:
			add(e.model)
		case r.imported[e.modelPath] != nil:
			if obj, ok := r.imported[e.modelPath].Scope().Lookup(e.modelName).(*types.TypeName); ok {
				if model := asNamed(obj.Type()); model != nil {
					add(model)
				}
			}
		}
	}
	return models
}

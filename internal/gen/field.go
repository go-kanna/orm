package gen

import (
	"fmt"
	"go/token"
	"go/types"
	"reflect"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/packages"
	"github.com/go-kanna/internal/suggest"

	"github.com/go-kanna/orm/internal/snapshot"
)

// column is a field of a model that holds a column of its table.
type column struct {
	// path is the field's name, after those of the structs it is embedded in.
	path          []string
	typ           types.Type
	col           snapshot.Column
	pos           token.Position
	nullzero      bool
	insertDefault bool
	insertOnly    bool
	audit         audit
	relationValue bool
}

// relation is a field of a model that holds other models: a relation, once the foreign key it follows is found.
type relation struct {
	path []string
	typ  types.Type
	// fk names the columns of the foreign key its tag names, if it names any.
	fk  []string
	pos token.Position
	// table is the table a scalar relation reads the values of, and value their column, when its tag names it.
	table, value string
}

// matched is what the fields of a model are.
type matched struct {
	columns   []column
	relations []relation
	// unchecked names the fields whose types do not type-check.
	unchecked []string
}

// fields matches the fields of the model of sc to the columns of t, its table. It reports a field that is neither a
// column nor a relation, one naming a column t does not have, a column held by two fields, and a column that may be
// NULL held by a field that cannot be nil. A field whose type does not type-check is neither, but unchecked: it
// reports packages.NotTypeChecked of it, as the type error behind it says more. It settles which columns are the
// times a row is created and updated at, as audits does, and then reports a default the database has none for.
func fields(fset *token.FileSet, sc scope, t snapshot.Table) (matched, []diag.Diag) {
	m := matcher{fset: fset, sc: sc, table: t, held: map[string]string{}}
	m.match(sc.model.Underlying().(*types.Struct), nil) //nolint:forcetypeassert // findScopes takes structs alone
	m.audits()
	m.defaults()
	return m.matched, m.ds
}

type matcher struct {
	matched

	fset  *token.FileSet
	sc    scope
	table snapshot.Table
	// held names the field holding each column matched so far.
	held map[string]string
	ds   []diag.Diag
}

// match matches the fields of st, which is embedded in the model through path.
func (m *matcher) match(st *types.Struct, path []string) {
	for i := range st.NumFields() {
		f := st.Field(i)
		if f.Name() == "_" {
			continue
		}
		fpath := append(slices.Clip(path), f.Name())
		label := m.sc.model.Obj().Name() + "." + strings.Join(fpath, ".")
		pos := m.fset.Position(f.Pos())
		written := reflect.StructTag(st.Tag(i)).Get("orm")

		tag, err := parseTag(st.Tag(i))
		switch {
		case err != nil:
			m.ds = append(m.ds, diag.Errorf(pos, "%s: %v", label, err))
			continue
		case tag.skip:
			continue
		case !f.Exported():
			m.ds = append(m.ds, diag.Errorf(pos, "%s: the code kanna-orm writes cannot reach an unexported field", label).
				WithHints(`export it, or leave it out with orm:"-"`))
			continue
		case packages.Invalid(f.Type()):
			m.ds = append(m.ds, packages.NotTypeChecked(pos))
			m.unchecked = append(m.unchecked, f.Name())
			continue
		}

		switch t := f.Type(); {
		case tag.relation != "":
			m.relations = append(m.relations, relation{path: fpath, typ: t, fk: tag.fk, pos: pos,
				table: tag.relation, value: tag.value})
		case f.Embedded() && isModel(t):
			if written != "" {
				m.ds = append(m.ds, diag.Errorf(pos, `%s: orm:%q is on an embedded struct, whose fields hold the columns`,
					label, written).WithHints("tag those fields instead"))
				continue
			}
			m.match(t.Underlying().(*types.Struct), fpath) //nolint:forcetypeassert // a model is a struct
		case f.Embedded() && isPointerTo(t, isModel):
			m.ds = append(m.ds, diag.Errorf(pos, "%s: an embedded pointer may be nil, and so cannot be read into", label).
				WithHints(fmt.Sprintf(`embed %s itself, or leave it out with orm:"-"`, f.Name())))
		case holdsColumn(t):
			if tag.fk != nil {
				m.ds = append(m.ds, diag.Errorf(pos, `%s: orm:%q is for a relation, and %s holds a column`,
					label, written, f.Name()))
				continue
			}
			name := tag.column
			if name == "" {
				name = columnName(f.Name())
			}
			m.column(label, fpath, f, pos, name, tag)
		case holdsModels(t):
			if isModel(t) {
				typ := types.TypeString(t, relativeTo(f.Pkg()))
				m.ds = append(m.ds, diag.Errorf(pos, "%s: a relation of one row is held by a pointer, nil when its row "+
					"is not loaded or not there", label).WithHints(fmt.Sprintf("declare it as *%s, or, for a column, "+
					"give %s a Scan method", typ, typ)))
				continue
			}
			if tag.column != "" {
				m.ds = append(m.ds, diag.Errorf(pos, `%s: orm:%q names a column, but %s is a relation`,
					label, written, f.Name()).WithHints(`name the foreign key it follows with orm:"fk=<column>"`))
				continue
			}
			if tag.nullzero || tag.insertDefault || tag.insertOnly || tag.audit != auditNone {
				m.ds = append(m.ds, diag.Errorf(pos, `%s: orm:%q is for a column, and %s is a relation`,
					label, written, f.Name()))
				continue
			}
			m.relations = append(m.relations, relation{path: fpath, typ: t, fk: tag.fk, pos: pos})
		default:
			typ := types.TypeString(t, relativeTo(f.Pkg()))
			m.ds = append(m.ds, diag.Errorf(pos, "%s: %s is neither a column nor a relation", label, typ).
				WithHints(`hold the column in a type with a Scan method, or leave the field out with orm:"-"`))
		}
	}
}

// column matches f, at path in the model, to the column of the table named name.
func (m *matcher) column(label string, path []string, f *types.Var, pos token.Position, name string, tag fieldTag) {
	nullzero, insertDefault := tag.nullzero, tag.insertDefault
	i := slices.IndexFunc(m.table.Columns, func(c snapshot.Column) bool { return c.Name == name })
	if i < 0 {
		d := diag.Errorf(pos, "%s: %s has no column %s", label, m.sc.table, name)
		names := make([]string, 0, len(m.table.Columns))
		for _, c := range m.table.Columns {
			names = append(names, c.Name)
		}
		if near, ok := suggest.Closest(name, names); ok {
			d = d.WithHints(fmt.Sprintf("did you mean %s?", near))
		} else {
			d = d.WithHints(`name the column with orm:"<column>", or leave the field out with orm:"-"`)
		}
		m.ds = append(m.ds, d)
		return
	}
	col := m.table.Columns[i]
	if insertDefault && tag.insertOnly {
		m.ds = append(m.ds, diag.Errorf(pos, "%s: default leaves %s.%s out of an insert, and insertonly out of every "+
			"other write, so it would never be written", label, m.sc.table, name))
		return
	}

	if other, ok := m.held[name]; ok {
		m.ds = append(m.ds, diag.Errorf(pos, "%s: %s.%s is held by %s already", label, m.sc.table, name, other).
			WithHints(`leave one of them out with orm:"-"`))
		return
	}
	m.held[name] = label

	typ := types.TypeString(f.Type(), relativeTo(f.Pkg()))
	if nullzero && (col.NotNull || !isBasic(f.Type().Underlying(), types.String)) {
		m.ds = append(m.ds, diag.Errorf(pos, "%s: nullzero requires a string field holding a nullable column", label))
		return
	}
	if !col.NotNull && !nilable(f.Type()) && !nullzero {
		m.ds = append(m.ds, diag.Errorf(pos, "%s: %s.%s may be NULL, which %s %s cannot hold",
			label, m.sc.table, name, article(typ), typ).WithHints("declare it as *"+typ))
		return
	}
	if want, ok := readable(f.Type(), col.Type); !ok {
		if isPointer(f.Type()) {
			want = "*" + want
		}
		m.ds = append(m.ds, diag.Errorf(pos, "%s: %s.%s is %s, which %s %s cannot hold", label, m.sc.table, name,
			col.Type, article(typ), typ).WithHints("declare it as "+want+", or as a type of your own with Scan and "+
			"Value methods"))
		return
	}
	m.columns = append(m.columns, column{path: path, typ: f.Type(), col: col, pos: pos,
		nullzero: nullzero, insertDefault: insertDefault, insertOnly: tag.insertOnly, audit: tag.audit})
}

// defaults reports each column tagged default of which the snapshot records no database default, but for the times a
// row is created and updated at, which a trigger may assign, as the snapshot records no triggers.
func (m *matcher) defaults() {
	for _, c := range m.columns {
		if c.insertDefault && c.audit == auditNone && !c.col.Default && !c.col.Assigned {
			m.ds = append(m.ds, diag.Errorf(c.pos, "%s.%s: default requires %s.%s to have a database default",
				m.sc.model.Obj().Name(), strings.Join(c.path, "."), m.sc.table, c.col.Name))
		}
	}
}

// holdsColumn reports whether a field of type t holds a column: t is a type a driver reads a column into, a pointer
// to one, or a slice of either, for an array.
func holdsColumn(t types.Type) bool {
	return scalar(t) || isPointerTo(t, scalar) || isSliceOf(t, func(e types.Type) bool {
		return scalar(e) || isPointerTo(e, scalar)
	})
}

// holdsModels reports whether a field of type t holds other models: t is a model, a pointer to one, or a slice of
// either.
func holdsModels(t types.Type) bool {
	return isModel(t) || isPointerTo(t, isModel) || isSliceOf(t, func(e types.Type) bool {
		return isModel(e) || isPointerTo(e, isModel)
	})
}

// scalar reports whether a driver reads a column into a t: a boolean, a number, or a string, or a type made of one; a
// time.Time or a uuid.UUID; or a type whose pointer has a Scan method, as a value type has.
func scalar(t types.Type) bool {
	if b, ok := t.Underlying().(*types.Basic); ok {
		info := b.Info()
		return info&(types.IsBoolean|types.IsNumeric|types.IsString) != 0 && info&types.IsComplex == 0
	}
	return isTime(t) || isUUID(t) || scanner(t)
}

func isTime(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "time" && n.Obj().Name() == "Time"
}

// isUUID reports whether t is the uuid.UUID of the standard library, from Go 1.27 on, which pgx and database/sql read
// a uuid into, though it has no Scan method.
func isUUID(t types.Type) bool {
	return isNamed(t, "uuid", "UUID")
}

// scanner reports whether a *t is a sql.Scanner.
func scanner(t types.Type) bool {
	if types.IsInterface(t) {
		return false
	}
	if _, ok := t.Underlying().(*types.Pointer); !ok {
		t = types.NewPointer(t)
	}
	return types.Implements(t, sqlScanner)
}

// sqlScanner is the interface sql.Scanner, which the packages a run loads need not import.
var sqlScanner = types.NewInterfaceType([]*types.Func{types.NewFunc(token.NoPos, nil, "Scan", types.NewSignatureType(
	nil, nil, nil,
	types.NewTuple(types.NewParam(token.NoPos, nil, "src", types.Universe.Lookup("any").Type())),
	types.NewTuple(types.NewParam(token.NoPos, nil, "", types.Universe.Lookup("error").Type())), false))}, nil).Complete()

// isModel reports whether t is a named struct that no driver reads a column into.
func isModel(t types.Type) bool {
	_, ok := types.Unalias(t).(*types.Named)
	return ok && isStruct(t) && !scalar(t)
}

func isPointerTo(t types.Type, elem func(types.Type) bool) bool {
	p, ok := t.Underlying().(*types.Pointer)
	return ok && elem(p.Elem())
}

func isSliceOf(t types.Type, elem func(types.Type) bool) bool {
	s, ok := t.Underlying().(*types.Slice)
	return ok && elem(s.Elem())
}

func nilable(t types.Type) bool {
	switch t.Underlying().(type) {
	case *types.Pointer, *types.Slice, *types.Map, *types.Interface:
		return true
	}
	return false
}

// relativeTo names the types of pkg by their names alone, and those of other packages by their package's name too,
// as code in pkg spells them.
func relativeTo(pkg *types.Package) types.Qualifier {
	return func(p *types.Package) string {
		if p == pkg {
			return ""
		}
		return p.Name()
	}
}

// article is the indefinite article of the name of a type, by its first letter: an int64, but a string.
func article(typ string) string {
	if strings.ContainsAny(typ[:1], "aeioAEIO") {
		return "an"
	}
	return "a"
}

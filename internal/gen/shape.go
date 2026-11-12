package gen

import (
	"fmt"
	"go/ast"
	"go/types"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-kanna/orm/internal/decl"
	"github.com/go-kanna/orm/internal/snapshot"
)

// shapes are the forms a method of each verb is declared in, for a hint, of the names shapeNames fills in.
var shapes = map[verb]string{
	verbGet:    "Get(ctx context.Context, {keys}, s ...{scope}) ({model}, error)",
	verbFind:   "Find(ctx context.Context, {keys}, s ...{scope}) (*{model}, error)",
	verbFirst:  "First(ctx context.Context, s ...{scope}) ({model}, error)",
	verbList:   "List(ctx context.Context, s ...{scope}) ([]{model}, error)",
	verbCount:  "Count(ctx context.Context, s ...{scope}) (int64, error)",
	verbExists: "Exists(ctx context.Context, s ...{scope}) (bool, error)",
	verbInsert: "Insert(ctx context.Context, {row} {model}) ({model}, error), or with error alone",
	verbUpdate: "Update(ctx context.Context, {row} {model}, s ...{scope}) error, or " +
		"Update(ctx context.Context, {keys}, c {changes}, s ...{scope}) ({model}, error)",
	verbTouch: "Touch(ctx context.Context, {keys}) error, or TouchStale(ctx context.Context, s ...{scope}) " +
		"(int64, error)",
	verbUpsert: "Upsert(ctx context.Context, {row} {model}) error",
	verbDelete: "Delete(ctx context.Context, {keys}) error, or DeleteStale(ctx context.Context, " +
		"s ...{scope}) (int64, error) or ([]{model}, error)",
	verbCopy: "Copy(ctx context.Context, rows []{model}) (int64, error)",
	verbSum:  "Sum{number}(ctx context.Context, s ...{scope}) ({numberType}, error)",
	verbMin:  "Min{ordered}(ctx context.Context, s ...{scope}) ({orderedType}, error)",
	verbMax:  "Max{ordered}(ctx context.Context, s ...{scope}) (*{orderedType}, error)",
	verbAvg:  "Avg{number}(ctx context.Context, s ...{scope}) (float64, error)",
}

// pluckShape is the form of a method reading the values of one column, for a hint.
const pluckShape = "List{plural}(ctx context.Context, s ...{scope}) ([]{keyType}, error), which reads {key}"

// shapeNames are the names a hint declares a method of a model with: its type, its scope type, and its fields.
type shapeNames struct {
	model, scope, changes, row string
	// keys are the parameters of its key, and key and keyType the field of its first column and the field's type.
	keys, key, keyType string
	// number names a field of numbers, and ordered one of an order, which aggregates read, and their types.
	number, numberType, ordered, orderedType string
}

// orderShape are the names of Order, which a hint shows when it knows no model of its own.
var orderShape = shapeNames{
	model: "model.Order", scope: "scopes.Order", changes: "scopes.OrderChangeSet", row: "o",
	keys: "id model.OrderID", key: "ID", keyType: "model.OrderID",
	number: "Total", numberType: "int64", ordered: "PlacedAt", orderedType: "time.Time",
}

// fill gives the form of f, a form of shapes, of the names of n.
func (n shapeNames) fill(f string) string {
	plural := n.key + "s"
	if !endsInAcronym(n.key) && !strings.HasPrefix(n.key, "<") {
		plural = camelCase(decl.Plural(n.key))
	}
	return strings.NewReplacer("{model}", n.model, "{scope}", n.scope, "{changes}", n.changes, "{row}", n.row,
		"{keys}", n.keys, "{key}", n.key, "{keyType}", n.keyType, "{plural}", plural,
		"{number}", n.number, "{numberType}", n.numberType, "{ordered}", n.ordered,
		"{orderedType}", n.orderedType).Replace(f)
}

// shape gives the form of a method of v, for a hint, of model, or of Order if model is nil or not known of.
func (r repoReader) shape(v verb, model *types.Named) string {
	return r.shapeNames(model).fill(shapes[v])
}

// shapeNames finds the names of model, its scope type and its fields, for a hint: those of Order if it is nil, or no
// scope type is declared for it, and placeholders for the fields it has none of.
func (r repoReader) shapeNames(model *types.Named) shapeNames {
	if model == nil {
		return orderShape
	}
	sc, ok := r.l.scopeOf(model)
	if !ok {
		return orderShape
	}
	changes, _ := changeSetNames(sc.name)
	n := shapeNames{
		model: typeName(model), scope: sc.pkgName + "." + sc.name, changes: sc.pkgName + "." + changes,
		row:  string(unicode.ToLower(firstRune(model.Obj().Name()))),
		keys: "<key> <type>", key: "<Field>", keyType: "<type>",
		number: "<Field>", numberType: "<type>", ordered: "<Field>", orderedType: "<type>",
	}
	table, ok := r.s.Tables[sc.table]
	if !ok {
		return n
	}
	f, _ := fields(r.pkg.Fset, scope{name: sc.name, model: model, table: sc.table}, table, r.s.Dialect)
	byName := func(name string) (column, bool) {
		i := slices.IndexFunc(f.columns, func(c column) bool { return c.col.Name == name })
		if i < 0 {
			return column{}, false
		}
		return f.columns[i], true
	}
	for _, key := range append([][]string{table.PrimaryKey}, table.UniqueKeys...) {
		var params []string
		for _, name := range key {
			if c, ok := byName(name); ok {
				params = append(params, lowerFirst(c.field())+" "+typeName(c.typ))
			}
		}
		if len(key) == 0 || len(params) < len(key) {
			continue
		}
		c, _ := byName(key[0])
		n.keys, n.key, n.keyType = strings.Join(params, ", "), c.field(), typeName(c.typ)
		break
	}
	// An aggregate reads a column of the row's own, which no key or foreign key holds.
	other := func(c column) bool {
		return !slices.Contains(table.PrimaryKey, c.col.Name) && !slices.ContainsFunc(table.ForeignKeys,
			func(fk snapshot.ForeignKey) bool { return slices.Contains(fk.Columns, c.col.Name) })
	}
	if i := slices.IndexFunc(f.columns, func(c column) bool {
		return other(c) && numeric(elemOf(c.typ), c.col.Type)
	}); i >= 0 {
		n.number, n.numberType = f.columns[i].field(), typeName(elemOf(f.columns[i].typ))
	}
	// A time is what a minimum or a maximum is read of the most.
	i := slices.IndexFunc(f.columns, func(c column) bool { return other(c) && kindOf(elemOf(c.typ)).time })
	if i < 0 {
		i = slices.IndexFunc(f.columns, func(c column) bool {
			return other(c) && kindOf(elemOf(c.typ)).ordered && classOf(c.col.Type) != classUUID
		})
	}
	if i >= 0 {
		n.ordered, n.orderedType = f.columns[i].field(), typeName(elemOf(f.columns[i].typ))
	}
	return n
}

// field is the name of the field holding c, without those of the structs it is embedded in.
func (c column) field() string { return c.path[len(c.path)-1] }

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

// camelCase spells snake, a name of words joined by underscores, as Go does: order_lines as OrderLines.
func camelCase(snake string) string {
	var b strings.Builder
	for word := range strings.SplitSeq(snake, "_") {
		if word != "" {
			b.WriteString(strings.ToUpper(word[:1]) + word[1:])
		}
	}
	return b.String()
}

// returnShapes are what a method of each verb returns besides its error, of the names shapeNames fills in.
var returnShapes = map[verb]string{
	verbGet:    "{model}",
	verbFind:   "*{model}",
	verbFirst:  "{model}",
	verbList:   "[]{model}",
	verbCount:  "int64",
	verbExists: "bool",
	verbInsert: "{model} of one row, []{model} of many, or error alone",
	verbUpdate: "{model} of one row, int64 or []{model} of the rows of a change set, or error alone",
	verbTouch:  "int64 of the rows its scopes select, or error alone",
	verbUpsert: "{model} of one row, []{model} of many, or error alone",
	verbDelete: "int64 or []{model} of the rows its scopes select, or error alone of the row of a key",
	verbCopy:   "int64",
	verbSum:    "the type of its field",
	verbMin:    "the type of its field",
	verbMax:    "the type of its field",
	verbAvg:    "float64",
}

func (v verb) article() string {
	switch v { //nolint:exhaustive // the verbs read with an
	case verbInsert, verbUpdate, verbUpsert, verbExists, verbAvg:
		return "an"
	}
	return "a"
}

// resultsMismatch tells why results are not those of a method of v, an error last after one value at most, if they
// are not.
func resultsMismatch(v verb, results *types.Tuple) string {
	switch {
	case results.Len() == 0:
		return fmt.Sprintf("it returns nothing, and %s %s returns an error", v.article(), v)
	case results.Len() > 2:
		return fmt.Sprintf("it returns %d values, and %s %s returns an error after one value at most", results.Len(),
			v.article(), v)
	case !types.Identical(results.At(results.Len()-1).Type(), types.Universe.Lookup("error").Type()):
		return fmt.Sprintf("its last result is %s, not error", typeName(results.At(results.Len()-1).Type()))
	}
	return ""
}

// signatureModel finds the model of the rows a method of sig reads or writes, of the repository ts declares, for a
// hint: that of its scopes, its change set, the rows it takes, or those it returns, or else fallback.
func (r repoReader) signatureModel(sig *types.Signature, field *ast.Field, ts *ast.TypeSpec,
	fallback tableModel,
) *types.Named {
	params := sig.Params()
	for i := 1; i < params.Len(); i++ {
		p := params.At(i)
		if sig.Variadic() && i == params.Len()-1 {
			if model, _, ok := r.scopeParam(p, field, ts); ok {
				return model
			}
			continue
		}
		if model, ok := r.changesParam(p, field, ts); ok {
			return model
		}
		if model, _, ok := r.rowModel(p.Type()); ok {
			return model
		}
	}
	for t := range sig.Results().Variables() {
		if model, _, ok := r.rowModel(elemOf(t.Type())); ok {
			return model
		}
	}
	return fallback.model
}

// mismatch tells why m is of no form of its verb, given result, what it returns besides its error, or error alone, in
// the names of n, if it can tell.
func (m repoMethod) mismatch(result string, n shapeNames) string {
	v := m.verb
	writes := v == verbInsert || v == verbUpsert || v == verbCopy || v == verbUpdate && m.changes == ""
	switch {
	case !writes && v != verbUpdate && m.row != "":
		return fmt.Sprintf("it takes %s, rows to write, and %s %s writes none", m.row, v.article(), v)
	case writes && m.row == "":
		return fmt.Sprintf("it takes no row to write, and %s %s writes the row it takes", v.article(), v)
	case v == verbCopy && !m.many:
		return fmt.Sprintf("it takes %s, one row, and a Copy takes a slice of them", m.row)
	case v == verbUpdate && m.row != "" && m.many:
		return fmt.Sprintf("it takes %s, many rows, and an Update writes one", m.row)
	case writes && len(m.keys) > 0:
		return fmt.Sprintf("it takes %s besides its row, and %s %s writes the row by the key it holds",
			m.keys[0].param, v.article(), v)
	case writes && v != verbUpdate && m.scoped:
		return fmt.Sprintf("it takes scopes, and %s %s writes the rows it takes", v.article(), v)
	case !m.returnsFit():
		return fmt.Sprintf("it returns %s, and %s %s returns %s", result, v.article(), v, n.fill(returnShapes[v]))
	case v == verbDelete && m.returns == returnsNone && m.scoped:
		return "it takes scopes, and a Delete returning error alone deletes the one row of its key"
	case v == verbDelete && m.returns != returnsNone && !m.scoped && len(m.keys) == 0:
		return fmt.Sprintf("it returns %s, and takes neither the scopes nor the keys of the rows it deletes", result)
	}
	return ""
}

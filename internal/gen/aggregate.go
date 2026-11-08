package gen

import (
	"fmt"
	"go/types"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"

	"github.com/go-kanna/orm/internal/snapshot"
)

// aggregate is what a Sum, a Min, a Max, or an Avg reads: the sum, the minimum, the maximum, or the average of a
// column.
type aggregate struct {
	fn  verb
	col column
	// typ is what it is read into: a value, or a pointer to one, nil for no value.
	typ types.Type
	// coalesce reports whether no value reads as zero, as of a Sum read into a value.
	coalesce bool
}

// aggregates reports whether v reads an aggregate of a column.
func aggregates(v verb) bool {
	return v == verbSum || v == verbMin || v == verbMax || v == verbAvg
}

// aggregateWords name what each aggregate of a column reads.
var aggregateWords = map[verb]string{verbSum: "sum", verbMin: "minimum", verbMax: "maximum", verbAvg: "average"}

// aggregateColumn finds the column whose aggregate m, a Sum, a Min, a Max, or an Avg, reads as t, its result: that of
// the field its name says after the verb, as SumTotal reads the sum of that of Total. The name says nothing more: which
// rows, as ever, are told by its keys and scopes.
func (r repoReader) aggregateColumn(m *repoMethod, t types.Type) []diag.Diag {
	rest := strings.TrimPrefix(m.name, string(m.verb))
	word := aggregateWords[m.verb]
	i := slices.IndexFunc(m.columns, func(c column) bool { return c.path[len(c.path)-1] == rest })
	switch {
	case rest == "":
		return []diag.Diag{diag.Errorf(m.pos, "%s: it names no field after %[1]s, whose column it reads the %s of",
			m.name, word).WithHints("name it after a field, as in " + shapes[m.verb])}
	case i < 0:
		return []diag.Diag{diag.Errorf(m.pos, "%s: it reads the %s of a column, but %s has no field named %s", m.name,
			word, typeName(m.model), rest).WithHints("name it after a field, as in " + shapes[m.verb] +
			", whose keys and scopes tell the rows")}
	}
	a := aggregate{fn: m.verb, col: m.columns[i], typ: t, coalesce: m.verb == verbSum && !isPointer(t)}
	if msg, hint, ok := aggregateRule(m, a); !ok {
		return []diag.Diag{diag.Errorf(m.pos, "%s: it %s", m.name, msg).WithHints(hint)}
	}
	m.aggregate = &a
	return nil
}

// aggregateRule reports why a, of m, reads no aggregate, if it does not: Sum and Avg read a column of numbers, and Min
// and Max one of an order; Avg reads a floating-point number, and the others a value of the field's type; any of them a
// pointer to one, nil for no value. The message follows its subject.
func aggregateRule(m *repoMethod, a aggregate) (msg, hint string, ok bool) {
	c := a.col
	field := m.model.Obj().Name() + "." + strings.Join(c.path, ".")
	elem := elemOf(c.typ)
	word, ft := aggregateWords[a.fn], typeName(elem)
	switch a.fn { //nolint:exhaustive // the verbs of aggregates
	case verbSum, verbAvg:
		numbers := "Sum and Avg read a column of numbers, held by an integer or a floating-point field"
		if b, ok := elem.Underlying().(*types.Basic); !ok || b.Info()&(types.IsInteger|types.IsFloat) == 0 {
			return fmt.Sprintf("reads the %s of %s, %s %s, which is no number", word, field, article(ft), ft), numbers,
				false
		}
		if !numeric(elem, c.col.Type) {
			return fmt.Sprintf("reads the %s of %s, whose column %s is %s, which holds no number", word, field,
				c.col.Name, c.col.Type), numbers, false
		}
	case verbMin, verbMax:
		if k := kindOf(elem); !k.ordered {
			return fmt.Sprintf("reads the %s of %s, %s %s, which has no order", word, field, article(ft), ft),
				"Min and Max read a column of numbers, strings, or times", false
		}
		if classOf(c.col.Type) == classUUID {
			return fmt.Sprintf("reads the %s of %s, but the database has no %s of a uuid, as its column %s is", word,
				field, strings.ToLower(string(a.fn)), c.col.Name), "read it by SQL written by hand", false
		}
	}
	read := a.typ
	if p, ok := a.typ.Underlying().(*types.Pointer); ok {
		read = p.Elem()
	}
	rt := typeName(a.typ)
	if a.fn == verbAvg {
		if !isBasic(read, types.Float64) && !isBasic(read, types.Float32) {
			return fmt.Sprintf("reads the average of %s as %s %s, which is no floating-point number", field,
				article(rt), rt), "declare it as float64, or *float64, nil when there is no value", false
		}
		return "", "", true
	}
	if !types.Identical(read, elem) {
		return fmt.Sprintf("reads the %s of %s, %s %s, as %s %s", word, field, article(ft), ft, article(rt), rt),
			fmt.Sprintf("declare it as %s, or *%[1]s, nil when there is no value", ft), false
	}
	return "", "", true
}

// aggregateSQL is the SQL of a, of what d returns as Go reads it: a sum of integers as a bigint on PostgreSQL, whose
// sum of bigints is numeric, and an average as a double precision number, which MySQL returns of integers as a DECIMAL
// rounded to four places.
func aggregateSQL(d snapshot.Dialect, a aggregate) string {
	name := sqlName(d, a.col.col.Name)
	switch a.fn { //nolint:exhaustive // the verbs of aggregates
	case verbSum:
		sum := "sum(" + name + ")"
		if d == snapshot.PostgreSQL && classOf(a.col.col.Type) == classInteger {
			sum += "::bigint"
		}
		if a.coalesce {
			sum = "coalesce(" + sum + ", 0)"
		}
		return sum
	case verbAvg:
		if d == snapshot.MySQL {
			return "CAST(avg(" + name + ") AS DOUBLE)"
		}
		return "avg(" + name + ")::double precision"
	}
	return strings.ToLower(string(a.fn)) + "(" + name + ")"
}

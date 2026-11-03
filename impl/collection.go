package impl

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Batch describes the join used for a generated collection preload.
type Batch struct {
	ParentTable, Table                      string
	ParentKeys, ChildKeys                   []string
	Through                                 string
	ThroughParent, ThroughChild, TargetKeys []string
	Projections                             []Projection
}
type batchPlan[M any] struct {
	parent Parent
	scope  Of[M]
}

type batchField struct {
	field string
	plan  any // a batchPlan of the model of field
}

// PreloadMany has field loaded for each row of q by the rows m selects, correlated by parent. What keeps m from paging
// the rows of each parent becomes an error of q now, rather than when the rows of field are read, which no parent row
// may come to.
func PreloadMany[M, C any](q Of[M], field string, parent Parent, m Many[C]) Of[M] {
	q.err = errors.Join(q.err, m.scope.err, m.scope.pageEach(field))
	q = q.addBatch(batchField{field, batchPlan[C]{parent, m.scope}})
	return q
}

// pageEach reports why q cannot page the rows of field for each parent.
func (q Of[M]) pageEach(field string) error {
	switch {
	case q.seeking:
		return fmt.Errorf("orm: %s does not accept a cursor; use Offset", field)
	case (q.hasLimit || q.hasOffset) && len(q.orders) == 0:
		return fmt.Errorf("orm: %s keeps some of the rows of each parent, which requires an order", field)
	case q.hasLimit && q.hasOffset && q.limit > math.MaxInt-q.offset:
		return fmt.Errorf("orm: the page of %s overflows", field)
	}
	return nil
}

// loadsTwice reports a relation q loads both by Preload, as a With method has it, and by PreloadMany, as a Preload
// method has it: one would load the rows of the other over again.
func (q Of[M]) loadsTwice() error {
	for _, b := range q.batches {
		if slices.ContainsFunc(q.preloads, func(p preload) bool { return p.relation == b.field }) {
			name := b.field[strings.LastIndex(b.field, ".")+1:]
			return fmt.Errorf("orm: %s is loaded by both With%s and Preload%s; use one", b.field, name, name)
		}
	}
	return nil
}

// addBatch returns q loading batch too, or with an error if q loads its field by PreloadMany already.
func (q Of[M]) addBatch(batch batchField) Of[M] {
	if slices.ContainsFunc(q.batches, func(b batchField) bool { return b.field == batch.field }) {
		q.err = errors.Join(q.err, fmt.Errorf("orm: %s is preloaded twice", batch.field))
		return q
	}
	q.batches = append(slices.Clip(q.batches), batch)
	return q
}

func PreloadedMany[C, M any](q Of[M], field string) (Of[C], Parent, bool) {
	for _, p := range q.batches {
		if p.field != field {
			continue
		}
		v, ok := p.plan.(batchPlan[C])
		if !ok {
			panic("orm: collection type mismatch")
		}
		return v.scope, v.parent, true
	}
	return Of[C]{}, Parent{}, false
}

// RenderBatch returns parent keys followed by model columns, with pagination applied independently to each parent.
func (q Of[M]) RenderBatch(d Dialect, b Batch, parent Parent, columns []string, keys [][]any) (string, []any, error) {
	if err := q.validateBatch(b, keys); err != nil {
		return "", nil, err
	}
	projections, args, err := q.renderProjections(d, b.Projections, "orm_child", 1)
	if err != nil {
		return "", nil, err
	}
	restrictions, keyArgs, err := batchRestrictions(d, b.ParentKeys, keys, len(args)+1)
	if err != nil {
		return "", nil, err
	}
	args = append(args, keyArgs...)
	q.parent = &parentContext{parent, "orm_parent"}
	q.parentPrefix = "orm_p_"
	rd, err := q.Render(d, len(args)+1)
	if err != nil {
		return "", nil, err
	}
	args = append(args, rd.Args()...)
	parentNames := q.parentNames(parent, b.ParentKeys)
	from := b.fromSQL(d, parentNames)
	names, partition, outputs := b.resultColumns(d, columns)
	names = append(names, projections...)
	for _, p := range b.Projections {
		outputs = append(outputs, quoted(d, p.Column))
	}
	where := " WHERE (" + restrictions + ")" + rd.And()
	if !q.hasLimit && !q.hasOffset {
		return "SELECT " + strings.Join(names, ", ") + from + where + rd.OrderBy(), args, nil
	}
	inner := "SELECT " + strings.Join(names, ", ") + ", ROW_NUMBER() OVER (PARTITION BY " + strings.Join(partition, ", ")
	inner += " ORDER BY " + rd.orders + ") AS " + quoted(d, "orm_rank") + from + where
	lower := 0
	if q.hasOffset {
		lower = q.offset
	}
	outer := "SELECT " + strings.Join(outputs, ", ") + " FROM (" + inner + ") AS " + quoted(d, "orm_ranked")
	outer += " WHERE " + quoted(d, "orm_rank") + " > " + strconv.Itoa(lower)
	if q.hasLimit {
		outer += " AND " + quoted(d, "orm_rank") + " <= " + strconv.Itoa(lower+q.limit)
	}
	return outer + " ORDER BY " + quoted(d, "orm_rank"), args, nil
}

func (q Of[M]) validateBatch(b Batch, keys [][]any) error {
	if len(keys) == 0 || len(b.ParentKeys) == 0 {
		return errors.New("orm: invalid batch keys")
	}
	if b.Through == "" && len(b.ParentKeys) != len(b.ChildKeys) {
		return errors.New("orm: inconsistent relation keys")
	}
	if b.Through != "" && (len(b.ParentKeys) != len(b.ThroughParent) || len(b.ThroughChild) != len(b.TargetKeys)) {
		return errors.New("orm: inconsistent junction keys")
	}
	return q.err
}
func batchRestrictions(d Dialect, columns []string, keys [][]any, first int) (string, []any, error) {
	predicates := make([]string, 0, len(keys))
	args := []any{}
	for _, key := range keys {
		if len(key) != len(columns) {
			return "", nil, errors.New("orm: inconsistent batch key width")
		}
		conditions := make([]string, 0, len(columns))
		for i, name := range columns {
			placeholder := "?"
			if d == Postgres {
				placeholder = "$" + strconv.Itoa(first+len(args))
			}
			args = append(args, key[i])
			conditions = append(conditions, qualified(d, "orm_parent", "orm_p_"+name)+" = "+placeholder)
		}
		predicates = append(predicates, "("+strings.Join(conditions, " AND ")+")")
	}
	return strings.Join(predicates, " OR "), args, nil
}
func (q Of[M]) parentNames(parent Parent, keys []string) []string {
	names := slices.Clone(keys)
	for _, condition := range q.conds {
		for _, arg := range condition.args {
			ref, ok := asReference(arg)
			if ok && ref.parent.identity == parent.identity && !slices.Contains(names, ref.name) {
				names = append(names, ref.name)
			}
		}
	}
	return names
}
func (b Batch) fromSQL(d Dialect, parentNames []string) string {
	parentColumns := make([]string, 0, len(parentNames))
	for _, name := range parentNames {
		parentColumns = append(parentColumns, quoted(d, name)+" AS "+quoted(d, "orm_p_"+name))
	}
	text := " FROM " + quoted(d, b.Table) + " AS " + quoted(d, "orm_child")
	joins := make([]string, 0, len(b.ParentKeys))
	if b.Through == "" {
		for i, name := range b.ChildKeys {
			joins = append(joins, qualified(d, "orm_child", name)+" = "+qualified(d, "orm_parent", "orm_p_"+b.ParentKeys[i]))
		}
	} else {
		junction, on := b.throughSQL(d)
		text += " JOIN (" + junction + ") AS " + quoted(d, "orm_link") + " ON " + on
		for i, name := range b.ThroughParent {
			joins = append(joins, qualified(d, "orm_link", "orm_l_"+name)+" = "+qualified(d, "orm_parent", "orm_p_"+
				b.ParentKeys[i]))
		}
	}
	text += " JOIN (SELECT " + strings.Join(parentColumns, ", ") + " FROM " + quoted(d, b.ParentTable) + ")"
	return text + " AS " + quoted(d, "orm_parent") + " ON " + strings.Join(joins, " AND ")
}
func (b Batch) throughSQL(d Dialect) (string, string) {
	linkColumns := []string{}
	for _, name := range slices.Concat(b.ThroughParent, b.ThroughChild) {
		if !slices.Contains(linkColumns, name) {
			linkColumns = append(linkColumns, name)
		}
	}
	selects := make([]string, 0, len(linkColumns))
	for _, name := range linkColumns {
		selects = append(selects, quoted(d, name)+" AS "+quoted(d, "orm_l_"+name))
	}
	joins := make([]string, 0, len(b.ThroughChild))
	for i, name := range b.ThroughChild {
		joins = append(joins, qualified(d, "orm_link", "orm_l_"+name)+" = "+qualified(d, "orm_child", b.TargetKeys[i]))
	}
	return "SELECT " + strings.Join(selects, ", ") + " FROM " + quoted(d, b.Through), strings.Join(joins, " AND ")
}
func (b Batch) resultColumns(d Dialect, columns []string) (names, partition, outputs []string) {
	for i, name := range b.ParentKeys {
		expr := qualified(d, "orm_parent", "orm_p_"+name)
		alias := quoted(d, "orm_key_"+strconv.Itoa(i))
		names = append(names, expr+" AS "+alias)
		partition = append(partition, expr)
		outputs = append(outputs, alias)
	}
	for _, name := range columns {
		names = append(names, qualified(d, "orm_child", name))
		outputs = append(outputs, quoted(d, name))
	}
	return names, partition, outputs
}

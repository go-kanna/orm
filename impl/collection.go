package impl

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Batch tells how the rows of a relation are related to the rows they are loaded for: by ChildKeys of Table, or
// through the table Through, whose ThroughParent columns hold the key of a row and whose ThroughChild columns hold
// TargetKeys of Table.
type Batch struct {
	Table                                   string
	ChildKeys                               []string
	Through                                 string
	ThroughParent, ThroughChild, TargetKeys []string
}

// PagesEach reports whether q, the scope of a relation's rows, keeps only some of the related rows of each row: a
// limit or an offset, which a With method applies to the related rows of each row on its own.
func (q Of[M]) PagesEach() bool { return q.hasLimit || q.hasOffset }

// RenderEach returns the query reading a page of the rows of q related to each of keys, the values of the columns
// the rows they are loaded for are related by. Each row comes with the key it is related to first, then columns.
// key is the primary key of the table, as Render takes it: the order a page is counted in ends in it, or is it
// when q has none.
func (q Of[M]) RenderEach(d Dialect, b Batch, key, columns []string, keys [][]any) (string, []any, error) {
	q = q.DefaultOrder(key)
	if err := q.pageEach(); err != nil {
		return "", nil, err
	}
	if len(keys) == 0 || (b.Through == "" && len(b.ChildKeys) == 0) || (b.Through != "" && len(b.ThroughParent) == 0) {
		return "", nil, errors.New("orm: a page of related rows needs the keys of the rows they are loaded for")
	}
	partition := make([]string, 0, len(b.ChildKeys))
	from := " FROM " + quoted(d, b.Table) + " AS " + quoted(d, "orm_child")
	if b.Through == "" {
		for _, name := range b.ChildKeys {
			partition = append(partition, qualified(d, "orm_child", name))
		}
	} else {
		junction, on := b.throughSQL(d)
		from += " JOIN (" + junction + ") AS " + quoted(d, "orm_link") + " ON " + on
		for _, name := range b.ThroughParent {
			partition = append(partition, qualified(d, "orm_link", "orm_l_"+name))
		}
	}
	restriction, args, err := restrictions(d, partition, keys)
	if err != nil {
		return "", nil, err
	}
	rd, err := q.Render(d, len(args)+1, key)
	if err != nil {
		return "", nil, err
	}
	args = append(args, rd.Args()...)

	names := make([]string, 0, len(partition)+len(columns))
	outputs := make([]string, 0, len(partition)+len(columns))
	for i, expr := range partition {
		alias := quoted(d, "orm_key_"+strconv.Itoa(i))
		names = append(names, expr+" AS "+alias)
		outputs = append(outputs, alias)
	}
	for _, name := range columns {
		names = append(names, qualified(d, "orm_child", name))
		outputs = append(outputs, quoted(d, name))
	}
	inner := "SELECT " + strings.Join(names, ", ") + ", ROW_NUMBER() OVER (PARTITION BY " +
		strings.Join(partition, ", ") + " ORDER BY " + rd.orders + ") AS " + quoted(d, "orm_rank") + from +
		" WHERE (" + restriction + ")" + rd.And()
	outer := "SELECT " + strings.Join(outputs, ", ") + " FROM (" + inner + ") AS " + quoted(d, "orm_ranked") +
		" WHERE " + quoted(d, "orm_rank") + " > " + strconv.Itoa(q.offset)
	if q.hasLimit {
		outer += " AND " + quoted(d, "orm_rank") + " <= " + strconv.Itoa(q.offset+q.limit)
	}
	return outer + " ORDER BY " + quoted(d, "orm_rank"), args, nil
}

// pageEach reports why q cannot keep a page of the related rows of each row.
func (q Of[M]) pageEach() error {
	switch {
	case q.err != nil:
		return q.err
	case q.seeking:
		return errors.New("orm: a page of the related rows of each row continues by Offset, not by a row")
	case len(q.orders) == 0:
		return errors.New("orm: a page of the related rows of each row requires an order")
	case q.hasLimit && q.hasOffset && q.limit > math.MaxInt-q.offset:
		return fmt.Errorf("orm: a page of %d rows after %d overflows", q.limit, q.offset)
	}
	return nil
}

// restrictions is the condition matching the rows related to keys, each by the columns of partition, and its
// arguments.
func restrictions(d Dialect, partition []string, keys [][]any) (string, []any, error) {
	predicates := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys)*len(partition))
	for _, key := range keys {
		if len(key) != len(partition) {
			return "", nil, errors.New("orm: a key of the rows related to has another number of columns")
		}
		conditions := make([]string, 0, len(partition))
		for i, expr := range partition {
			placeholder := "?"
			if d == Postgres {
				placeholder = "$" + strconv.Itoa(len(args)+1)
			}
			args = append(args, key[i])
			conditions = append(conditions, expr+" = "+placeholder)
		}
		predicates = append(predicates, "("+strings.Join(conditions, " AND ")+")")
	}
	return strings.Join(predicates, " OR "), args, nil
}

// throughSQL returns the rows of the table between, its columns named orm_l_ and theirs, and the condition joining
// them to the related rows.
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

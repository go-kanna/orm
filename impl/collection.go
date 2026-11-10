package impl

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Batch tells how the rows of a relation are related to the rows they are loaded for: by ChildKeys of Table, or
// through the table Through, whose ThroughParent columns hold the key of a row and whose ThroughChild columns hold
// TargetKeys of Table. KeyTypes are the types of the columns of the keys, which PostgreSQL casts them to: one of
// pg_catalog by its name, and any other by its schema and its name, as describe records them.
type Batch struct {
	Table                                   string
	ChildKeys                               []string
	KeyTypes                                []string
	Through                                 string
	ThroughParent, ThroughChild, TargetKeys []string
}

// Each is the query reading the rows of a relation for the keys of the rows they are loaded for, as far as it is the
// same for every read: made once, by NewEach, for each read to add the keys and the scope of the relation to.
type Each struct {
	d            Dialect
	key, columns []string
	width        int
	err          error
	// selects lists the number of the key and columns of the related rows, and outputs the same as the columns of the
	// query that pages them. keys is the table of the keys on PostgreSQL, and joins what joins the related rows to it.
	selects, outputs, keys, joins string
}

// NewEach makes the query reading the rows of the relation b says, in d, for the keys of rows: each related row comes
// with the number of the key it is related to first, then columns. key is the primary key of the table, as Render
// takes it: the order a page is counted in ends in it, or is it when the scope has none.
func NewEach(d Dialect, b Batch, key, columns []string) Each {
	e := Each{d: d, key: key, columns: columns}
	matched, prefix := b.ChildKeys, ""
	if b.Through != "" {
		matched, prefix = b.ThroughParent, "orm_l_"
	}
	e.width = len(matched)
	if e.width == 0 {
		e.err = errors.New("orm: related rows need the keys of the rows they are loaded for")
		return e
	}
	if d == Postgres && len(b.KeyTypes) != e.width {
		e.err = errors.New("orm: PostgreSQL casts each column of a key to its type, which the batch does not name")
		return e
	}

	names := []string{quoted(d, "orm_n")}
	for i := range e.width {
		names = append(names, quoted(d, "orm_k"+strconv.Itoa(i)))
	}
	alias := " AS " + quoted(d, "orm_key") + " (" + strings.Join(names, ", ") + ")"
	if d == Postgres {
		arrays := []string{"$1::int8[]"}
		for i, t := range b.KeyTypes {
			// A type outside pg_catalog, which is named by its schema, may be one pgx cannot write, as an enum: its keys
			// go as text, which PostgreSQL reads as the type.
			cast := t + "[]"
			if strings.Contains(t, ".") {
				cast = "text[]::" + cast
			}
			arrays = append(arrays, "$"+strconv.Itoa(i+2)+"::"+cast)
		}
		e.keys = "unnest(" + strings.Join(arrays, ", ") + ")" + alias
	} else {
		e.keys = alias
	}

	child := " JOIN " + quoted(d, b.Table) + " ON "
	if b.Through == "" {
		e.joins = child + matching(d, b.Table, matched, prefix)
	} else {
		junction, on := b.throughSQL(d)
		e.joins = " JOIN (" + junction + ") AS " + quoted(d, "orm_link") + " ON " + matching(d, "orm_link", matched, prefix) +
			child + on
	}
	selects, outputs := []string{qualified(d, "orm_key", "orm_n")}, []string{quoted(d, "orm_n")}
	for _, name := range columns {
		selects = append(selects, qualified(d, b.Table, name))
		outputs = append(outputs, quoted(d, name))
	}
	e.selects, e.outputs = strings.Join(selects, ", "), strings.Join(outputs, ", ")
	return e
}

// PagesEach reports whether q, the scope of a relation's rows, keeps only some of the related rows of each row: a
// limit or an offset, which a With method applies to the related rows of each row on its own.
func (q Of[M]) PagesEach() bool { return q.hasLimit || q.hasOffset }

// RenderEach returns the query e reads the rows of q related to keys with, and its arguments. keys holds a slice for
// each column of the keys, all of a length, and the keys are numbered from first. The database compares the keys to
// the columns, as SQL does whatever Go's == says of their values. When q pages each, it keeps a page of the rows of
// each key, counted apart from those of the others.
func (q Of[M]) RenderEach(e Each, keys []any, first int) (string, []any, error) {
	if e.err != nil {
		return "", nil, e.err
	}
	paged := q.PagesEach()
	if paged {
		q = q.DefaultOrder(e.key)
		if err := q.pageEach(); err != nil {
			return "", nil, err
		}
	}
	if len(keys) != e.width {
		return "", nil, errors.New("orm: a key of the rows related to has another number of columns")
	}
	n := reflect.ValueOf(keys[0]).Len()
	if n == 0 {
		return "", nil, errors.New("orm: related rows need the keys of the rows they are loaded for")
	}
	table, args := e.keyTable(keys, n, first)
	rd, err := q.Render(e.d, len(args)+1, e.key)
	if err != nil {
		return "", nil, err
	}
	args = append(args, rd.Args()...)

	from := " FROM " + table + e.joins
	if !paged {
		return "SELECT " + e.selects + from + rd.Where() + rd.OrderBy(), args, nil
	}
	inner := "SELECT " + e.selects + ", ROW_NUMBER() OVER (PARTITION BY " + qualified(e.d, "orm_key", "orm_n") +
		" ORDER BY " + rd.orders + ") AS " + quoted(e.d, "orm_rank") + from + rd.Where()
	outer := "SELECT " + e.outputs + " FROM (" + inner + ") AS " + quoted(e.d, "orm_ranked") + " WHERE " +
		quoted(e.d, "orm_rank") + " > " + strconv.Itoa(q.offset)
	if q.hasLimit {
		outer += " AND " + quoted(e.d, "orm_rank") + " <= " + strconv.Itoa(q.offset+q.limit)
	}
	return outer + " ORDER BY " + quoted(e.d, "orm_rank"), args, nil
}

// keyTable is the table of the n keys of keys, numbered from first, and its arguments. PostgreSQL reads the columns of
// the keys as arrays, the slices of keys as they are, and MySQL a row of placeholders for each key.
func (e Each) keyTable(keys []any, n, first int) (string, []any) {
	if e.d == Postgres {
		numbers := make([]int64, n)
		for i := range numbers {
			numbers[i] = int64(first + i)
		}
		return e.keys, append([]any{numbers}, keys...)
	}
	columns := make([]reflect.Value, len(keys))
	for i, k := range keys {
		columns[i] = reflect.ValueOf(k)
	}
	var b strings.Builder
	b.Grow(len("(VALUES )") + n*(len("ROW(?), ")+len(", ?")*e.width) + len(e.keys))
	b.WriteString("(VALUES ")
	args := make([]any, 0, n*(e.width+1))
	for i := range n {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("ROW(?")
		args = append(args, first+i)
		for _, c := range columns {
			b.WriteString(", ?")
			args = append(args, c.Index(i).Interface())
		}
		b.WriteByte(')')
	}
	b.WriteByte(')')
	b.WriteString(e.keys)
	return b.String(), args
}

// Group hands rows, each the related row of the key numbers holds the number of, to the n keys: the rows of each, in
// the order they come in, as one slice of one array, each capped so that appending to one leaves the others as they
// are. A key of no rows has nil.
func Group[T any](rows []T, numbers []int, n int) [][]T {
	starts := groupStarts(numbers, n)
	sorted := make([]T, len(rows))
	next := starts[:n:n]
	for i, number := range numbers {
		sorted[next[number]] = rows[i]
		next[number]++
	}
	groups := make([][]T, n)
	for k := range n {
		start, end := groupStart(starts, k), starts[k]
		if end > start {
			groups[k] = sorted[start:end:end]
		}
	}
	return groups
}

// GroupPointers is Group, of pointers to the rows of one array.
func GroupPointers[T any](rows []T, numbers []int, n int) [][]*T {
	starts := groupStarts(numbers, n)
	sorted := make([]*T, len(rows))
	next := starts[:n:n]
	for i, number := range numbers {
		sorted[next[number]] = &rows[i]
		next[number]++
	}
	groups := make([][]*T, n)
	for k := range n {
		start, end := groupStart(starts, k), starts[k]
		if end > start {
			groups[k] = sorted[start:end:end]
		}
	}
	return groups
}

// groupStarts counts the rows of each of n numbers and returns where the rows of each start in an array of them all,
// one more than n long: the rows of k go from starts[k] to starts[k+1], until Group moves starts[k] past them.
func groupStarts(numbers []int, n int) []int {
	starts := make([]int, n+1)
	for _, number := range numbers {
		starts[number+1]++
	}
	for k := range n {
		starts[k+1] += starts[k]
	}
	return starts
}

// groupStart is where the rows of k start, once Group has moved each start past the rows of its number: where those
// of the number before end.
func groupStart(starts []int, k int) int {
	if k == 0 {
		return 0
	}
	return starts[k-1]
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

// matching is the condition joining columns, prefixed, of the table alias to the columns of the keys, in their order.
func matching(d Dialect, alias string, columns []string, prefix string) string {
	conditions := make([]string, 0, len(columns))
	for i, name := range columns {
		conditions = append(conditions,
			qualified(d, alias, prefix+name)+" = "+qualified(d, "orm_key", "orm_k"+strconv.Itoa(i)))
	}
	return strings.Join(conditions, " AND ")
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
		joins = append(joins, qualified(d, "orm_link", "orm_l_"+name)+" = "+qualified(d, b.Table, b.TargetKeys[i]))
	}
	return "SELECT " + strings.Join(selects, ", ") + " FROM " + quoted(d, b.Through), strings.Join(joins, " AND ")
}

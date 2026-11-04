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
// TargetKeys of Table. KeyTypes are the types of the columns of the keys, which PostgreSQL casts them to: one of
// pg_catalog by its name, and any other by its schema and its name, as describe records them.
type Batch struct {
	Table                                   string
	ChildKeys                               []string
	KeyTypes                                []string
	Through                                 string
	ThroughParent, ThroughChild, TargetKeys []string
}

// PagesEach reports whether q, the scope of a relation's rows, keeps only some of the related rows of each row: a
// limit or an offset, which a With method applies to the related rows of each row on its own.
func (q Of[M]) PagesEach() bool { return q.hasLimit || q.hasOffset }

// RenderEach returns the query reading the rows of q related to each of keys, the values of the columns the rows they
// are loaded for are related by, numbered from first. The database compares the keys to the columns, as SQL does
// whatever Go's == says of their values, and each row comes with the number of the key it is related to first, then
// columns. When q pages each, it keeps a page of the rows of each key, counted apart from those of the others. key is
// the primary key of the table, as Render takes it: the order a page is counted in ends in it, or is it when q has
// none.
func (q Of[M]) RenderEach(d Dialect, b Batch, key, columns []string, keys [][]any, first int) (string, []any, error) {
	paged := q.PagesEach()
	if paged {
		q = q.DefaultOrder(key)
		if err := q.pageEach(); err != nil {
			return "", nil, err
		}
	}
	matched, prefix := b.ChildKeys, ""
	if b.Through != "" {
		matched, prefix = b.ThroughParent, "orm_l_"
	}
	if len(keys) == 0 || len(matched) == 0 {
		return "", nil, errors.New("orm: related rows need the keys of the rows they are loaded for")
	}
	numbered, args, err := keyTable(d, b.KeyTypes, keys, first, len(matched))
	if err != nil {
		return "", nil, err
	}
	child := " JOIN " + quoted(d, b.Table) + " AS " + quoted(d, "orm_child") + " ON "
	from := " FROM " + numbered
	if b.Through == "" {
		from += child + matching(d, "orm_child", matched, prefix)
	} else {
		junction, on := b.throughSQL(d)
		from += " JOIN (" + junction + ") AS " + quoted(d, "orm_link") + " ON " + matching(d, "orm_link", matched, prefix) +
			child + on
	}
	rd, err := q.Render(d, len(args)+1, key)
	if err != nil {
		return "", nil, err
	}
	args = append(args, rd.Args()...)

	number := qualified(d, "orm_key", "orm_n")
	names := []string{number}
	outputs := []string{quoted(d, "orm_n")}
	for _, name := range columns {
		names = append(names, qualified(d, "orm_child", name))
		outputs = append(outputs, quoted(d, name))
	}
	if !paged {
		return "SELECT " + strings.Join(names, ", ") + from + rd.Where() + rd.OrderBy(), args, nil
	}
	inner := "SELECT " + strings.Join(names, ", ") + ", ROW_NUMBER() OVER (PARTITION BY " + number + " ORDER BY " +
		rd.orders + ") AS " + quoted(d, "orm_rank") + from + rd.Where()
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

// keyTable is keys as a table, orm_key, and its arguments: orm_n holds the number of each key, counted from first, and
// orm_k0 and on its columns, of the width each key has. PostgreSQL reads the columns of every key from an array each,
// cast to types, and MySQL a row of each key.
func keyTable(d Dialect, types []string, keys [][]any, first, width int) (string, []any, error) {
	names := make([]string, 0, 1+width)
	names = append(names, quoted(d, "orm_n"))
	for i := range width {
		names = append(names, quoted(d, "orm_k"+strconv.Itoa(i)))
	}
	alias := " AS " + quoted(d, "orm_key") + " (" + strings.Join(names, ", ") + ")"
	for _, key := range keys {
		if len(key) != width {
			return "", nil, errors.New("orm: a key of the rows related to has another number of columns")
		}
	}
	if d == MySQL {
		rows := make([]string, 0, len(keys))
		args := make([]any, 0, len(keys)*(width+1))
		for i, key := range keys {
			rows = append(rows, "ROW(?"+strings.Repeat(", ?", width)+")")
			args = append(append(args, first+i), key...)
		}
		return "(VALUES " + strings.Join(rows, ", ") + ")" + alias, args, nil
	}
	if len(types) != width {
		return "", nil, errors.New("orm: PostgreSQL casts each column of a key to its type, which the batch does not name")
	}
	numbers := make([]int64, 0, len(keys))
	columns := make([][]any, width)
	for i, key := range keys {
		numbers = append(numbers, int64(first+i))
		for j, v := range key {
			columns[j] = append(columns[j], v)
		}
	}
	arrays := []string{"$1::int8[]"}
	args := []any{numbers}
	for i, t := range types {
		// A type outside pg_catalog, which is named by its schema, may be one pgx cannot write, as an enum: its keys
		// go as text, which PostgreSQL reads as the type.
		cast := t + "[]"
		if strings.Contains(t, ".") {
			cast = "text[]::" + cast
		}
		arrays = append(arrays, "$"+strconv.Itoa(i+2)+"::"+cast)
		args = append(args, columns[i])
	}
	return "unnest(" + strings.Join(arrays, ", ") + ")" + alias, args, nil
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
		joins = append(joins, qualified(d, "orm_link", "orm_l_"+name)+" = "+qualified(d, "orm_child", b.TargetKeys[i]))
	}
	return "SELECT " + strings.Join(selects, ", ") + " FROM " + quoted(d, b.Through), strings.Join(joins, " AND ")
}

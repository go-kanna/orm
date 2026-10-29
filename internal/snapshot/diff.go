package snapshot

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Diff lists, a line for each, the tables, columns, keys, and statements in which now, what describe has just
// learned, differs from was, what the snapshot holds. It is empty when the two are the same.
func Diff(was, now Snapshot) []string {
	if was.Dialect != now.Dialect {
		return []string{fmt.Sprintf("the snapshot is of %s, but the database is %s", was.Dialect, now.Dialect)}
	}
	var lines []string
	for _, name := range union(was.Tables, now.Tables) {
		w, inWas := was.Tables[name]
		n, inNow := now.Tables[name]
		switch {
		case !inWas:
			lines = append(lines, fmt.Sprintf("table %s is in the database but not in the snapshot", name))
		case !inNow:
			lines = append(lines, fmt.Sprintf("table %s is in the snapshot but not in the database", name))
		default:
			lines = append(lines, diffTable(name, w, n)...)
		}
	}
	for _, key := range union(was.Statements, now.Statements) {
		w, inWas := was.Statements[key]
		n, inNow := now.Statements[key]
		switch {
		case !inWas:
			lines = append(lines, key+" is in the .sql files but not in the snapshot")
		case !inNow:
			lines = append(lines, key+" is in the snapshot but in no .sql file")
		case w.Digest != n.Digest:
			lines = append(lines, key+" has been rewritten since it was described")
		case !slices.Equal(w.Params, n.Params) || !slices.Equal(w.Columns, n.Columns):
			lines = append(lines, key+" is described otherwise by the database")
		}
	}
	return lines
}

func diffTable(name string, was, now Table) []string {
	var lines []string
	cols := func(t Table) map[string]Column {
		m := make(map[string]Column, len(t.Columns))
		for _, c := range t.Columns {
			m[c.Name] = c
		}
		return m
	}
	w, n := cols(was), cols(now)
	for _, c := range now.Columns {
		if _, ok := w[c.Name]; !ok {
			lines = append(lines, fmt.Sprintf("%s.%s (%s) is in the database but not in the snapshot", name, c.Name, c.Type))
		}
	}
	for _, c := range was.Columns {
		if _, ok := n[c.Name]; !ok {
			lines = append(lines, fmt.Sprintf("%s.%s is in the snapshot but not in the database", name, c.Name))
		}
	}
	for _, c := range now.Columns {
		if old, ok := w[c.Name]; ok && old != c {
			lines = append(lines, fmt.Sprintf("%s.%s is %s in the database but %s in the snapshot",
				name, c.Name, describeColumn(c), describeColumn(old)))
		}
	}
	if len(lines) == 0 && !slices.EqualFunc(was.Columns, now.Columns, func(a, b Column) bool { return a.Name == b.Name }) {
		lines = append(lines, fmt.Sprintf("the columns of %s are in another order", name))
	}

	if !slices.Equal(was.PrimaryKey, now.PrimaryKey) {
		lines = append(lines, fmt.Sprintf("the primary key of %s is not the snapshot's", name))
	}
	if !slices.EqualFunc(sortedKeys(was.UniqueKeys), sortedKeys(now.UniqueKeys), slices.Equal) {
		lines = append(lines, fmt.Sprintf("the unique keys of %s are not the snapshot's", name))
	}
	if !slices.EqualFunc(sortedForeignKeys(was.ForeignKeys), sortedForeignKeys(now.ForeignKeys), equalForeignKeys) {
		lines = append(lines, fmt.Sprintf("the foreign keys of %s are not the snapshot's", name))
	}
	return lines
}

func describeColumn(c Column) string {
	parts := []string{c.Type}
	if c.NotNull {
		parts = append(parts, "not null")
	}
	if c.Default {
		parts = append(parts, "with a default")
	}
	if c.Assigned {
		parts = append(parts, "assigned by the database")
	}
	return strings.Join(parts, ", ")
}

func sortedKeys(keys [][]string) [][]string {
	return slices.SortedFunc(slices.Values(keys), slices.Compare[[]string])
}

func sortedForeignKeys(fks []ForeignKey) []ForeignKey {
	return slices.SortedFunc(slices.Values(fks), compareForeignKeys)
}

func equalForeignKeys(a, b ForeignKey) bool {
	return compareForeignKeys(a, b) == 0
}

// union returns the names of a and b, sorted.
func union[V any](a, b map[string]V) []string {
	names := slices.Collect(maps.Keys(a))
	for name := range b {
		if _, ok := a[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

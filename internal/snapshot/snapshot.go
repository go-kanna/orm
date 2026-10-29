// Package snapshot reads and writes kanna-orm.snapshot.json, where describe records what it learned of a database so
// that generation can read it in place of the database.
package snapshot

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// Dialect names the database a snapshot was taken of.
type Dialect string

const (
	PostgreSQL Dialect = "postgresql"
	MySQL      Dialect = "mysql"
)

// Snapshot is what describe learned of a database.
type Snapshot struct {
	Dialect Dialect          `json:"dialect"`
	Tables  map[string]Table `json:"tables"`
}

// Table is a table: its columns, in the order it declares them, and its keys.
type Table struct {
	Columns     []Column     `json:"columns"`
	PrimaryKey  []string     `json:"primary_key"`
	UniqueKeys  [][]string   `json:"unique_keys"`
	ForeignKeys []ForeignKey `json:"foreign_keys"`
}

// Column is a column of a table.
type Column struct {
	Name string `json:"name"`
	// Type is the type as the database spells it, such as int8 on PostgreSQL or tinyint(1) on MySQL.
	Type    string `json:"type"`
	NotNull bool   `json:"not_null,omitzero"`
	// Default reports whether the column has a default of its own, which an insert leaving it out gets.
	Default bool `json:"default,omitzero"`
	// Assigned reports whether the database gives the column its value itself, as it does an identity, serial,
	// AUTO_INCREMENT, or generated column. kanna-orm never writes such a column.
	Assigned bool `json:"assigned,omitzero"`
}

// ForeignKey is a foreign key, its columns paired in order with the ones it references.
type ForeignKey struct {
	Columns           []string `json:"columns"`
	References        string   `json:"references"`
	ReferencedColumns []string `json:"referenced_columns"`
}

// Marshal returns s as the snapshot file holds it: tables by name, each column on a line of its own in the order the
// table declares it, and keys sorted, so that a change to the schema shows as the lines it changes.
func Marshal(s Snapshot) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "{\n  \"dialect\": %s,\n  \"tables\": {", quote(string(s.Dialect)))
	for i, name := range slices.Sorted(maps.Keys(s.Tables)) {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "\n    %s: ", quote(name))
		writeTable(&b, s.Tables[name])
	}
	if len(s.Tables) > 0 {
		b.WriteString("\n  ")
	}
	b.WriteString("}\n}\n")
	return b.Bytes()
}

func writeTable(b *bytes.Buffer, t Table) {
	const indent = "      "

	columns := make([]string, 0, len(t.Columns))
	for _, c := range t.Columns {
		columns = append(columns, column(c))
	}
	uniques := make([]string, 0, len(t.UniqueKeys))
	for _, k := range slices.SortedFunc(slices.Values(t.UniqueKeys), slices.Compare[[]string]) {
		uniques = append(uniques, names(k))
	}
	fks := make([]string, 0, len(t.ForeignKeys))
	for _, fk := range slices.SortedFunc(slices.Values(t.ForeignKeys), compareForeignKeys) {
		fks = append(fks, fmt.Sprintf(`{"columns": %s, "references": %s, "referenced_columns": %s}`,
			names(fk.Columns), quote(fk.References), names(fk.ReferencedColumns)))
	}

	b.WriteString("{\n")
	writeArray(b, indent, "columns", columns)
	fmt.Fprintf(b, ",\n%s\"primary_key\": %s,\n", indent, names(t.PrimaryKey))
	writeArray(b, indent, "unique_keys", uniques)
	b.WriteString(",\n")
	writeArray(b, indent, "foreign_keys", fks)
	b.WriteString("\n    }")
}

// writeArray writes key and its items, one to a line, or [] when there are none.
func writeArray(b *bytes.Buffer, indent, key string, items []string) {
	fmt.Fprintf(b, "%s%s: [", indent, quote(key))
	for i, item := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("\n" + indent + "  " + item)
	}
	if len(items) > 0 {
		b.WriteString("\n" + indent)
	}
	b.WriteByte(']')
}

func column(c Column) string {
	s := fmt.Sprintf(`{"name": %s, "type": %s`, quote(c.Name), quote(c.Type))
	if c.NotNull {
		s += `, "not_null": true`
	}
	if c.Default {
		s += `, "default": true`
	}
	if c.Assigned {
		s += `, "assigned": true`
	}
	return s + "}"
}

func compareForeignKeys(a, b ForeignKey) int {
	return cmp.Or(
		slices.Compare(a.Columns, b.Columns),
		cmp.Compare(a.References, b.References),
		slices.Compare(a.ReferencedColumns, b.ReferencedColumns),
	)
}

func names(ns []string) string {
	quoted := make([]string, 0, len(ns))
	for _, n := range ns {
		quoted = append(quoted, quote(n))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err) // A string always encodes.
	}
	return string(b)
}

// Unmarshal parses a snapshot file. A field it does not know is an error, since a newer kanna-orm may have written it.
func Unmarshal(data []byte) (Snapshot, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s Snapshot
	if err := dec.Decode(&s); err != nil {
		return Snapshot{}, fmt.Errorf("parse the snapshot: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Snapshot{}, errors.New("parse the snapshot: something follows it")
	}
	switch s.Dialect {
	case PostgreSQL, MySQL:
	default:
		return Snapshot{}, fmt.Errorf("the snapshot is of %q, which is neither %s nor %s", s.Dialect, PostgreSQL, MySQL)
	}
	return s, nil
}

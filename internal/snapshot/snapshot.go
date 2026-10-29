// Package snapshot reads and writes kanna-orm.snapshot.json, where describe records what it learned of a database so
// that generation can read it in place of the database.
package snapshot

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Name is the file a module keeps its snapshot in, at its root.
const Name = "kanna-orm.snapshot.json"

// Path resolves the snapshot that the -snapshot flag of a command run in dir names, or, when it names none, finds the
// one at the root of the module dir is in.
func Path(dir, flag string) (string, error) {
	if flag != "" {
		if filepath.IsAbs(flag) {
			return flag, nil
		}
		return filepath.Join(dir, flag), nil
	}
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return filepath.Join(d, Name), nil
		}
		if filepath.Dir(d) == d {
			return "", errors.New("no go.mod in " + dir + " or above it; name the snapshot with -snapshot")
		}
	}
}

// Dialect names the database a snapshot was taken of.
type Dialect string

const (
	PostgreSQL Dialect = "postgresql"
	MySQL      Dialect = "mysql"
)

// Snapshot is what describe learned of a database.
type Snapshot struct {
	Dialect    Dialect              `json:"dialect"`
	Tables     map[string]Table     `json:"tables"`
	Statements map[string]Statement `json:"statements"`
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

// Statement is what the database told of a statement written by hand.
type Statement struct {
	// Digest identifies the text the statement was described from, so that generation can tell when it has changed
	// since.
	Digest  string   `json:"digest"`
	Params  []Param  `json:"params"`
	Columns []Result `json:"columns"`
}

// Param is a parameter of a statement, in the order of its placeholders. MySQL does not tell its type.
type Param struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// Result is a column of what a statement returns. NotNull and Nullable are both false where the database cannot tell.
type Result struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	NotNull  bool   `json:"not_null,omitzero"`
	Nullable bool   `json:"nullable,omitzero"`
}

// Digest identifies the text of a statement.
func Digest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

// Marshal returns s as the snapshot file holds it: tables and statements by name, each column on a line of its own
// in the order the table or the statement has it, and keys sorted, so that a change shows as the lines it changes.
func Marshal(s Snapshot) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "{\n  \"dialect\": %s,\n", quote(string(s.Dialect)))
	writeObject(&b, "tables", s.Tables, writeTable)
	b.WriteString(",\n")
	writeObject(&b, "statements", s.Statements, writeStatement)
	b.WriteString("\n}\n")
	return b.Bytes()
}

// writeObject writes key and the values of m by their names, in order.
func writeObject[V any](b *bytes.Buffer, key string, m map[string]V, write func(*bytes.Buffer, V)) {
	fmt.Fprintf(b, "  %s: {", quote(key))
	for i, name := range slices.Sorted(maps.Keys(m)) {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(b, "\n    %s: ", quote(name))
		write(b, m[name])
	}
	if len(m) > 0 {
		b.WriteString("\n  ")
	}
	b.WriteByte('}')
}

// indent is that of a member of a table or a statement.
const indent = "      "

func writeStatement(b *bytes.Buffer, st Statement) {
	params := make([]string, 0, len(st.Params))
	for _, p := range st.Params {
		s := `{"name": ` + quote(p.Name)
		if p.Type != "" {
			s += `, "type": ` + quote(p.Type)
		}
		params = append(params, s+"}")
	}
	columns := make([]string, 0, len(st.Columns))
	for _, c := range st.Columns {
		s := fmt.Sprintf(`{"name": %s, "type": %s`, quote(c.Name), quote(c.Type))
		if c.NotNull {
			s += `, "not_null": true`
		}
		if c.Nullable {
			s += `, "nullable": true`
		}
		columns = append(columns, s+"}")
	}

	fmt.Fprintf(b, "{\n%s\"digest\": %s,\n", indent, quote(st.Digest))
	writeArray(b, "params", params)
	b.WriteString(",\n")
	writeArray(b, "columns", columns)
	b.WriteString("\n    }")
}

func writeTable(b *bytes.Buffer, t Table) {
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
	writeArray(b, "columns", columns)
	fmt.Fprintf(b, ",\n%s\"primary_key\": %s,\n", indent, names(t.PrimaryKey))
	writeArray(b, "unique_keys", uniques)
	b.WriteString(",\n")
	writeArray(b, "foreign_keys", fks)
	b.WriteString("\n    }")
}

// writeArray writes key and its items, one to a line, or [] when there are none.
func writeArray(b *bytes.Buffer, key string, items []string) {
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

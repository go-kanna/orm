package orm

import (
	"fmt"
	"strings"
)

// Parent identifies one correlated scope. Its columns cannot escape into another query.
type Parent struct{ identity *parentIdentity }
type parentIdentity byte

func NewParent() Parent { return Parent{identity: new(parentIdentity)} }

// Column is a typed reference to a column of a correlated parent.
type Column[T any] struct {
	parent Parent
	name   string
}

func NewColumn[T any](parent Parent, name string) Column[T] {
	return Column[T]{parent: parent, name: name}
}

type columnReference struct {
	parent Parent
	name   string
}
type reference interface{ columnReference() columnReference }

func (c Column[T]) columnReference() columnReference { return columnReference(c) }

// CompareColumn compares a scope column to a typed parent column; generated methods check the Go types.
func CompareColumn[M, T any](q Of[M], name, op string, other Column[T]) Of[M] {
	switch op {
	case "=", "<>", "<", "<=", ">", ">=":
	default:
		q.err = fmt.Errorf("orm: invalid column comparison %q", op)
		return q
	}
	return q.Where(name+" "+op+" ", other)
}

type parentContext struct {
	parent Parent
	alias  string
}

func qualified(d Dialect, alias, column string) string {
	return quoted(d, alias) + "." + quoted(d, column)
}
func quoted(d Dialect, name string) string {
	if d == MySQL {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return "\"" + strings.ReplaceAll(name, "\"", "\"\"") + "\""
}

func asReference(v any) (columnReference, bool) {
	if r, ok := v.(reference); ok {
		return r.columnReference(), true
	}
	r, ok := v.(columnReference)
	return r, ok
}

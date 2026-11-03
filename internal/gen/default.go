package gen

import (
	"slices"

	"github.com/go-kanna/internal/diag"

	"github.com/go-kanna/orm/internal/snapshot"
)

func (m repoMethod) hasDefaults() bool {
	return slices.ContainsFunc(m.columns, func(c column) bool { return c.insertDefault })
}

func (m repoMethod) missingInsertColumns() bool {
	return len(m.inserted()) == 0 && (m.many || !m.hasDefaults())
}

func (m repoMethod) defaultConstraints(d snapshot.Dialect) []diag.Diag {
	if d != snapshot.MySQL {
		return nil
	}
	if m.returns != returnsRow || m.verb != verbInsert && m.verb != verbUpsert {
		return nil
	}
	if _, ok := m.autoIncrement(); ok {
		return nil
	}
	key := m.table.PrimaryKey
	if m.verb == verbUpsert {
		key = m.rowKey()
	}
	for _, name := range key {
		i := slices.IndexFunc(m.columns, func(c column) bool { return c.col.Name == name })
		if i >= 0 && m.columns[i].insertDefault {
			return []diag.Diag{diag.Errorf(m.pos, "%s: MySQL cannot read the row back by %s, which default omits from INSERT",
				m.name, name).WithHints("return error alone, or provide the readback key explicitly")}
		}
	}
	return nil
}

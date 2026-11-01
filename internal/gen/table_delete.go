package gen

import (
	"go/types"
	"slices"

	"github.com/go-kanna/internal/diag"

	"github.com/go-kanna/orm/internal/snapshot"
)

func (r repoReader) tableDelete(m *repoMethod, params []*types.Var, table string) []diag.Diag {
	m.target.table, m.table = table, r.s.Tables[table]
	var ds []diag.Diag
	for _, p := range params {
		name := columnName(p.Name())
		i := slices.IndexFunc(m.table.Columns, func(c snapshot.Column) bool { return c.Name == name })
		if i < 0 {
			ds = append(ds, diag.Errorf(m.pos, "%s: %s has no column for the parameter %s", m.name, table, p.Name()))
			continue
		}
		c := m.table.Columns[i]
		if want, ok := fits(p.Type(), c.Type); !ok {
			ds = append(ds, diag.Errorf(m.pos, "%s: %s is %s, which cannot hold %s.%s (%s)",
				m.name, p.Name(), typeName(p.Type()), table, name, c.Type).WithHints("declare it as "+want))
			continue
		}
		m.keys = append(m.keys, key{param: p.Name(), col: column{path: []string{p.Name()}, typ: p.Type(), col: c}})
	}
	return ds
}

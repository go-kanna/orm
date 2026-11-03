package gen

import (
	"go/types"

	"github.com/go-kanna/internal/diag"

	"github.com/go-kanna/orm/internal/snapshot"
)

type readProjection struct {
	model   *types.Named
	columns []column
}

func (r repoReader) projectionFields(m *repoMethod) []diag.Diag {
	sources := map[string]column{}
	ambiguous := map[string]bool{}
	table := snapshot.Table{}
	for _, c := range m.columns {
		for _, name := range []string{c.col.Name, columnName(c.path[len(c.path)-1])} {
			if prior, ok := sources[name]; ok {
				if prior.col.Name != c.col.Name {
					ambiguous[name] = true
				}
				continue
			}
			sources[name] = c
			alias := c.col
			alias.Name = name
			alias.NotNull = c.col.NotNull || c.nullzero
			table.Columns = append(table.Columns, alias)
		}
	}
	matched, ds := fields(r.pkg.Fset, scope{model: m.projection.model, table: m.target.table}, table)
	for _, relation := range matched.relations {
		ds = append(ds, diag.Errorf(relation.pos, "%s: a single-table projection cannot contain relations", m.name))
	}
	seen := map[string]bool{}
	for _, c := range matched.columns {
		if ambiguous[c.col.Name] {
			ds = append(ds, diag.Errorf(c.pos, "%s: projection name %s matches multiple model columns", m.name, c.col.Name))
			continue
		}
		source := sources[c.col.Name]
		if !types.Identical(c.typ, source.typ) {
			ds = append(ds, diag.Errorf(c.pos, "%s: projection field %s has type %s; the model field has type %s",
				m.name, fieldPath(c), typeName(c.typ), typeName(source.typ)))
		}
		if seen[source.col.Name] {
			ds = append(ds, diag.Errorf(c.pos, "%s: projection selects column %s more than once", m.name, source.col.Name))
		}
		seen[source.col.Name] = true
		c.col = source.col
		c.nullzero = c.nullzero || source.nullzero
		m.projection.columns = append(m.projection.columns, c)
	}
	if len(m.projection.columns) == 0 {
		ds = append(ds, diag.Errorf(m.pos, "%s: a projection must select at least one model column", m.name))
	}
	return ds
}

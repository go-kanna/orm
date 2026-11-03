package gen

import (
	"go/types"
	"maps"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"

	"github.com/go-kanna/orm/internal/snapshot"
)

type readProjection struct {
	model   *types.Named
	columns []column
	tables  map[string]snapshot.Table
	sources []projectionSource
}

type projectionSource struct {
	table  string
	column column
	name   string
}

func (r repoReader) projectionFields(m *repoMethod) []diag.Diag {
	m.projection.tables = r.s.Tables
	sources := map[string]projectionSource{}
	ambiguous := map[string]bool{}
	table := snapshot.Table{}
	for _, c := range m.columns {
		for _, name := range []string{c.col.Name, columnName(c.path[len(c.path)-1]), m.target.table + "." + c.col.Name} {
			if prior, ok := sources[name]; ok {
				if prior.column.col.Name != c.col.Name {
					ambiguous[name] = true
				}
				continue
			}
			sources[name] = projectionSource{table: m.target.table, column: c, name: name}
			alias := c.col
			alias.Name = name
			alias.NotNull = c.col.NotNull || c.nullzero
			table.Columns = append(table.Columns, alias)
		}
	}

	if m.verb != verbDelete {
		for _, fk := range m.table.ForeignKeys {
			if fk.References == m.target.table {
				continue
			}
			for _, db := range r.s.Tables[fk.References].Columns {
				for _, name := range []string{fk.References + "." + db.Name, db.Name} {
					if prior, ok := sources[name]; ok {
						if prior.table != m.target.table && prior.table != fk.References {
							ambiguous[name] = true
						}
						continue
					}
					source := column{col: db}
					sources[name] = projectionSource{table: fk.References, column: source, name: name}
					alias := db
					alias.Name = name
					table.Columns = append(table.Columns, alias)
				}
			}
		}
	}
	// Tables without a foreign key require qualified projection tags.
	if m.verb != verbDelete {
		for _, name := range slices.Sorted(maps.Keys(r.s.Tables)) {
			if name == m.target.table {
				continue
			}
			for _, db := range r.s.Tables[name].Columns {
				qualified := name + "." + db.Name
				if _, ok := sources[qualified]; ok {
					continue
				}
				sources[qualified] = projectionSource{table: name, column: column{col: db}, name: qualified}
				alias := db
				alias.Name = qualified
				table.Columns = append(table.Columns, alias)
			}
		}
	}
	matched, ds := fields(r.pkg.Fset, scope{model: m.projection.model, table: m.target.table}, table)
	for _, relation := range matched.relations {
		ds = append(ds, diag.Errorf(relation.pos, "%s: a projection cannot contain relations", m.name))
	}
	seen := map[string]bool{}
	for _, c := range matched.columns {
		if ambiguous[c.col.Name] {
			ds = append(ds, diag.Errorf(c.pos, "%s: projection name %s matches multiple model columns", m.name, c.col.Name))
			continue
		}
		selected := sources[c.col.Name]
		source := selected.column
		if source.typ != nil && !types.Identical(c.typ, source.typ) {
			ds = append(ds, diag.Errorf(c.pos, "%s: projection field %s has type %s; the model field has type %s",
				m.name, fieldPath(c), typeName(c.typ), typeName(source.typ)))
		}
		identity := selected.table + "." + source.col.Name
		if seen[identity] {
			ds = append(ds, diag.Errorf(c.pos, "%s: projection selects column %s more than once", m.name, source.col.Name))
		}
		seen[identity] = true
		c.col = source.col
		c.nullzero = c.nullzero || source.nullzero
		m.projection.columns = append(m.projection.columns, c)
		if strings.Contains(selected.name, ".") {
			selected.name = ""
		}
		m.projection.sources = append(m.projection.sources, selected)
	}
	if len(m.projection.columns) == 0 {
		ds = append(ds, diag.Errorf(m.pos, "%s: a projection must select at least one model column", m.name))
	}
	return ds
}

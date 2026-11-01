package gen

import (
	"go/types"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"

	"github.com/go-kanna/orm/internal/snapshot"
)

// readModel is a model whose whole rows the repositories read: one a method reads, or one a relation of another
// reaches. Each has a function reading its rows, and one loading its relations.
type readModel struct {
	target  indexed
	model   *types.Named
	table   snapshot.Table
	columns []column
	links   []link
}

// readModels finds the models repos read whole rows of, and those their relations reach, by table. It reports a
// relation it cannot load, which the scope type of the model still offers to.
func (r repoReader) readModels(repos []repository) (map[string]readModel, []diag.Diag) {
	models := map[string]readModel{}
	var queue []readModel
	for _, repo := range repos {
		for _, m := range repo.methods {
			if m.verb == verbGet || m.verb == verbFirst || m.verb == verbList {
				queue = append(queue, readModel{target: m.target, model: m.model, table: m.table})
			}
		}
	}

	var ds []diag.Diag
	for len(queue) > 0 {
		rm := queue[0]
		queue = queue[1:]
		if _, ok := models[rm.target.table]; ok {
			continue
		}
		sc := scope{name: rm.target.name, model: rm.model, table: rm.target.table}
		f, _ := fields(r.pkg.Fset, sc, rm.table) // the run generating for the scope type reports what is wrong
		rm.columns = f.columns
		links, _ := r.l.link(sc, f.columns, f.relations)
		for _, lk := range links {
			if lk.scalar != nil {
				rm.links = append(rm.links, lk)
				continue
			}
			if d, ok := loadable(rm.model, lk); !ok {
				ds = append(ds, d)
				continue
			}
			rm.links = append(rm.links, lk)
			queue = append(queue, readModel{target: lk.target, model: lk.model, table: r.s.Tables[lk.target.table]})
		}
		models[rm.target.table] = rm
	}
	return models, ds
}

// loadable reports what keeps lk, a relation of model, from being loaded: keys of types unlike, or a key no map can be
// keyed by.
func loadable(model *types.Named, lk link) (diag.Diag, bool) {
	label := model.Obj().Name() + "." + strings.Join(lk.field.path, ".")
	field := func(m *types.Named, c column) string {
		return m.Obj().Name() + "." + strings.Join(c.path, ".")
	}
	// Through a table between, the keys of each side are their own, and may be of a column each or not.
	if lk.way.kind != manyToMany {
		for i := range lk.keys {
			if own, other := elemOf(lk.keys[i].typ), elemOf(lk.targetKeys[i].typ); !types.Identical(own, other) {
				return diag.Errorf(lk.field.pos, "%s: %s is %s %s, but %s, which it is loaded by, is %s %s", label,
					field(model, lk.keys[i]), article(typeName(own)), typeName(own), field(lk.model, lk.targetKeys[i]),
					article(typeName(other)), typeName(other)).WithHints("declare them alike"), false
			}
		}
	}
	for _, c := range slices.Concat(lk.keys, lk.targetKeys) {
		if !types.Comparable(elemOf(c.typ)) {
			return diag.Errorf(lk.field.pos, "%s: its keys are of a type no map can be keyed by", label), false
		}
	}
	return diag.Diag{}, true
}

// readNames names the functions reading the rows of each of models and loading their relations, by table.
func readNames(models map[string]readModel) map[string]string {
	names := map[string]string{}
	for table, rm := range models {
		name, ok := funcName(table)
		if !ok {
			name = rm.target.name
		}
		names[table] = name
	}
	return names
}

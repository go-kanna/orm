package gen

import (
	"go/types"
	"path/filepath"
	"strconv"
	"strings"
)

func (e emitter) selectionMethods() {
	model := types.TypeString(e.p.model, e.f.Qualifier)
	e.printf("func (s %s) One() %s.Single[%s] { return %s.One() }\n", e.p.name, e.orm, model, e.q("s"))
	e.printf("func (s %s) First() %s.Single[%s] { return %s.First() }\n", e.p.name, e.orm, model, e.q("s"))
	e.printf("func (s %s) Many() %s.Many[%s] { return %s.Many() }\n\n", e.p.name, e.orm, model, e.q("s"))
}

func (e emitter) relationModel(lk link) string {
	if lk.model != nil {
		return types.TypeString(lk.model, e.f.Qualifier)
	}
	model := lk.target.modelName
	if pkg := e.f.Import(lk.target.modelPath, filepath.Base(lk.target.modelPath)); pkg != "" {
		model = pkg + "." + model
	}
	return model
}

func (e emitter) selection(lk link) {
	name := lk.field.path[len(lk.field.path)-1]
	model := e.relationModel(lk)
	e.printf("func (s %s) Select%s(build func(%sColumns) %s.Single[%s]) %s {\n", e.p.name, name, e.p.name, e.orm,
		model, e.p.name)
	e.printf("parent := %s.NewParent()\nsub := build(%sColumnsFor(parent))\n", e.orm, lowerFirst(e.p.name))
	e.printf("return %s{%s.Select(%s, %q, %q, %q, parent, %s, %s, sub)}\n}\n\n",
		e.p.name, e.orm, e.q("s"), strings.Join(lk.field.path, "."), lk.target.table, lk.scalar.col.Name,
		columnNamesLiteral(lk.keys), columnNamesLiteral(lk.targetKeys))
}

func columnNamesLiteral(cs []column) string {
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		names = append(names, strconv.Quote(c.col.Name))
	}
	return "[]string{" + strings.Join(names, ",") + "}"
}
func namesLiteral(names []string) string {
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, strconv.Quote(name))
	}
	return "[]string{" + strings.Join(result, ",") + "}"
}

package gen

import (
	"fmt"
	"go/types"
	"strconv"
	"strings"
)

func (e emitter) collection(lk link) {
	name := lk.field.path[len(lk.field.path)-1]
	model := e.relationModel(lk)
	e.printf("func (s %s) Preload%s(build func(%sColumns) %s.Many[%s]) %s {\n",
		e.p.name, name, e.p.name, e.orm, model, e.p.name)
	e.printf("parent := %s.NewParent()\nreturn %s{%s.PreloadMany(%s, %q, parent, build(%sColumnsFor(parent)))}\n}\n\n",
		e.orm, e.p.name, e.orm, e.q("s"), strings.Join(lk.field.path, "."), lowerFirst(e.p.name))
}

func (e repoEmitter) preloadCollection(lk link, n preloadNames) {
	if lk.scalar != nil || (lk.way.kind != hasMany && lk.way.kind != manyToMany) {
		return
	}
	field := strings.Join(lk.field.path, ".")
	model := types.TypeString(lk.model, e.f.Qualifier)
	e.printf("if sub, parent, ok := %s.Batched[%s](%s, %q); ok {\n", e.ormPkg(), model, n.q, field)
	e.printf("type parentKey struct { %s }\n", e.keyFields(lk.keys))
	e.printf("keys := []parentKey{}\nfor i := range %s {\n%s\n}\n", n.rows, keyOf("parentKey", "key", n.rows+
		"[i]", lk.keys, func(k string) string { return "keys=append(keys," + k + ")" }))
	e.printf("flat := []%s{}\nowners := []parentKey{}\n", model)
	cols := e.models[lk.target.table].columns
	config := e.ormPkg() + ".Batch{ParentTable:" + strconv.Quote(lk.way.fk.References) + ",Table:" +
		strconv.Quote(lk.target.table) + ",ParentKeys:" + columnNamesLiteral(lk.keys) + ",ChildKeys:" +
		columnNamesLiteral(lk.targetKeys)
	if lk.way.kind == manyToMany {
		config += ",Through:" + strconv.Quote(lk.way.table) + ",ThroughParent:" + namesLiteral(lk.way.fk.Columns) +
			",ThroughChild:" + namesLiteral(lk.way.throughFK.Columns) + ",TargetKeys:" + columnNamesLiteral(lk.targetKeys)
	}
	manifests := []string{}
	for _, c := range e.models[lk.target.table].projections {
		manifests = append(manifests, fmt.Sprintf("{Field:%q,Column:%q,Optional:%t}", fieldPath(c), c.col.Name,
			nilable(c.typ)))
	}
	if len(manifests) > 0 {
		config += ",Projections:[]" + e.ormPkg() + ".Projection{" + strings.Join(manifests, ",") + "}"
	}
	config += "}"
	e.printf("for chunk := range %s.Chunk(keys,%d) {\ninputs:=make([][]any,len(chunk))\nfor"+
		" i,key:=range chunk { inputs[i]=[]any{", e.f.Import("slices", "slices"), max(1, inChunk/len(lk.keys)))
	for i := range lk.keys {
		e.printf("key.k%d,", i)
	}
	e.printf("} }\ntext,args,err:=sub.RenderBatch(%s,%s,parent,%s,inputs)\nif err!=nil{return"+
		" err}\n", e.dialect(), config, columnNamesLiteral(cols))
	e.printf("related,err:=%s.%s(%s,text,args...)\nif err!=nil{return %s.Errorf(%q,err)}\n", n.db, e.query,
		n.ctx, e.fmtPkg(), "preload "+field+": %w")
	e.printf("for related.Next(){ var key parentKey\nvar row %s\n", model)
	scanScope := e.f.Scope()
	scanColumns := append([]column(nil), cols...)
	for _, c := range e.models[lk.target.table].projections {
		c.nullzero = true
		c.selectedBy = "sub"
		c.selectionReturn = "related.Close();return err"
		scanColumns = append(scanColumns, c)
	}
	dests, assign := e.scanTargets(scanScope, "row", scanColumns)
	var keys []string
	for i := range lk.keys {
		keys = append(keys, fmt.Sprintf("&key.k%d", i))
	}
	e.printf("if err:=related.Scan(%s);err!=nil{related.Close();return %s.Errorf(%q,err)}\n",
		strings.Join(append(keys, dests...), ","), e.fmtPkg(), "scan "+field+": %w")
	e.assignScanned(assign)
	e.printf("flat=append(flat,row)\nowners=append(owners,key)\n}\nrelated.Close()\nif" +
		" err:=related.Err();err!=nil{return err}\n}\n")
	if len(e.models[lk.target.table].links) > 0 {
		e.printf("if err:=preload%s(%s,%s,flat,sub);err!=nil{return err}\n", e.names[lk.target.table], n.ctx, n.db)
	}
	slice, ok := lk.field.typ.Underlying().(*types.Slice)
	if !ok {
		return
	}
	elem := types.TypeString(slice.Elem(), e.f.Qualifier)
	value := "flat[i]"
	if isPointer(slice.Elem()) {
		value = "&flat[i]"
	}
	e.printf("grouped:=map[parentKey][]%s{}\nfor i:=range"+
		" flat{grouped[owners[i]]=append(grouped[owners[i]],%s)}\n", elem, value)
	e.printf("for i:=range %s{ %s[i].%s=[]%s{}\n%s\n}\n}\n", n.rows, n.rows, field, elem, keyOf("parentKey",
		"key", n.rows+"[i]", lk.keys, func(k string) string {
			return "if values,ok:=grouped[" + k + "];ok{" + n.rows + "[i]." + field + "=values}"
		}))
}

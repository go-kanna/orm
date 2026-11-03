package gen

import (
	"fmt"
	"go/types"
	"strconv"
	"strings"
)

// preloadEach writes the block loading a page of the rows of lk, a relation to many rows, for each row, when its scope
// keeps only some of them: one query a chunk of rows, which counts the related rows of each row on its own.
func (e repoEmitter) preloadEach(lk link, n preloadNames) {
	if lk.scalar != nil || (lk.way.kind != hasMany && lk.way.kind != manyToMany) {
		return
	}
	field := strings.Join(lk.field.path, ".")
	model := types.TypeString(lk.model, e.f.Qualifier)
	target := e.models[lk.target.table]
	e.printf("if sub, ok := %s[%s](%s, %q); ok && sub.PagesEach() {\n", e.ref("Preloaded"), model, n.q, field)
	e.printf("type parentKey struct { %s }\n", e.keyFields(lk.keys))
	e.printf("keys := []parentKey{}\nfor i := range %s {\n%s\n}\n", n.rows, keyOf("parentKey", "key", n.rows+
		"[i]", lk.keys, func(k string) string { return "keys=append(keys," + k + ")" }))
	e.printf("flat := []%s{}\nowners := []parentKey{}\n", model)
	batch := e.ref("Batch") + "{Table:" + strconv.Quote(lk.target.table) + ",ChildKeys:" +
		columnNamesLiteral(lk.targetKeys)
	if lk.way.kind == manyToMany {
		batch += ",Through:" + strconv.Quote(lk.way.table) + ",ThroughParent:" + namesLiteral(lk.way.fk.Columns) +
			",ThroughChild:" + namesLiteral(lk.way.throughFK.Columns) + ",TargetKeys:" + columnNamesLiteral(lk.targetKeys)
	}
	batch += "}"
	e.printf("for chunk := range %s.Chunk(keys,%d) {\ninputs:=make([][]any,len(chunk))\nfor"+
		" i,key:=range chunk { inputs[i]=[]any{", e.f.Import("slices", "slices"), max(1, inChunk/len(lk.keys)))
	for i := range lk.keys {
		e.printf("key.k%d,", i)
	}
	e.printf("} }\ntext,args,err:=sub.RenderEach(%s,%s,%s,%s,inputs)\nif err!=nil{return"+
		" %s.Errorf(%q,err)}\n", e.dialect(), batch, e.orderKey(target.table), columnNamesLiteral(target.columns),
		e.fmtPkg(), "load "+field+": %w")
	e.printf("related,err:=%s.%s(%s,text,args...)\nif err!=nil{return %s.Errorf(%q,err)}\n", n.db, e.query,
		n.ctx, e.fmtPkg(), "load "+field+": %w")
	e.printf("for related.Next(){ var key parentKey\nvar row %s\n", model)
	dests, assign := e.scanTargets(e.f.Scope(), "row", target.columns)
	keys := make([]string, 0, len(lk.keys))
	for i := range lk.keys {
		keys = append(keys, fmt.Sprintf("&key.k%d", i))
	}
	e.printf("if err:=related.Scan(%s);err!=nil{related.Close();return %s.Errorf(%q,err)}\n",
		strings.Join(append(keys, dests...), ","), e.fmtPkg(), "load "+field+": %w")
	e.assignScanned(assign)
	e.printf("flat=append(flat,row)\nowners=append(owners,key)\n}\nrelated.Close()\nif"+
		" err:=related.Err();err!=nil{return %s.Errorf(%q,err)}\n}\n", e.fmtPkg(), "load "+field+": %w")
	if len(target.links) > 0 {
		e.printf("if err:=preload%s(%s,%s,flat,sub);err!=nil{return err}\n", e.names[lk.target.table], n.ctx, n.db)
	}
	slice, ok := lk.field.typ.Underlying().(*types.Slice)
	if !ok {
		e.printf("}\n")
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

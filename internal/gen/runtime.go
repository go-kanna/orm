package gen

import "github.com/go-kanna/internal/gofile"

const (
	// ormPath is the import path of what code written by hand uses of the runtime.
	ormPath = "github.com/go-kanna/orm"
	// implPath is the import path of what the generated code runs on.
	implPath = "github.com/go-kanna/orm/impl"
)

// ormNames are the names of the runtime that code written by hand reads, which the generated code names as it does.
var ormNames = map[string]bool{
	"Of": true, "Single": true, "Many": true, "Column": true, "Option": true, "QueryLog": true, "QueryLogger": true,
	"WithQueryLogger": true, "ErrNotFound": true, "ErrRequiredRelation": true,
}

// runtimeRef names name of the runtime in f, importing the package it is read from only once it is named, so that f
// imports no package it does not use.
func runtimeRef(f *gofile.File, name string) string {
	if ormNames[name] {
		return f.Import(ormPath, "orm") + "." + name
	}
	return f.Import(implPath, "impl") + "." + name
}

package gen

import (
	"path"

	"github.com/go-kanna/internal/gofile"
)

const (
	// ormPath is the import path of what code written by hand uses of the runtime.
	ormPath = "github.com/go-kanna/orm"
	// scopePath is the import path of the package of scope.Of, the one field of a scope type.
	scopePath = "github.com/go-kanna/orm/scope"
	// implPath is the import path of what the generated code runs on.
	implPath = "github.com/go-kanna/orm/impl"
)

// publicNames are the names of the runtime that code written by hand reads, by the import path it reads each from,
// which the generated code names as it does. Any other name is of impl.
var publicNames = map[string]string{
	"Of": scopePath, "Single": ormPath, "Many": ormPath, "Column": ormPath, "Option": ormPath, "QueryLog": ormPath,
	"QueryLogger": ormPath, "WithQueryLogger": ormPath, "ErrNotFound": ormPath, "ErrRequiredRelation": ormPath,
}

// runtimeRef names name of the runtime in f, importing the package it is read from only once it is named, so that f
// imports no package it does not use.
func runtimeRef(f *gofile.File, name string) string {
	pkg, ok := publicNames[name]
	if !ok {
		pkg = implPath
	}
	return f.Import(pkg, path.Base(pkg)) + "." + name
}

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
	// optionPath is the import path of the options of a repository.
	optionPath = "github.com/go-kanna/orm/option"
	// changePath is the import path of change.Set, the columns a change set has an update write.
	changePath = "github.com/go-kanna/orm/change"
	// implPath is the import path of what the generated code runs on.
	implPath = "github.com/go-kanna/orm/impl"
	// pgxPath and stdlibPath are the import paths of the DBTX the repositories run on, by pgx and by database/sql.
	pgxPath    = "github.com/go-kanna/orm/pgx"
	stdlibPath = "github.com/go-kanna/orm/stdlib"
)

// publicNames are the names of the runtime that code written by hand reads, or the API generated code exports shows,
// by the import path each is read from. The generated code names them as such. Any other name is of impl.
var publicNames = map[string]string{
	"Of": scopePath, "Option": optionPath, "Set": changePath, "ErrNotFound": ormPath,
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

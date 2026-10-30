// Package gomod finds the module a directory is in, the import path of the package in a directory, and the
// directories the go command leaves out of ./....
package gomod

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

// Of returns the root of the module dir is in, and the module's path.
func Of(dir string) (root, modPath string, err error) {
	for d := dir; ; d = filepath.Dir(d) {
		data, err := os.ReadFile(filepath.Join(d, "go.mod")) //nolint:gosec // the go.mod of the module
		switch {
		case err == nil:
			if modPath = modfile.ModulePath(data); modPath == "" {
				return "", "", fmt.Errorf("%s names no module", filepath.Join(d, "go.mod"))
			}
			return d, modPath, nil
		case !errors.Is(err, fs.ErrNotExist):
			return "", "", fmt.Errorf("find the module of %s: %w", dir, err)
		case filepath.Dir(d) == d:
			return "", "", fmt.Errorf("no go.mod in %s or above it", dir)
		}
	}
}

// PkgPath returns the import path of the package in dir, which need not exist yet, from the module dir is in.
func PkgPath(dir string) (string, error) {
	root, modPath, err := Of(dir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return "", err //nolint:wrapcheck // the caller reports it at dir
	}
	return strings.TrimSuffix(modPath+"/"+filepath.ToSlash(rel), "/."), nil
}

// Skip reports whether the go command leaves the directory at path, named name, out of ./...: one starting with . or
// _, testdata, vendor, and the root of another module.
func Skip(path, name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor" {
		return true
	}
	_, err := os.Stat(filepath.Join(path, "go.mod"))
	return err == nil
}

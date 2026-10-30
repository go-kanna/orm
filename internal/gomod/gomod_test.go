package gomod_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/orm/internal/gomod"
)

// write writes the files of files, by their paths under dir.
func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, data := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOf(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, map[string]string{
		"go.mod":         "module example.com/shop\n\ngo 1.26\n",
		"store/store.go": "package store\n",
		"bad/go.mod":     "go 1.26\n",
	})

	root, modPath, err := gomod.Of(filepath.Join(dir, "store"))
	if err != nil || root != dir || modPath != "example.com/shop" {
		t.Errorf("Of(store) = %q, %q, %v, want %q, example.com/shop", root, modPath, err, dir)
	}
	if _, _, err := gomod.Of(filepath.Join(dir, "bad")); err == nil || !strings.Contains(err.Error(), "names no module") {
		t.Errorf("Of(bad) = %v, want an error naming no module", err)
	}
	if _, _, err := gomod.Of(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no go.mod") {
		t.Errorf("Of(a directory of no module) = %v, want an error finding no go.mod", err)
	}
}

func TestPkgPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, map[string]string{"go.mod": "module example.com/shop\n\ngo 1.26\n"})

	tests := []struct{ dir, want string }{
		{".", "example.com/shop"},
		{"store", "example.com/shop/store"},
		{"internal/store", "example.com/shop/internal/store"}, // which need not exist
	}
	for _, tt := range tests {
		if got, err := gomod.PkgPath(filepath.Join(dir, tt.dir)); err != nil || got != tt.want {
			t.Errorf("PkgPath(%s) = %q, %v, want %q", tt.dir, got, err, tt.want)
		}
	}
}

func TestSkip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write(t, dir, map[string]string{"tools/go.mod": "module example.com/tools\n\ngo 1.26\n"})

	tests := []struct {
		name string
		want bool
	}{
		{"store", false},
		{".git", true},
		{"_old", true},
		{"testdata", true},
		{"vendor", true},
		{"tools", true}, // the root of another module
	}
	for _, tt := range tests {
		if got := gomod.Skip(filepath.Join(dir, tt.name), tt.name); got != tt.want {
			t.Errorf("Skip(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

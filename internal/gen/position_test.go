package gen_test

import (
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/orm/internal/gen"
)

func TestFieldPosition(t *testing.T) {
	t.Parallel()
	const src = `package model

type Order struct {
	ID    int64
	*Audit
	other.Base
	Page[int]
	Pair[int, string]
}
`
	name := filepath.Join(t.TempDir(), "model.go")
	if err := os.WriteFile(name, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	// A fake file of lines alone, as export data holds a package.
	fset := token.NewFileSet()
	file := fset.AddFile(name, -1, len(src))
	var lines []int
	for i := range len(src) {
		if i == 0 || src[i-1] == '\n' {
			lines = append(lines, i)
		}
	}
	file.SetLines(lines)
	field := func(pos token.Pos, name string) token.Position {
		return gen.FieldPosition(fset, types.NewField(pos, nil, name, types.Typ[types.Int], false))
	}
	for _, tt := range []struct {
		line   int
		field  string
		column int
	}{
		{4, "ID", 2}, {5, "Audit", 3}, {6, "Base", 8}, {7, "Page", 2}, {8, "Pair", 2},
		{4, "Audit", 1}, // no field of the name on the line
	} {
		if got := field(file.LineStart(tt.line), tt.field); got.Line != tt.line || got.Column != tt.column {
			t.Errorf("%s on line %d at %d:%d, want %d:%d", tt.field, tt.line, got.Line, got.Column, tt.line, tt.column)
		}
	}

	// A file it cannot read keeps the position of export data.
	broken := fset.AddFile(filepath.Join(t.TempDir(), "gone.go"), -1, 10)
	if got := field(broken.Pos(0), "ID"); got.Column != 1 {
		t.Errorf("a file not there at %v, want column 1", got)
	}

	// A position of the source is kept as it is.
	if got := field(file.LineStart(4)+3, "X"); !strings.HasSuffix(got.String(), ":4:4") {
		t.Errorf("a column of the source at %v, want 4:4", got)
	}
}

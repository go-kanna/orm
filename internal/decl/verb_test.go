package decl_test

import (
	"testing"

	"github.com/go-kanna/orm/internal/decl"
)

func TestVerbOf(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		verb   decl.Verb
		known  bool
		writes bool
	}{
		{"GetByNumber", decl.VerbGet, true, false},
		{"Getaway", "", false, false},
		{"Insert", decl.VerbInsert, true, true},
		{"Upsert2", decl.VerbUpsert, true, true},
		{"CopyApprovals", decl.VerbCopy, true, true},
		{"Tag", "", false, false},
	} {
		v, ok := decl.VerbOf(tt.name)
		if v != tt.verb || ok != tt.known || v.Writes() != tt.writes {
			t.Errorf("VerbOf(%q) = %q, %t, writes %t; want %q, %t, writes %t",
				tt.name, v, ok, v.Writes(), tt.verb, tt.known, tt.writes)
		}
	}
}

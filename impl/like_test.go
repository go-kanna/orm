package impl_test

import (
	"testing"

	"github.com/go-kanna/orm/impl"
)

func TestEscapeLike(t *testing.T) {
	t.Parallel()

	for s, want := range map[string]string{
		"":          "",
		"plain":     "plain",
		"50%":       "50!%",
		"a_b":       "a!_b",
		"wow!":      "wow!!",
		`C:\dir`:    `C:\dir`,
		"!%_ mixed": "!!!%!_ mixed",
	} {
		if got := impl.EscapeLike(s); got != want {
			t.Errorf("EscapeLike(%q) = %q, want %q", s, got, want)
		}
	}
}

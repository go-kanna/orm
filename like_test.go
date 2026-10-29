package orm_test

import (
	"testing"

	"github.com/go-kanna/orm"
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
		if got := orm.EscapeLike(s); got != want {
			t.Errorf("EscapeLike(%q) = %q, want %q", s, got, want)
		}
	}
}

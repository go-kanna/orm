package orm

import "strings"

var likeEscaper = strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")

// EscapeLike escapes what a LIKE pattern gives a meaning, % and _, and the escape character itself, !, so that s
// matches only itself in a pattern that ends in ESCAPE '!'. ! rather than a backslash, since MySQL reads a backslash
// in a string differently by its NO_BACKSLASH_ESCAPES mode.
func EscapeLike(s string) string {
	return likeEscaper.Replace(s)
}

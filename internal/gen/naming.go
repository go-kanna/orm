package gen

import (
	"strings"
	"unicode"
)

// columnName returns the column a field holds unless its tag says otherwise: the field's name in snake case. A word
// starts at an upper-case letter after a lower-case one or a digit, and at the last of a run of upper-case letters that
// a lower-case one follows, so that CustomerID holds customer_id and HTTPServer http_server. There is no list of
// acronyms to consult: OAuthToken holds o_auth_token, and a field named otherwise says so with a tag.
func columnName(field string) string {
	runes := []rune(field)
	var b strings.Builder
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

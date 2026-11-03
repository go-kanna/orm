package gen

import (
	"strconv"
	"strings"
)

// columnNamesLiteral is the names of the columns of cs as a []string literal.
func columnNamesLiteral(cs []column) string {
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		names = append(names, c.col.Name)
	}
	return namesLiteral(names)
}

// namesLiteral is names as a []string literal.
func namesLiteral(names []string) string {
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, strconv.Quote(name))
	}
	return "[]string{" + strings.Join(result, ",") + "}"
}

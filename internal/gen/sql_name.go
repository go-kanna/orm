package gen

import (
	_ "embed"
	"strings"

	"github.com/go-kanna/orm/internal/snapshot"
)

// The reserved words of each database, which a name must be quoted to be. They are what the databases answer:
//
//	SELECT word FROM pg_get_keywords() WHERE catcode IN ('R', 'T') ORDER BY 1
//	SELECT lower(word) FROM information_schema.keywords WHERE reserved = 1 ORDER BY 1
var (
	//go:embed keywords/postgresql.txt
	postgresKeywords string
	//go:embed keywords/mysql.txt
	mysqlKeywords string

	reserved = map[snapshot.Dialect]map[string]bool{
		snapshot.PostgreSQL: words(postgresKeywords),
		snapshot.MySQL:      words(mysqlKeywords),
	}
)

func words(list string) map[string]bool {
	m := map[string]bool{}
	for w := range strings.FieldsSeq(list) {
		m[w] = true
	}
	return m
}

// sqlName writes the name of a table or a column as SQL for d: as it is where it can be, and quoted where it is a
// reserved word or has a character other than a lower-case letter, a digit, or an underscore.
func sqlName(d snapshot.Dialect, name string) string {
	if plain(name) && !reserved[d][name] {
		return name
	}
	q := `"`
	if d == snapshot.MySQL {
		q = "`"
	}
	return q + strings.ReplaceAll(name, q, q+q) + q
}

func plain(name string) bool {
	if name == "" || name[0] >= '0' && name[0] <= '9' {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

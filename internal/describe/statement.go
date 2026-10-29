package describe

import (
	"fmt"

	"github.com/go-kanna/orm/internal/snapshot"
)

// distinctNames reports a name two result columns share, since the fields take the columns by name.
func distinctNames(columns []snapshot.Result) error {
	seen := map[string]bool{}
	for _, c := range columns {
		if seen[c.Name] {
			return fmt.Errorf("two columns are named %s: give them names of their own with AS", c.Name)
		}
		seen[c.Name] = true
	}
	return nil
}

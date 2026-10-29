package describe

import (
	"slices"

	"github.com/go-kanna/orm/internal/snapshot"
)

// mergeUniqueKeys makes one key of a unique constraint and a unique index on the same columns.
func mergeUniqueKeys(tables map[string]snapshot.Table) {
	for name, t := range tables {
		slices.SortFunc(t.UniqueKeys, slices.Compare)
		t.UniqueKeys = slices.CompactFunc(t.UniqueKeys, slices.Equal)
		tables[name] = t
	}
}

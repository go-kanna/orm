package gen

import (
	"fmt"
	"slices"
	"strings"
)

func (m *repoMethod) conflictOptions(options []string) error {
	seen := map[string]bool{}
	for _, option := range options {
		key, value, ok := strings.Cut(option, "=")
		if !ok || value == "" || key != "keep" && key != "coalesce" && key != "do" {
			return fmt.Errorf("//kanna:conflict takes keep= or coalesce= or do=nothing after the columns of the key, not %q",
				option)
		}
		if seen[key] {
			return fmt.Errorf("//kanna:conflict gives %s= twice", key)
		}
		seen[key] = true
		if key == "do" {
			if value != "nothing" {
				return fmt.Errorf("do= supports only nothing, not %q", value)
			}
			m.doNothing = true
			continue
		}
		columns := strings.Split(value, ",")
		unique := map[string]bool{}
		for _, name := range columns {
			if name == "" || unique[name] {
				return fmt.Errorf("%s= must name each column once", key)
			}
			unique[name] = true
		}
		if key == "keep" {
			m.keep = columns
		} else {
			m.coalesce = columns
		}
	}
	return nil
}

func (e repoEmitter) upsertAssignment(m repoMethod, c column, incoming, existing string) string {
	if slices.Contains(m.coalesce, c.col.Name) {
		incoming = "COALESCE(" + incoming + ", " + existing + ")"
	}
	return sqlName(e.d, c.col.Name) + " = " + incoming
}

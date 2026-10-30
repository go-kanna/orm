package gen

import "github.com/go-kanna/orm/internal/decl"

// columnName returns the column a field holds unless its tag says otherwise: the field's name in snake case, as
// decl.SnakeCase writes it, so that CustomerID holds customer_id. A field named otherwise says so with a tag.
func columnName(field string) string {
	return decl.SnakeCase(field)
}

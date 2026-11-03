// Package join constructs explicit conditions for joined read projections.
package join

// Condition compares a base-table column with a joined-table column for equality.
type Condition struct {
	BaseColumn, JoinedColumn string
}

// On names the base and joined columns of an equality in a JOIN condition.
func On(baseColumn, joinedColumn string) Condition {
	return Condition{BaseColumn: baseColumn, JoinedColumn: joinedColumn}
}

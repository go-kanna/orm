// Package change holds Set, the columns an update writes, as a generated change set hands them to the repository
// running the update.
package change

import "github.com/go-kanna/orm/impl"

// Set is the columns an update writes, and their values. The Of method of a generated change set returns it.
type Set[M any] = impl.Changes[M]

// Package change holds Set, which a generated change set uses to hand the columns an update writes to the repository
// that runs the update.
package change

import "github.com/go-kanna/orm/impl"

// Set holds the columns an update writes, and their values. The Of method of a generated change set returns it, and
// only the change set's setters write to it.
type Set[M any] impl.Changes[M]

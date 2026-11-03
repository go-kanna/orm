// Package scope holds Of, the one field of a scope type.
package scope

import "github.com/go-kanna/orm/impl"

// Of is the scope of a query reading Ms: its conditions, the order of its rows, and how many of them it keeps. A scope
// type holds one in an unexported field, so that nothing but its methods narrows it.
type Of[M any] = impl.Of[M]

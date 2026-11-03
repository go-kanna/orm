// Package orm holds the errors a generated repository returns. A scope type holds a scope.Of, a relation is selected
// by the types of package relation, a repository is configured by package option, and what the generated code runs
// on is in package impl.
package orm

import "errors"

// ErrNotFound is what a repository returns, wrapped, for a row that is not there.
var ErrNotFound = errors.New("orm: not found")

// ErrRequiredRelation is what a repository returns, wrapped, for a required selection that has no value.
var ErrRequiredRelation = errors.New("orm: required relation has no value")

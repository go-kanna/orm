package orm

import "errors"

// ErrNotFound is what a repository returns, wrapped, for a row that is not there.
var ErrNotFound = errors.New("orm: not found")

// ErrRequiredRelation is what a repository returns, wrapped, for a required selection that has no value.
var ErrRequiredRelation = errors.New("orm: required relation has no value")

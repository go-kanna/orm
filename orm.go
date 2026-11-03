// Package orm holds the errors a repository kanna-orm generates returns. What else code written by hand uses is in
// its packages:
//
//   - [github.com/go-kanna/orm/scope]: Of, the one field of a scope type declared by hand.
//   - [github.com/go-kanna/orm/relation]: what the closures given to a scope's Select and Preload methods return and
//     read.
//   - [github.com/go-kanna/orm/join]: the explicit conditions of a joined projection.
//   - [github.com/go-kanna/orm/option]: the options a repository's constructor takes.
//   - [github.com/go-kanna/orm/hook]: the functions those options take, called around each statement.
//
// What the generated code runs on is in [github.com/go-kanna/orm/impl], which only the generated code imports.
package orm

import "errors"

// ErrNotFound is what a repository returns, wrapped, for a row that is not there.
var ErrNotFound = errors.New("orm: not found")

// ErrRequiredRelation is what a repository returns, wrapped, for a required selection that has no value.
var ErrRequiredRelation = errors.New("orm: required relation has no value")

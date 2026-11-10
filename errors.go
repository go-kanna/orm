package orm

import "errors"

// ErrNotFound is what a repository returns, wrapped, for a row that is not there.
var ErrNotFound = errors.New("orm: not found")

// ErrInvalidCursor is what AfterCursor of a scope returns, wrapped, for a cursor it cannot continue from: one
// kanna-orm did not make, one made in another order, or one holding a value its column's type rejects. A cursor comes
// from a client, so this is the client's error.
var ErrInvalidCursor = errors.New("orm: invalid cursor")

// ErrEmptyChangeSet is what an Update of a change set returns for one that assigns no column.
var ErrEmptyChangeSet = errors.New("orm: Update requires at least one assigned column")

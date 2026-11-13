package orm

import "errors"

// ErrNotFound is returned, wrapped, by a repository when a row does not exist.
var ErrNotFound = errors.New("orm: not found")

// ErrInvalidCursor is returned, wrapped, by the AfterCursor method of a scope when it cannot continue from the
// cursor: kanna-orm did not make it, it was made for another order, or it holds a value its column's type rejects. A
// cursor comes from a client, so this is the client's error.
var ErrInvalidCursor = errors.New("orm: invalid cursor")

// ErrEmptyChangeSet is returned by Update when the change set assigns no column.
var ErrEmptyChangeSet = errors.New("orm: Update requires at least one assigned column")

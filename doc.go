// Package orm holds the error a repository kanna-orm generates returns, and the clock it writes the creation and
// update times of its rows by. What else code written by hand uses is in its packages:
//
//   - [github.com/go-kanna/orm/scope]: Of, the one field of a scope type declared by hand.
//   - [github.com/go-kanna/orm/option]: the options a repository's constructor takes.
//   - [github.com/go-kanna/orm/hook]: the functions those options take, called around each statement.
//
// [github.com/go-kanna/orm/change] holds Set, what the Of of a generated change set returns, which code written by hand
// does not name. What the generated code runs on is in [github.com/go-kanna/orm/impl], which only the generated code
// imports.
package orm

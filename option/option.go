// Package option holds the options that a generated repository's constructor takes.
package option

import (
	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/hook"
	"github.com/go-kanna/orm/impl"
)

// Option configures a generated repository. Its zero value configures nothing.
type Option impl.Option

// WithHook makes a repository call h around each statement it runs. It runs inside the hooks given before it.
func WithHook(h hook.Hook) Option { return Option(impl.WithHook(h)) }

// WithClock makes a repository take the creation and update times of its rows from c instead of time.Now. If given more
// than once, the last one wins.
func WithClock(c orm.Clock) Option { return Option(impl.WithClock(c)) }

// Join combines opts into one option that applies each of them in order. Use it with a dependency injector, which holds
// only one value of a type and passes it to every repository it builds.
func Join(opts ...Option) Option { return Option(impl.Join(opts...)) }

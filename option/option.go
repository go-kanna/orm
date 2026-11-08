// Package option holds the options a generated repository's constructor takes.
package option

import (
	"github.com/go-kanna/orm"
	"github.com/go-kanna/orm/hook"
	"github.com/go-kanna/orm/impl"
)

// Option configures a generated repository.
type Option = impl.Option

// WithHook has a repository call h around each statement it runs, inside the hooks given before it.
func WithHook(h hook.Hook) Option { return impl.WithHook(h) }

// WithClock has a repository write the creation and update times of its rows by c, rather than by time.Now. Given
// more than once, the last wins.
func WithClock(c orm.Clocker) Option { return impl.WithClock(c) }

// Package option holds the options a generated repository's constructor takes.
package option

import (
	"github.com/go-kanna/orm/hook"
	"github.com/go-kanna/orm/impl"
)

// Option configures a generated repository.
type Option = impl.Option

// WithHook has a repository call h around each statement it runs, inside the hooks given before it.
func WithHook(h hook.Hook) Option { return impl.WithHook(h) }

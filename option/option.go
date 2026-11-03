// Package option holds the options a generated repository's constructor takes.
package option

import (
	"github.com/go-kanna/orm/hook"
	"github.com/go-kanna/orm/impl"
)

// Option configures a generated repository.
type Option = impl.Option

// WithBefore has a repository call f as each statement it runs starts, after the Before hooks added before it.
func WithBefore(f hook.Before) Option { return impl.WithBefore(f) }

// WithAfter has a repository call f once each statement it runs is done, before the After hooks added before it, so
// that the first hooks added enclose the others.
func WithAfter(f hook.After) Option { return impl.WithAfter(f) }

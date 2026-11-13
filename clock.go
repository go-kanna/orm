package orm

import (
	"context"
	"time"
)

// Clock is the clock a repository writes the creation and update times of its rows by, as option.WithClock gives
// it. Now takes the context of the method writing them, for a clock the context carries.
type Clock interface {
	Now(ctx context.Context) time.Time
}

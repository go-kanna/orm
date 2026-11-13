package orm

import (
	"context"
	"time"
)

// Clock supplies the creation and update times that a repository writes to its rows. Pass it with option.WithClock. Now
// receives the context of the method that writes the times, for clocks that read the context.
type Clock interface {
	Now(ctx context.Context) time.Time
}

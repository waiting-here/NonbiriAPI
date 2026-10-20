package observability

import (
	"context"
	"errors"
	"sync"
	"time"
)

type connectionRootKey struct{}

// StartConnectionSpan observes actual writer occupancy independently of whether
// the caller opted into a measured transaction. Phases overlap the hold span.
// Only private, preclassified context values can produce a root label.
func (p *Performance) StartConnectionSpan(ctx context.Context, kind string) func(error) {
	switch kind {
	case "hold", "transaction", "rows", "exec", "prepare", "ping":
	default:
		return func(error) {}
	}
	root, _ := ctx.Value(connectionRootKey{}).(string)
	if root == "" {
		root = "unclassified"
	}
	started := time.Now()
	finish := p.begin(performanceKey{"occupancy." + kind + "." + root, ""})
	var once sync.Once
	return func(err error) {
		once.Do(func() {
			canceled := ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
			finish(time.Since(started), 0, err != nil && !canceled, canceled)
		})
	}
}

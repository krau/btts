package enrich

import (
	"context"
	"sync"
	"time"

	"github.com/krau/btts/utils"
)

// mediaGate limits concurrent and per-second media enrichment jobs. Jobs are
// gated as a whole because each job can issue multiple tagger/embedder calls.
type mediaGate struct {
	sem      chan struct{}
	interval time.Duration
	mu       sync.Mutex
	next     time.Time
}

func newMediaGate(perSecond float64, concurrency int) *mediaGate {
	if concurrency <= 0 {
		concurrency = 1
	}
	var interval time.Duration
	if perSecond > 0 {
		interval = time.Duration(float64(time.Second) / perSecond)
		if interval <= 0 {
			interval = time.Nanosecond
		}
	}
	return &mediaGate{
		sem:      make(chan struct{}, concurrency),
		interval: interval,
	}
}

// acquire blocks until a media enrichment slot is available. The returned
// release function must be called after the job finishes.
func (g *mediaGate) acquire(ctx context.Context) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	select {
	case g.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-g.sem }

	g.mu.Lock()
	now := time.Now()
	if g.next.Before(now) {
		g.next = now
	}
	wait := g.next.Sub(now)
	g.next = g.next.Add(g.interval)
	g.mu.Unlock()

	if wait > 0 {
		if err := utils.SleepContext(ctx, wait); err != nil {
			release()
			return nil, err
		}
	}
	return release, nil
}

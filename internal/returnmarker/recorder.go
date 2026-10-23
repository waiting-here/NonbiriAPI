package returnmarker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	queueCapacity = 32
	dedupCapacity = 256
	writeInterval = 5 * time.Second
	writeBudget   = 200 * time.Millisecond
)

type Event struct {
	Evidence   Evidence
	UserID     int64
	RequestID  string
	ObservedAt int64
	Ref        string
}

type Recorder struct {
	marker   *Marker
	persist  func(context.Context, Event) error
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	queue    chan Event
	mu       sync.Mutex
	seen     map[string]int64
	interval time.Duration
	budget   time.Duration
}

func NewRecorder(ctx context.Context, marker *Marker, persist func(context.Context, Event) error) *Recorder {
	ctx, cancel := context.WithCancel(ctx)
	r := &Recorder{marker: marker, persist: persist, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		queue: make(chan Event, queueCapacity), seen: make(map[string]int64), interval: writeInterval, budget: writeBudget}
	go r.run()
	return r
}

// Observe never waits for storage. Saturation, duplicate evidence and storage
// failure are deliberately lossy: alerting must not delay inference.
func (r *Recorder) Observe(request *http.Request, userID int64, requestID string) {
	if r == nil || request == nil || userID <= 0 || request.Context().Err() != nil || r.ctx.Err() != nil {
		return
	}
	evidence, ok := r.marker.Verify(request.Header)
	if !ok {
		return
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("%x:%d:%d:%d:%d", evidence.Origin, evidence.EndpointID, evidence.EndpointKeyID, evidence.DonationKeyID, userID)))
	ref := "return-detected:" + hex.EncodeToString(key[:])
	now := r.marker.now().Unix()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx.Err() != nil {
		return
	}
	if until, found := r.seen[ref]; found && until > now {
		return
	}
	for key, until := range r.seen {
		if until <= now {
			delete(r.seen, key)
		}
	}
	if len(r.seen) >= dedupCapacity {
		return
	}
	event := Event{Evidence: evidence, UserID: userID, RequestID: requestID, ObservedAt: now, Ref: ref}
	select {
	case r.queue <- event:
		r.seen[ref] = now + int64(Lifetime/time.Second)
	default:
	}
}

func (r *Recorder) run() {
	defer close(r.done)
	var next time.Time
	for {
		select {
		case <-r.ctx.Done():
			return
		case event := <-r.queue:
			if delay := time.Until(next); delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-r.ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			if r.ctx.Err() != nil {
				return
			}
			if r.marker.now().Unix()-event.Evidence.IssuedAt >= int64(Lifetime/time.Second) {
				continue
			}
			ctx, cancel := context.WithTimeout(r.ctx, r.budget)
			_ = r.persist(ctx, event)
			cancel()
			next = time.Now().Add(r.interval)
		}
	}
}

// Close cancels both pending work and an active bounded write, then joins the
// worker before the application releases its database. It is safe to repeat.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.cancel()
	<-r.done
	r.mu.Lock()
	clear(r.seen)
	for len(r.queue) > 0 {
		<-r.queue
	}
	r.mu.Unlock()
	return nil
}

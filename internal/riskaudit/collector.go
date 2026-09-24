package riskaudit

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
)

type MinuteWriter interface {
	SaveMinutes(context.Context, []Minute, []Gap) error
}
type CollectorOptions struct {
	Now                                          func() time.Time
	Boundary                                     func()
	Epoch                                        string
	QueueSize, MaxUsers, MaxRequests, MaxBuckets int
	Config                                       Config
}
type auditEvent struct {
	value  flowcontrol.Observation
	kind   string
	config Config
}
type bucketKey struct {
	user, minute int64
	kind         string
}
type userOccupancy struct {
	id                                 int64
	last, seen                         time.Time
	firstMinute                        int64
	active, rpmLimit, concurrencyLimit int
	kinds                              map[string]int
	uncertain                          bool
}
type requestSample struct {
	user     int64
	kind     string
	start    time.Time
	admitted time.Time
	active   bool
	rpmState string
}

// Collector is a bounded, single-consumer minute integrator. Observe and Bind
// only enqueue; Flush and Run belong to the same lifecycle owner.
type Collector struct {
	writer                         MinuteWriter
	opts                           CollectorOptions
	config                         Config
	queue                          chan auditEvent
	lost                           atomic.Uint64
	lossMinute                     atomic.Int64
	running                        atomic.Bool
	stopped                        atomic.Bool
	users                          map[int64]*userOccupancy
	requests                       map[string]*requestSample
	buckets                        map[bucketKey]*Minute
	dirty                          map[bucketKey]bool
	gaps                           map[string]Gap
	started                        time.Time
	concurrencyThrough, rpmThrough time.Time
}

func NewCollector(writer MinuteWriter, options CollectorOptions) (*Collector, error) {
	if writer == nil {
		return nil, ErrInvalid
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.QueueSize == 0 {
		options.QueueSize = 4096
	}
	if options.MaxUsers == 0 {
		options.MaxUsers = 100000
	}
	if options.MaxRequests == 0 {
		options.MaxRequests = 100000
	}
	if options.MaxBuckets == 0 {
		options.MaxBuckets = 200000
	}
	if options.QueueSize < 1 || options.QueueSize > 65536 || options.MaxUsers < 1 || options.MaxUsers > 100000 || options.MaxRequests < 1 || options.MaxRequests > 100000 || options.MaxBuckets < 4 || options.MaxBuckets > 500000 {
		return nil, ErrInvalid
	}
	if options.Config.Revision == 0 {
		options.Config = DefaultConfig()
	}
	if !options.Config.Valid() {
		return nil, ErrInvalid
	}
	if options.Epoch == "" {
		var err error
		options.Epoch, err = db.GenerateOpaqueID("aud_")
		if err != nil {
			return nil, err
		}
	}
	if len(options.Epoch) > 64 {
		return nil, ErrInvalid
	}
	c := &Collector{writer: writer, opts: options, config: options.Config, queue: make(chan auditEvent, options.QueueSize), users: make(map[int64]*userOccupancy), requests: make(map[string]*requestSample), buckets: make(map[bucketKey]*Minute), dirty: make(map[bucketKey]bool), gaps: make(map[string]Gap), started: options.Now()}
	c.addGap(minuteAt(c.started), "restart", 0)
	return c, nil
}
func (c *Collector) Observe(value flowcontrol.Observation) {
	if c != nil {
		c.enqueue(auditEvent{value: value})
	}
}
func (c *Collector) Bind(requestID string, userID int64, kind string) {
	if c == nil || userID <= 0 || requestID == "" || !validKind(kind) {
		return
	}
	c.enqueue(auditEvent{value: flowcontrol.Observation{Event: "bind", At: c.opts.Now(), UserID: userID, RequestID: requestID}, kind: kind})
}
func (c *Collector) ForgetUser(userID int64) {
	if c != nil && userID > 0 {
		c.enqueue(auditEvent{value: flowcontrol.Observation{Event: "forget", At: c.opts.Now(), UserID: userID}})
	}
}
func (c *Collector) ApplyConfig(config Config) {
	if c != nil && config.Valid() {
		c.enqueue(auditEvent{value: flowcontrol.Observation{Event: "audit_config", At: c.opts.Now()}, config: config})
	}
}
func (c *Collector) enqueue(event auditEvent) {
	if c.stopped.Load() {
		return
	}
	select {
	case c.queue <- event:
	default:
		c.lost.Add(1)
		at := event.value.At
		if !event.value.AdmittedAt.IsZero() && event.value.AdmittedAt.Before(at) {
			at = event.value.AdmittedAt
		}
		minute := minuteAt(at) + 1
		for {
			old := c.lossMinute.Load()
			if old != 0 && old <= minute {
				break
			}
			if c.lossMinute.CompareAndSwap(old, minute) {
				break
			}
		}
	}
}
func (c *Collector) addGap(minute int64, reason string, count int64) {
	if minute < 0 {
		return
	}
	key := fmt.Sprintf("%d/%s", minute, reason)
	gap, exists := c.gaps[key]
	gap.Epoch = c.opts.Epoch
	gap.Minute = minute
	gap.Reason = reason
	gap.LostCount += count
	if len(c.gaps) < 4096 || exists {
		c.gaps[key] = gap
	}
}
func (c *Collector) bucket(user *userOccupancy, minute int64, kind string) *Minute {
	key := bucketKey{user.id, minute, kind}
	b := c.buckets[key]
	if b == nil {
		if len(c.buckets) >= c.opts.MaxBuckets {
			c.addGap(minute, "capacity", 1)
			user.uncertain = true
			return nil
		}
		b = &Minute{Epoch: c.opts.Epoch, UserID: user.id, Minute: minute, Kind: kind, ConfigRevision: c.config.Revision}
		if total := c.buckets[bucketKey{user.id, minute, "total"}]; total != nil && kind != "total" {
			b.RPMLimit = total.RPMLimit
			b.ConcurrencyLimit = total.ConcurrencyLimit
			b.ConfigRevision = total.ConfigRevision
			b.Coverage = total.Coverage
		}
		if minute <= user.firstMinute || minute <= minuteAt(c.started) {
			b.Coverage |= CoveragePartial
		}
		c.buckets[key] = b
	}
	if user.uncertain {
		b.Coverage |= CoverageDropped
	}
	if user.rpmLimit > 0 {
		if b.RPMLimit == 0 {
			b.RPMLimit = user.rpmLimit
		} else if minute >= minuteAt(user.last) && b.RPMLimit != user.rpmLimit {
			b.Coverage |= CoverageLimitChanged
		}
	}
	if user.concurrencyLimit > 0 {
		if b.ConcurrencyLimit == 0 {
			b.ConcurrencyLimit = user.concurrencyLimit
		} else if minute >= minuteAt(user.last) && b.ConcurrencyLimit != user.concurrencyLimit {
			b.Coverage |= CoverageLimitChanged
		}
	}
	if b.ConfigRevision != c.config.Revision && minute >= minuteAt(user.last) {
		b.Coverage |= CoverageLimitChanged
	}
	c.dirty[key] = true
	return b
}
func (c *Collector) user(id int64, at time.Time) *userOccupancy {
	u := c.users[id]
	if u == nil {
		if len(c.users) >= c.opts.MaxUsers {
			c.addGap(minuteAt(at), "capacity", 1)
			return nil
		}
		u = &userOccupancy{id: id, last: at, seen: at, firstMinute: minuteAt(at), kinds: map[string]int{"self": 0, "charity": 0, "unclassified": 0}}
		c.users[id] = u
	}
	if at.After(u.seen) {
		u.seen = at
	}
	return u
}
func (c *Collector) advance(u *userOccupancy, at time.Time) {
	if at.Before(u.last) {
		c.addGap(minuteAt(at), "clock", 1)
		u.uncertain = true
		return
	}
	from, to := u.last.UnixMilli(), at.UnixMilli()
	if to-from > int64(Retention/time.Millisecond) {
		c.addGap(minuteAt(at), "clock", 1)
		u.uncertain = true
		u.last = at
		return
	}
	for from < to {
		minute := from / 60000 * 60
		end := min(to, (minute+60)*1000)
		if b := c.bucket(u, minute, "total"); b != nil {
			b.OccupancyMillis += int64(u.active) * (end - from)
			b.Peak = max(b.Peak, u.active)
		}
		for kind, count := range u.kinds {
			if count > 0 {
				if b := c.bucket(u, minute, kind); b != nil {
					b.OccupancyMillis += int64(count) * (end - from)
					b.Peak = max(b.Peak, count)
				}
			}
		}
		from = end
	}
	u.last = at
}
func (c *Collector) eachBucket(u *userOccupancy, minute int64, kind string, update func(*Minute)) {
	if b := c.bucket(u, minute, "total"); b != nil {
		update(b)
	}
	if b := c.bucket(u, minute, kind); b != nil {
		update(b)
	}
}
func (c *Collector) invalidate(at time.Time, userID int64, flag int, reason string) {
	if userID == 0 {
		c.addGap(minuteAt(at), reason, 0)
	}
	for id, u := range c.users {
		if userID != 0 && id != userID {
			continue
		}
		c.advance(u, at)
		for kind := range u.kinds {
			if b := c.bucket(u, minuteAt(at), kind); b != nil {
				b.Coverage |= flag
			}
		}
		if b := c.bucket(u, minuteAt(at), "total"); b != nil {
			b.Coverage |= flag
		}
		if flag&CoverageDropped != 0 {
			u.uncertain = true
		} else {
			u.rpmLimit = 0
			u.concurrencyLimit = 0
		}
	}
}
func (c *Collector) handle(event auditEvent) {
	v := event.value
	if v.At.IsZero() || v.At.Unix() < 0 {
		c.addGap(minuteAt(c.opts.Now()), "clock", 1)
		return
	}
	switch v.Event {
	case "concurrency_boundary", "rpm_boundary":
		through := &c.concurrencyThrough
		if v.Event == "rpm_boundary" {
			through = &c.rpmThrough
		}
		if v.At.Before(*through) {
			c.invalidate(v.At, 0, CoverageDropped, "clock")
			return
		}
		*through = v.At
		return
	case "forget":
		delete(c.users, v.UserID)
		for id, r := range c.requests {
			if r.user == v.UserID {
				delete(c.requests, id)
			}
		}
		for key := range c.buckets {
			if key.user == v.UserID {
				delete(c.buckets, key)
				delete(c.dirty, key)
			}
		}
		return
	case "limits_changed":
		c.invalidate(v.At, v.UserID, CoverageLimitChanged, "configuration")
		return
	case "audit_config":
		if event.config.Revision <= c.config.Revision {
			return
		}
		c.invalidate(v.At, 0, CoverageLimitChanged, "configuration")
		c.config = event.config
		return
	case "closed":
		c.invalidate(v.At, 0, CoverageDropped, "restart")
		return
	}
	if v.UserID <= 0 || len(v.RequestID) != 26 {
		c.addGap(minuteAt(v.At), "capacity", 1)
		return
	}
	u := c.user(v.UserID, v.At)
	if u == nil {
		return
	}
	r := c.requests[v.RequestID]
	if r != nil && r.user != v.UserID {
		c.invalidate(v.At, 0, CoverageDropped, "capacity")
		return
	}
	if v.Event == "bind" {
		c.bind(u, r, event.kind, v.At)
		return
	}
	kind := "unclassified"
	if r != nil {
		kind = r.kind
	}
	switch v.Event {
	case "concurrency_acquire":
		c.advance(u, v.At)
		if r != nil {
			c.invalidate(v.At, v.UserID, CoverageDropped, "capacity")
			return
		}
		if len(c.requests) >= c.opts.MaxRequests {
			c.invalidate(v.At, v.UserID, CoverageDropped, "capacity")
			return
		}
		r = &requestSample{user: v.UserID, kind: kind, start: v.At, active: true}
		c.requests[v.RequestID] = r
		u.concurrencyLimit = v.ConcurrencyLimit
		if u.active+1 != v.Active {
			u.uncertain = true
		}
		u.active = v.Active
		u.kinds[kind]++
		if b := c.bucket(u, minuteAt(v.At), "total"); b != nil {
			b.Peak = max(b.Peak, v.Active)
		}
		if b := c.bucket(u, minuteAt(v.At), kind); b != nil {
			b.Peak = max(b.Peak, u.kinds[kind])
		}
	case "concurrency_release":
		c.advance(u, v.At)
		if r != nil && r.active {
			u.kinds[kind] = max(0, u.kinds[kind]-1)
			r.active = false
		}
		if u.active-1 != v.Active {
			u.uncertain = true
		}
		u.active = max(0, v.Active)
		if v.Active == 0 {
			u.kinds = map[string]int{"self": 0, "charity": 0, "unclassified": 0}
			u.uncertain = false
		}
	case "concurrency_denied":
		c.advance(u, v.At)
		u.concurrencyLimit = v.ConcurrencyLimit
		c.eachBucket(u, minuteAt(v.At), kind, func(b *Minute) { b.ConcurrencyDenied++ })
	case "rpm_reserve":
		u.rpmLimit = v.RPMLimit
		if r == nil {
			c.invalidate(v.At, v.UserID, CoverageDropped, "capacity")
			return
		}
		if r.rpmState != "" {
			return
		}
		r.admitted = v.AdmittedAt
		r.rpmState = "pending"
		c.eachBucket(u, minuteAt(v.AdmittedAt), kind, func(b *Minute) { b.RPMPending++ })
	case "rpm_commit", "rpm_release":
		if r == nil {
			c.addGap(minuteAt(v.AdmittedAt), "queue_overflow", 1)
			return
		}
		if r.rpmState != "pending" {
			return
		}
		r.rpmState = v.Event
		c.eachBucket(u, minuteAt(r.admitted), kind, func(b *Minute) {
			b.RPMPending = max(0, b.RPMPending-1)
			if v.Event == "rpm_commit" {
				b.RPMCommitted++
			} else {
				b.RPMReleased++
			}
		})
	case "rpm_denied":
		u.rpmLimit = v.RPMLimit
		c.eachBucket(u, minuteAt(v.At), kind, func(b *Minute) { b.RPMDenied++ })
	}
	if r != nil && !r.active && r.rpmState != "pending" {
		delete(c.requests, v.RequestID)
	}
}
func (c *Collector) bind(u *userOccupancy, r *requestSample, kind string, at time.Time) {
	if r == nil || !validKind(kind) || kind == r.kind {
		return
	}
	if r.kind != "unclassified" {
		c.invalidate(at, u.id, CoverageDropped, "capacity")
		return
	}
	c.advance(u, at)
	if r.active {
		u.kinds[r.kind] = max(0, u.kinds[r.kind]-1)
		u.kinds[kind]++
		if b := c.bucket(u, minuteAt(at), kind); b != nil {
			b.Peak = max(b.Peak, u.kinds[kind])
		}
	}
	// Move the request's exact earlier occupancy. Historical subcategory peaks
	// cannot be reconstructed from a late label, so they remain marked partial.
	from, to := r.start.UnixMilli(), at.UnixMilli()
	if to-from > int64(Retention/time.Millisecond) {
		c.invalidate(at, u.id, CoverageDropped, "capacity")
		return
	}
	for from < to {
		minute := from / 60000 * 60
		end := min(to, (minute+60)*1000)
		old, next := c.bucket(u, minute, r.kind), c.bucket(u, minute, kind)
		if old != nil && next != nil {
			amount := min(old.OccupancyMillis, end-from)
			old.OccupancyMillis -= amount
			next.OccupancyMillis += amount
			old.Coverage |= CoverageUnresolved
			next.Coverage |= CoverageUnresolved
			next.Peak = max(next.Peak, 1)
		}
		from = end
	}
	if !r.admitted.IsZero() {
		old, next := c.bucket(u, minuteAt(r.admitted), r.kind), c.bucket(u, minuteAt(r.admitted), kind)
		if old != nil && next != nil {
			switch r.rpmState {
			case "pending":
				old.RPMPending = max(0, old.RPMPending-1)
				next.RPMPending++
			case "rpm_commit":
				old.RPMCommitted = max(0, old.RPMCommitted-1)
				next.RPMCommitted++
			case "rpm_release":
				old.RPMReleased = max(0, old.RPMReleased-1)
				next.RPMReleased++
			}
		}
	}
	r.kind = kind
}

// Flush drains a finite queue snapshot and writes bounded batches outside the
// admission locks. Calling it concurrently with Run is not supported.
func (c *Collector) Flush(ctx context.Context, at time.Time) error {
	if c == nil || ctx == nil {
		return ErrInvalid
	}
	for n := len(c.queue); n > 0; n-- {
		select {
		case event := <-c.queue:
			c.handle(event)
		default:
		}
	}
	through := at
	if c.opts.Boundary != nil {
		if c.concurrencyThrough.Before(through) {
			through = c.concurrencyThrough
		}
		if c.rpmThrough.Before(through) {
			through = c.rpmThrough
		}
	}
	if lost := c.lost.Swap(0); lost > 0 {
		first := c.lossMinute.Swap(0) - 1
		if first < minuteAt(c.started) {
			first = minuteAt(at)
		}
		c.addGap(max(0, first), "queue_overflow", int64(min(lost, uint64(1<<63-1))))
		c.invalidate(at, 0, CoverageDropped, "queue_overflow")
		for _, b := range c.buckets {
			b.Coverage |= CoverageDropped
			c.dirty[bucketKey{b.UserID, b.Minute, b.Kind}] = true
		}
	}
	for id, u := range c.users {
		until := through
		if u.active == 0 && through.Sub(u.seen) > 2*time.Minute {
			until = u.seen.Add(2 * time.Minute)
		}
		if until.After(u.last) {
			c.advance(u, until)
		}
		if u.active == 0 && through.Sub(u.seen) > 2*time.Minute {
			delete(c.users, id)
		}
	}
	// Counts may have arrived beyond one fence. Advance their completeness only
	// once both admission streams have crossed the end of the minute.
	for key, b := range c.buckets {
		if b.UpdatedAt < min(through.Unix(), b.Minute+60) {
			c.dirty[key] = true
		}
	}
	for len(c.dirty) > 0 || len(c.gaps) > 0 {
		batch := make([]Minute, 0, 500)
		keys := make([]bucketKey, 0, 500)
		gaps := make([]Gap, 0, 500)
		gapKeys := make([]string, 0, 500)
		for key := range c.dirty {
			b := c.buckets[key]
			if b == nil {
				delete(c.dirty, key)
				continue
			}
			b.UpdatedAt = max(b.UpdatedAt, through.Unix(), b.Minute)
			batch = append(batch, *b)
			keys = append(keys, key)
			if len(batch) == 500 {
				break
			}
		}
		for key, gap := range c.gaps {
			gaps = append(gaps, gap)
			gapKeys = append(gapKeys, key)
			if len(gaps) == 500 {
				break
			}
		}
		if err := c.writer.SaveMinutes(ctx, batch, gaps); err != nil {
			c.invalidate(at, 0, CoverageDropped, "persistence")
			return err
		}
		for _, key := range keys {
			delete(c.dirty, key)
		}
		for _, key := range gapKeys {
			delete(c.gaps, key)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	oldestUnknown := make(map[int64]int64)
	for _, r := range c.requests {
		if r.active && r.kind == "unclassified" {
			old, ok := oldestUnknown[r.user]
			if !ok || r.start.Unix() < old {
				oldestUnknown[r.user] = r.start.Unix()
			}
		}
	}
	for key, b := range c.buckets {
		if key.minute >= minuteAt(through)-120 || b.RPMPending > 0 {
			continue
		}
		old, ok := oldestUnknown[key.user]
		retain := ok && old < key.minute+60
		if !retain {
			delete(c.buckets, key)
		}
	}
	return nil
}
func (c *Collector) Run(ctx context.Context) error {
	if c == nil || ctx == nil || c.opts.Boundary == nil || !c.running.CompareAndSwap(false, true) {
		return ErrInvalid
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer c.stopped.Store(true)
	for {
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c.opts.Boundary()
			_ = c.Flush(final, c.opts.Now())
			return nil
		case <-ticker.C:
			c.opts.Boundary()
			if err := c.Flush(ctx, c.opts.Now()); err != nil && ctx.Err() != nil {
				return nil
			}
		}
	}
}

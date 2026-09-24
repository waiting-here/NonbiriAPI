package riskaudit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
)

type memoryMinutes struct {
	values map[bucketKey]Minute
	gaps   []Gap
	fail   bool
}

func (m *memoryMinutes) SaveMinutes(_ context.Context, values []Minute, gaps []Gap) error {
	if m.fail {
		return errors.New("unavailable")
	}
	if m.values == nil {
		m.values = make(map[bucketKey]Minute)
	}
	for _, v := range values {
		m.values[bucketKey{v.UserID, v.Minute, v.Kind}] = v
	}
	m.gaps = append(m.gaps, gaps...)
	return nil
}
func testRequestID(n int) string { return fmt.Sprintf("req_%022d", n) }
func TestCollectorMinuteIntegralLateBindingAndAdmissionSettlement(t *testing.T) {
	start := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	now := start
	writer := &memoryMinutes{}
	c, err := NewCollector(writer, CollectorOptions{Now: func() time.Time { return now }, Epoch: "epoch", Config: DefaultConfig()})
	if err != nil {
		t.Fatal(err)
	}
	id := testRequestID(1)
	push := func(event string, offset time.Duration, active int) {
		now = start.Add(offset)
		c.Observe(flowcontrol.Observation{Event: event, At: now, AdmittedAt: start.Add(10 * time.Second), UserID: 1, RequestID: id, RPMLimit: 10, ConcurrencyLimit: 1, Active: active})
	}
	push("concurrency_acquire", 10*time.Second, 1)
	push("rpm_reserve", 10*time.Second, 1)
	now = start.Add(20 * time.Second)
	c.Bind(id, 1, "charity")
	if err = c.Flush(context.Background(), start.Add(90*time.Second)); err != nil {
		t.Fatal(err)
	}
	push("rpm_commit", 100*time.Second, 1)
	push("concurrency_release", 100*time.Second, 0)
	if err = c.Flush(context.Background(), start.Add(121*time.Second)); err != nil {
		t.Fatal(err)
	}
	first := writer.values[bucketKey{1, start.Unix(), "total"}]
	second := writer.values[bucketKey{1, start.Unix() + 60, "total"}]
	if first.OccupancyMillis != 50000 || second.OccupancyMillis != 40000 || first.RPMCommitted != 1 || first.RPMPending != 0 || second.RPMCommitted != 0 {
		t.Fatalf("minute facts first=%+v second=%+v", first, second)
	}
	if first.Peak != 1 || second.Peak != 1 || first.Coverage&CoveragePartial == 0 {
		t.Fatalf("peak/coverage %+v %+v", first, second)
	}
	charity := writer.values[bucketKey{1, start.Unix(), "charity"}]
	unknown := writer.values[bucketKey{1, start.Unix(), "unclassified"}]
	if charity.OccupancyMillis != 50000 || charity.RPMCommitted != 1 || unknown.OccupancyMillis != 0 || unknown.RPMCommitted != 0 {
		t.Fatalf("binding charity=%+v unknown=%+v", charity, unknown)
	}
}
func TestCollectorTotalPeakIsNotSumOfCategoryPeaks(t *testing.T) {
	start := time.Unix(600, 0)
	now := start
	writer := &memoryMinutes{}
	c, _ := NewCollector(writer, CollectorOptions{Now: func() time.Time { return now }, Epoch: "peak"})
	for i, kind := range []string{"self", "charity"} {
		id := testRequestID(i + 1)
		now = start.Add(time.Duration(i*20+1) * time.Second)
		c.Observe(flowcontrol.Observation{Event: "concurrency_acquire", At: now, UserID: 1, RequestID: id, ConcurrencyLimit: 3, Active: 1})
		c.Bind(id, 1, kind)
		c.Observe(flowcontrol.Observation{Event: "concurrency_release", At: now.Add(10 * time.Second), UserID: 1, RequestID: id, ConcurrencyLimit: 3, Active: 0})
	}
	if err := c.Flush(context.Background(), start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	total := writer.values[bucketKey{1, start.Unix(), "total"}]
	if total.Peak != 1 || total.OccupancyMillis != 20000 {
		t.Fatalf("total %+v", total)
	}
}
func TestCollectorLossAndPersistenceFailureBreakCoverage(t *testing.T) {
	start := time.Unix(600, 0)
	writer := &memoryMinutes{}
	c, _ := NewCollector(writer, CollectorOptions{Now: func() time.Time { return start }, Epoch: "loss", QueueSize: 1})
	id := testRequestID(1)
	event := flowcontrol.Observation{Event: "concurrency_acquire", At: start, UserID: 1, RequestID: id, ConcurrencyLimit: 1, Active: 1}
	c.Observe(event)
	c.Observe(event)
	if err := c.Flush(context.Background(), start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if writer.values[bucketKey{1, start.Unix(), "total"}].Coverage&CoverageDropped == 0 {
		t.Fatal("queue loss did not mark coverage")
	}
	found := false
	for _, gap := range writer.gaps {
		found = found || gap.Reason == "queue_overflow"
	}
	if !found {
		t.Fatal("queue gap missing")
	}
	writer.fail = true
	if err := c.Flush(context.Background(), start.Add(2*time.Minute)); err == nil {
		t.Fatal("persistence failure concealed")
	}
	writer.fail = false
	if err := c.Flush(context.Background(), start.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	found = false
	for _, gap := range writer.gaps {
		found = found || gap.Reason == "persistence"
	}
	if !found {
		t.Fatal("persistence gap missing")
	}
}
func TestAuditRunsDoNotBridgeMissingChangedOrRestartedMinutes(t *testing.T) {
	cfg := DefaultConfig()
	start := int64(600)
	values := make([]Minute, 5)
	for i := range values {
		values[i] = Minute{Epoch: "one", UserID: 1, Minute: start + int64(i)*60, Kind: "total", RPMCommitted: 8, RPMLimit: 10, ConcurrencyLimit: 2, OccupancyMillis: 96000, ConfigRevision: 1, UpdatedAt: start + int64(i+1)*60}
	}
	full := SummarizeMinutes(1, values, nil, cfg, 2000, "total")
	if !full.RPMRisk || !full.ConcurrencyRisk {
		t.Fatalf("expected sustained risk: %+v", full)
	}
	for _, mutation := range []func([]Minute){func(v []Minute) { v[2].Coverage = CoverageLimitChanged }, func(v []Minute) { v[2].RPMPending = 1 }, func(v []Minute) { v[2].RPMLimit = 0 }, func(v []Minute) { v[2].Epoch = "two" }, func(v []Minute) { v[2].Minute += 1 }, func(v []Minute) { v[2].ConfigRevision = 2 }} {
		copyValues := append([]Minute(nil), values...)
		mutation(copyValues)
		out := SummarizeMinutes(1, copyValues, nil, cfg, 2000, "total")
		if out.RPMRisk || out.ConcurrencyRisk {
			t.Fatalf("bridged incomplete history: %+v", out)
		}
	}
	out := SummarizeMinutes(1, values, map[int64]bool{720: true}, cfg, 2000, "total")
	if out.RPMRisk || out.ConcurrencyRisk {
		t.Fatal("bridged explicit gap")
	}
}

func TestCollectorConcurrentCallbacksAndStaleConfiguration(t *testing.T) {
	writer := &memoryMinutes{}
	var c *Collector
	var err error
	c, err = NewCollector(writer, CollectorOptions{Epoch: "concurrent", QueueSize: 256, Boundary: func() {
		c.Observe(flowcontrol.Observation{Event: "concurrency_boundary", At: time.Now()})
		c.Observe(flowcontrol.Observation{Event: "rpm_boundary", At: time.Now()})
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			for j := 0; j < 64; j++ {
				c.Observe(flowcontrol.Observation{Event: "concurrency_acquire", At: time.Now(), UserID: int64(i + 1), RequestID: testRequestID(i*64 + j), ConcurrencyLimit: 100, Active: j + 1})
			}
		}(i)
	}
	workers.Wait()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !c.stopped.Load() {
		t.Fatal("collector did not stop")
	}
	now := time.Now()
	newer := DefaultConfig()
	newer.Revision = 3
	older := newer
	older.Revision = 2
	c.handle(auditEvent{value: flowcontrol.Observation{Event: "audit_config", At: now}, config: newer})
	c.handle(auditEvent{value: flowcontrol.Observation{Event: "audit_config", At: now}, config: older})
	if c.config.Revision != 3 {
		t.Fatal("late callback rolled back policy revision")
	}
}

func TestCollectorCompletesMinutesOnlyAfterBothAdmissionFences(t *testing.T) {
	start := time.Unix(600, 0)
	writer := &memoryMinutes{}
	c, err := NewCollector(writer, CollectorOptions{Now: func() time.Time { return start }, Epoch: "fence", Boundary: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	id := testRequestID(1)
	for _, event := range []string{"concurrency_acquire", "rpm_reserve"} {
		c.Observe(flowcontrol.Observation{Event: event, At: start.Add(time.Second), AdmittedAt: start.Add(time.Second), UserID: 1, RequestID: id, ConcurrencyLimit: 1, RPMLimit: 10, Active: 1})
	}
	c.Observe(flowcontrol.Observation{Event: "concurrency_boundary", At: start.Add(time.Minute)})
	c.Observe(flowcontrol.Observation{Event: "rpm_boundary", At: start.Add(59 * time.Second)})
	if err = c.Flush(context.Background(), start.Add(121*time.Second)); err != nil {
		t.Fatal(err)
	}
	first := writer.values[bucketKey{1, start.Unix(), "total"}]
	if first.OccupancyMillis != 58000 || first.UpdatedAt != start.Add(59*time.Second).Unix() {
		t.Fatalf("advanced past the earlier fence: %+v", first)
	}
	// The concurrency stream can advance beyond the latest RPM fence. Such
	// already observed counts must remain incomplete until the other fence.
	c.Observe(flowcontrol.Observation{Event: "concurrency_release", At: start.Add(121 * time.Second), UserID: 1, RequestID: id, Active: 0})
	c.Observe(flowcontrol.Observation{Event: "concurrency_boundary", At: start.Add(121 * time.Second)})
	if err = c.Flush(context.Background(), start.Add(121*time.Second)); err != nil {
		t.Fatal(err)
	}
	second := writer.values[bucketKey{1, start.Unix() + 60, "total"}]
	if second.OccupancyMillis != 60000 || second.UpdatedAt >= start.Add(120*time.Second).Unix() {
		t.Fatalf("unfenced counts claimed complete: %+v", second)
	}
	c.Observe(flowcontrol.Observation{Event: "rpm_boundary", At: start.Add(121 * time.Second)})
	if err = c.Flush(context.Background(), start.Add(121*time.Second)); err != nil {
		t.Fatal(err)
	}
	second = writer.values[bucketKey{1, start.Unix() + 60, "total"}]
	if second.UpdatedAt < start.Add(120*time.Second).Unix() || second.OccupancyMillis != 60000 || second.Coverage != 0 {
		t.Fatalf("fenced minute did not become complete: %+v", second)
	}
	manual, _ := NewCollector(&memoryMinutes{}, CollectorOptions{})
	if err = manual.Run(context.Background()); !errors.Is(err, ErrInvalid) {
		t.Fatal("unfenced lifecycle collector accepted")
	}
}

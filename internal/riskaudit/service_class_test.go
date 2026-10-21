package riskaudit

import (
	"context"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
)

func TestServiceClassSaturationPreservesSinglePoolRiskAndActualOccupancy(t *testing.T) {
	for _, light := range []bool{false, true} {
		start := time.Unix(600, 0)
		writer := &memoryMinutes{}
		c, err := NewCollector(writer, CollectorOptions{Now: func() time.Time { return start }, Epoch: "classes"})
		if err != nil {
			t.Fatal(err)
		}
		count := 5
		if light {
			count = 15
		}
		for i := 1; i <= count; i++ {
			v := flowcontrol.Observation{Event: "concurrency_acquire", At: start, UserID: 1, RequestID: testRequestID(i), Active: i, ConcurrencyLimit: 20, HeavyLimit: 5, LightLimit: 15}
			if light {
				v.LightActive = i
			} else {
				v.HeavyActive = i
			}
			c.Observe(v)
		}
		c.Observe(flowcontrol.Observation{Event: "rpm_denied", At: start, UserID: 1, RequestID: testRequestID(99), RPMLimit: 60})
		if err := c.Flush(context.Background(), start.Add(6*time.Minute)); err != nil {
			t.Fatal(err)
		}
		var values []Minute
		for _, m := range writer.values {
			if m.Kind == "total" {
				values = append(values, m)
			}
		}
		m := writer.values[bucketKey{1, 660, "total"}]
		if m.SaturationMicrosMillis == nil || *m.SaturationMicrosMillis != 60000000000 || m.OccupancyMillis != int64(count)*60000 || m.Peak != count {
			t.Fatalf("light=%v metric: %+v", light, m)
		}
		if result := SummarizeMinutes(1, values, nil, DefaultConfig(), 960, "total"); !result.ConcurrencyRisk {
			t.Fatalf("single pool saturation hidden: %+v", result)
		}
		for _, m := range writer.values {
			if m.Kind != "total" && m.SaturationMicrosMillis != nil {
				t.Fatal("category invented pool ratio")
			}
		}
	}
}

func TestServiceClassSaturationHistoricalNullAndExplicitZero(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ConsecutiveMinutes = 1
	m := Minute{Epoch: "old", UserID: 1, Minute: 600, Kind: "total", RPMLimit: 60, ConcurrencyLimit: 5, OccupancyMillis: 240000, ConfigRevision: 1, UpdatedAt: 660}
	if !SummarizeMinutes(1, []Minute{m}, nil, cfg, 660, "total").ConcurrencyRisk {
		t.Fatal("historical fallback lost")
	}
	zero := int64(0)
	m.SaturationMicrosMillis = &zero
	if SummarizeMinutes(1, []Minute{m}, nil, cfg, 660, "total").ConcurrencyRisk {
		t.Fatal("explicit zero treated as missing")
	}
	threshold := int64(48000000000)
	m.SaturationMicrosMillis = &threshold
	m.ConcurrencyLimit = 20
	m.OccupancyMillis = 0
	if !SummarizeMinutes(1, []Minute{m}, nil, cfg, 660, "total").ConcurrencyRisk {
		t.Fatal("normalized threshold boundary missed")
	}
}

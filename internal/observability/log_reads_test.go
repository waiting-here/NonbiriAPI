package observability

import (
	"context"
	"testing"
	"time"
)

func TestSlowLogShapeSharesOnePhaseSeries(t *testing.T) {
	p := NewPerformance()
	key := performanceKey{"log_read.steward.ids", ""}
	from, to := int64(987654321), int64(987654330)
	for i := range 250 {
		ctx := WithLogReadShape(p.Context(t.Context()), LogFilterShape{Status: true, CharityModel: i%2 == 1}, int64(i+1), &from, &to)
		shape := ctx.Value(logShapeKey{}).(*LogReadShape)
		p.beginSample(key)(time.Duration(i+1)*10*time.Millisecond, 0, performanceResult{shape: shape})
	}
	rows := p.Drain()
	if len(rows) != 1 || rows[0].Completed != 250 || rows[0].SlowCount != 201 {
		t.Fatal(rows)
	}
	shape := rows[0].SlowestShape
	if shape == nil || !shape.CharityModel || !shape.Status || shape.Page != "101+" || shape.TimeRange != "up_to_hour" {
		t.Fatal(shape)
	}
	ctx := p.Context(t.Context())
	LogReadPhase(ctx, "steward", "ids")(context.Canceled)
	LogReadPhase(ctx, "steward", "ids")(context.DeadlineExceeded)
	rows = p.Drain()
	if len(rows) != 1 || rows[0].Canceled != 1 || rows[0].Deadlines != 1 || rows[0].Failures != 0 || rows[0].SlowestShape != nil || rows[0].SlowCount != 0 {
		t.Fatal(rows)
	}
	for _, base := range []string{"/api/log-searches/", "/api/steward/log-searches/", "/admin/api/log-searches/"} {
		if path := logQueryRoute(base + "opaque"); path != base+"{id}" {
			t.Fatal(path)
		}
		if path := logQueryRoute(base + "opaque/more"); path != "" {
			t.Fatal(path)
		}
	}
}

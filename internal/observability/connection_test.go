package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestConnectionOccupancyAcrossWindowsAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := NewPerformance()
		ctx, cancel := context.WithCancel(t.Context())
		finish := p.StartConnectionSpan(ctx, "hold")
		time.Sleep(time.Minute)
		rows, httpStats := p.drain()
		if len(rows) != 1 || rows[0].Route != "occupancy.hold.unclassified" || rows[0].ActiveMS != 60000 || rows[0].Active != 1 || httpStats != (HTTPPerformance{}) {
			t.Fatalf("first window: %+v %+v", rows, httpStats)
		}
		time.Sleep(time.Second)
		cancel()
		finish(context.Canceled)
		finish(nil)
		rows, httpStats = p.drain()
		if len(rows) != 1 || rows[0].ActiveMS != 1000 || rows[0].Completed != 1 || rows[0].Canceled != 1 || rows[0].Active != 0 || httpStats != (HTTPPerformance{}) {
			t.Fatalf("second window: %+v %+v", rows, httpStats)
		}
		if rows := p.Drain(); len(rows) != 0 {
			t.Fatal(rows)
		}
	})
}

func TestConnectionOccupancyUsesOnlyFixedHTTPAndOperationRoots(t *testing.T) {
	p := NewPerformance()
	p.StartConnectionSpan(t.Context(), "private SQL")(nil)
	if len(p.Drain()) != 0 {
		t.Fatal("unknown driver class recorded")
	}
	handler := p.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.StartConnectionSpan(r.Context(), "hold")(nil)
		ctx := context.WithValue(r.Context(), connectionRootKey{}, "operation.catalog")
		p.StartConnectionSpan(ctx, "transaction")(nil)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/private-user/path?token=private", nil))
	rows := p.Drain()
	if len(rows) != 3 {
		t.Fatal(rows)
	}
	for _, row := range rows {
		if strings.Contains(row.Route, "private") || strings.Contains(row.Route, "token") {
			t.Fatal(row)
		}
		if strings.HasPrefix(row.Route, "occupancy.") && row.Method != "" {
			t.Fatal("database phase increased HTTP totals")
		}
	}
}

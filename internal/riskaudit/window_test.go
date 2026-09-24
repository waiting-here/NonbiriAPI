package riskaudit

import (
	"net/url"
	"testing"
)

func TestRelativeWindowUsesServerClock(t *testing.T) {
	const now = int64(1800000000)
	for _, hours := range []string{"1", "24", "168", "720"} {
		q := url.Values{"lookback_hours": {hours}}
		w, err := parseWindow(q, now, true)
		if err != nil || w.To != now || w.From >= w.To {
			t.Fatalf("hours=%s window=%+v error=%v", hours, w, err)
		}
	}
	for _, raw := range []string{"lookback_hours=0", "lookback_hours=721", "lookback_hours=01", "lookback_hours=24&from=1", "lookback_hours=24&to=0", "lookback_hours=24&lookback_hours=1"} {
		q, _ := url.ParseQuery(raw)
		if _, err := parseWindow(q, now, true); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	w, err := parseWindow(url.Values{"lookback_hours": {"168"}}, now, true)
	if err != nil || w.From != now-7*86400 {
		t.Fatalf("unexpected seven-day window: %+v %v", w, err)
	}
}

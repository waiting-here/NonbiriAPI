package timeapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestRecurrencePreviewSecondsAndMonthlyClamp(t *testing.T) {
	for _, path := range []string{"/api/time/recurrence", "/admin/api/time/recurrence"} {
		query := url.Values{"anchor_local": {"2027-01-31T03:00:01"}, "interval": {"month"}, "time_zone": {"UTC"}, "after": {"1801364401"}}
		request := httptest.NewRequest(http.MethodGet, path+"?"+query.Encode(), nil)
		recorder := httptest.NewRecorder()
		writeRecurrence(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatal(recorder.Body.String())
		}
		var value struct {
			Transitions []resolveResponse `json:"transitions"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		if len(value.Transitions) != 3 || value.Transitions[0].Local != "2027-02-28T03:00:01" || value.Transitions[1].Local != "2027-03-31T03:00:01" {
			t.Fatalf("transitions=%+v", value.Transitions)
		}
	}
}

func TestRecurrencePreviewRejectsInvalidQuery(t *testing.T) {
	base := url.Values{"anchor_local": {"2026-10-27T03:00:01"}, "interval": {"month"}, "time_zone": {"UTC"}, "after": {"0"}}
	for _, edit := range []func(url.Values){
		func(q url.Values) { q.Add("interval", "day") }, func(q url.Values) { q.Set("extra", "1") }, func(q url.Values) { q.Set("after", "-1") },
		func(q url.Values) { q.Set("anchor_local", "2026-02-30T00:00:00") }, func(q url.Values) { q.Set("time_zone", "Local") }, func(q url.Values) { q.Set("interval", "5h") },
	} {
		q := url.Values{}
		for key, value := range base {
			q[key] = append([]string(nil), value...)
		}
		edit(q)
		recorder := httptest.NewRecorder()
		writeRecurrence(recorder, httptest.NewRequest(http.MethodGet, "/api/time/recurrence?"+q.Encode(), nil))
		assertInvalidTimeResponse(t, recorder)
	}
}

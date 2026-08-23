package adminapi

import (
	"net/http"
	"testing"
)

func TestAdminSiteConfigAntiAbuseDefaultsAndTypes(t *testing.T) {
	e := newEnv(t)
	rec := adminGet(t, e, "/admin/api/site-config")
	var cfg map[string]any
	decodeJSON(t, rec, &cfg)
	want := map[string]any{
		KeyRPMBanThreshold:                  float64(5),
		KeyRPMBanWindowSeconds:              float64(86400),
		KeyRPMBanDurationSeconds:            float64(86400),
		KeyCharityMinChars:                  float64(20),
		KeyCharityViolationDeductMilli:      "0",
		KeyCharityViolationBanSeconds:       float64(0),
		KeyCharityViolationWindowSeconds:    float64(86400),
		KeyCharityViolationBanThreshold:     float64(0),
		KeyCharityViolationWindowBanSeconds: float64(0),
		KeyCharitySuspendWindowSeconds:      float64(86400),
		KeyCharitySuspendThreshold:          float64(0),
		KeyCharitySuspendDurationSeconds:    float64(0),
	}
	for key, expected := range want {
		if got := cfg[key]; got != expected {
			t.Fatalf("GET %s = %#v, want %#v", key, got, expected)
		}
	}
	for _, test := range []struct {
		key   string
		value any
	}{
		{KeyRPMBanThreshold, 0},
		{KeyCharityMinChars, 0},
		{KeyCharityViolationDeductMilli, "-1"},
		{KeyCharitySuspendDurationSeconds, 3600},
	} {
		rec := adminPatch(t, e, nil, "/admin/api/site-config/"+test.key, map[string]any{"value": test.value})
		if test.key == KeyCharityViolationDeductMilli {
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("negative deduction status=%d, body=%s", rec.Code, rec.Body.String())
			}
		} else if rec.Code != http.StatusOK {
			t.Fatalf("PATCH %s status=%d, body=%s", test.key, rec.Code, rec.Body.String())
		}
	}
}

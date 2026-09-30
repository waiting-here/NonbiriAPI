package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLegacyAuthDecodePolicy(t *testing.T) {
	for _, test := range []struct {
		raw, media   string
		strict, want bool
	}{
		{"null", "application/json", true, true},
		{"{\"value\":\"\\ud800\"}", "application/json", true, true},
		{"{\"value\":\"a\",\"value\":\"b\"}", "application/json", true, false},
		{"{\"value\":\"a\",\"value\":\"b\"}", "application/json", false, true},
		{"{}{}", "application/json", true, false},
		{"{}", "", true, false},
		{"{}", "APPLICATION/JSON; charset=utf-8", true, true},
	} {
		var body struct {
			Value string `json:"value"`
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.raw))
		r.Header.Set("Content-Type", test.media)
		if got := decodeJSONBody(w, r, &body, test.strict); got != test.want {
			t.Fatalf("%+v accepted=%v error=%s", test, got, w.Body)
		}
	}
	if requireEmptyQuery(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/?&&", nil)) || requireEmptyQuery(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/?", nil)) {
		t.Fatal("auth raw-empty query policy relaxed")
	}
}

package announcements

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnnouncementHTTPBoundaryCompatibility(t *testing.T) {
	for _, test := range []struct {
		raw, media string
		want       bool
	}{
		{"{\"value\":\"ok\"}", "", false},
		{"{\"value\":\"ok\"}", "text/plain", false},
		{"{\"value\":\"ok\"}", "APPLICATION/JSON; charset=utf-8", true},
		{"{\"value\":null}", "application/json", false},
		{"{\"value\":\"ok\"}{}", "application/json", false},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.raw))
		r.Header.Set("Content-Type", test.media)
		var body struct {
			Value requestField[string] `json:"value"`
		}
		if got := decodeStrictBody(w, r, &body); got != test.want {
			t.Fatalf("%q (%q): %v %s", test.raw, test.media, got, w.Body)
		}
	}
	for _, test := range []struct {
		path string
		want bool
	}{{"/?", false}, {"/?&&", true}, {"/?unexpected=1", false}} {
		if got := requireEmptyQuery(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, test.path, nil)); got != test.want {
			t.Fatalf("%s empty=%v", test.path, got)
		}
	}
	raw := "{\"items\":[" + strings.Repeat("{\"value\":0},", 299) + "{\"value\":0}]}"
	var body map[string]json.RawMessage
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if decodeStrictBody(w, r, &body) || w.Code != 400 {
		t.Fatal("announcement aggregate field budget relaxed")
	}
}

package resources

import (
	"encoding/json"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResourceHTTPBoundaryCompatibility(t *testing.T) {
	for _, test := range []struct {
		raw, media string
		want       bool
	}{
		{"{\"value\":\"ok\"}", "", true},
		{"{\"value\":\"ok\"}", "text/plain", true},
		{"{\"value\":null}", "", false},
		{"{\"value\":\"ok\",\"\\u0076alue\":\"bad\"}", "", false},
		{"{\"value\":\"ok\"}{}", "", false},
		{"{\"value\":\"\\ud800\"}", "", false},
		{"{\"unexpected\":true}", "", false},
	} {
		writer := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/endpoints", strings.NewReader(test.raw))
		request.Header.Set("Content-Type", test.media)
		var body struct {
			Value requestField[string] `json:"value"`
		}
		_, ok := decodeStrictObject(writer, request, &body)
		if ok != test.want {
			t.Fatalf("%q (%q): accepted=%v body=%s", test.raw, test.media, ok, writer.Body)
		}
		if !ok {
			var got struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if writer.Code != 400 || json.Unmarshal(writer.Body.Bytes(), &got) != nil || got.Error.Code != "invalid_request" {
				t.Fatalf("error drift: %d %s", writer.Code, writer.Body)
			}
		}
	}
	// This historical ingress accepts bare '?' and empty query separators.
	for _, raw := range []string{"/api/endpoints?", "/api/endpoints?&&"} {
		if !requireEmptyQuery(httptest.NewRecorder(), httptest.NewRequest(http.MethodDelete, raw, nil)) {
			t.Fatalf("empty query rejected: %s", raw)
		}
	}
	raw := "{\"items\":[" + strings.Repeat("{\"value\":0},", 299) + "{\"value\":0}]}"
	var body map[string]json.RawMessage
	if _, ok := decodeStrictObject(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", strings.NewReader(raw)), &body); !ok {
		t.Fatal("resource per-object batch budget tightened")
	}
}

func TestResourceHTTPBodyLimitKeepsStatusAndCode(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat(" ", idempotency.MaxControlBodyBytes+1)))
	writer := httptest.NewRecorder()
	var body map[string]json.RawMessage
	if _, ok := decodeStrictObject(writer, request, &body); ok {
		t.Fatal("accepted oversized body")
	}
	var got struct{ Error struct{ Code string } }
	if writer.Code != 413 || json.Unmarshal(writer.Body.Bytes(), &got) != nil || got.Error.Code != "payload_too_large" {
		t.Fatalf("limit error=%d %s", writer.Code, writer.Body)
	}
}

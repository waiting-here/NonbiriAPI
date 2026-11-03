package auth

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestManagementEffectFailsClosed(t *testing.T) {
	for _, c := range []struct {
		method, path string
		effect       ManagementEffect
	}{
		{"GET", "/admin/api/site-config", ManagementRead},
		{"POST", "/admin/api/inactivity-policy/preview", ManagementRead},
		{"POST", "/admin/api/ai/driver/models", ManagementDiscovery},
		{"POST", "/api/steward/donations/{id}/keys/{keyId}/models/refresh", ManagementDiscovery},
		{"POST", "/admin/api/future/preview", ManagementWrite},
		{"POST", "/admin/api/future/models/refresh", ManagementWrite},
		{"POST", "/admin/api/donations/{id}/review", ManagementWrite},
		{"POST", "/admin/api/ai/chat/plans/{id}/decision", ""},
		{"POST", "/api/steward/ai/chat/messages", ""},
		{"GET", "/api/automation/donations", ""},
		{"POST", "/admin/api/login", ""},
	} {
		if got := managementEffect(c.method, c.path); got != c.effect {
			t.Errorf("%s %s = %s want %s", c.method, c.path, got, c.effect)
		}
	}
}

func TestManagementStreamAndLargeReceiptsRemainExplicit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := &managementResponse{header: make(http.Header), cancel: cancel}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Flush()
	if ctx.Err() != nil {
		t.Fatal("empty initial flush stopped before any event")
	}
	_, _ = w.Write([]byte("data: example\n\n"))
	w.Flush()
	raw, err := managementReceipt(w)
	if err != nil || ctx.Err() == nil || !strings.Contains(string(raw), `"stream_sample":true`) {
		t.Fatal(string(raw), err)
	}
	w = &managementResponse{header: make(http.Header)}
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(strings.Repeat("x", (1<<20)+1)))
	raw, err = managementReceipt(w)
	if err != nil || !strings.Contains(string(raw), `"status_code":202`) || !strings.Contains(string(raw), "body_omitted") || len(raw) > 1024 {
		t.Fatal("large receipt invented a failed submission", string(raw), err)
	}
}

func TestManagementRequestCannotChangeRouteOrHeaders(t *testing.T) {
	for _, value := range []string{"..", "../admin", "a/b", "a\\b", "a\r\nb", ""} {
		if _, err := managementHTTPRequest(context.Background(), ManagementRequest{Route: "GET /api/steward/donations/{id}", PathParams: map[string]string{"id": value}}); err == nil {
			t.Fatal("unsafe path", value)
		}
	}
	req, err := managementHTTPRequest(context.Background(), ManagementRequest{Route: "GET /admin/api/users/{id}", PathParams: map[string]string{"id": "123"}, Query: map[string]string{"q": "a&b"}})
	if err != nil || req.PathValue("id") != "123" || req.URL.Query().Get("q") != "a&b" || strings.Contains(req.URL.RawQuery, "q=a&b") {
		t.Fatal(req, err)
	}
}

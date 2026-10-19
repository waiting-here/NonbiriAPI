package resourcebridge

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/connector/anthropic"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/headerfacts"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

func TestDiscoveryPersistsEveryRealPaginatedResponseHeader(t *testing.T) {
	var pages atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := pages.Add(1)
		w.Header().Set("X-Page", fmt.Sprint(index))
		w.Header().Add("Set-Cookie", "synthetic-private")
		w.Header().Set("Content-Type", "application/json")
		if index == 1 {
			_, _ = io.WriteString(w, `{"data":[{"type":"model","id":"first","display_name":"First","created_at":"2026-01-01T00:00:00Z"}],"has_more":true,"first_id":"first","last_id":"first"}`)
		} else {
			if r.URL.Query().Get("after_id") != "first" {
				t.Error("pagination cursor lost")
			}
			_, _ = io.WriteString(w, `{"data":[{"type":"model","id":"second","display_name":"Second","created_at":"2026-01-01T00:00:00Z"}],"has_more":false,"first_id":"second","last_id":"second"}`)
		}
	}))
	defer server.Close()
	f := newBridgeFixture(t)
	stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.CloseIdleConnections()
	if err = stack.AddSelfOrigins(context.Background(), &config.Config{SiteBaseURL: "http://127.0.0.1:1", UserHost: "127.0.0.1", AdminHost: "127.0.0.2", ListenAddr: "127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	local, err := backend.NewLocal(stack)
	if err != nil {
		t.Fatal(err)
	}
	f.runtime.backend = local
	observed, err := observability.NewRepository(f.store.DB())
	if err != nil {
		t.Fatal(err)
	}
	f.runtime.errorScope = observed.DiscoveryScope
	owner := f.seedUser(t, "header-pages")
	key := f.seedEndpointKey(t, owner, "header-pages", contract.TypeAnthropicCompatible, "synthetic-upstream-key", server.URL+"/v1")
	result, err := f.runtime.Discover(context.Background(), f.discoveryInput(t, key, anthropic.ModelDiscoverer{}))
	if err != nil || !result.Succeeded || len(result.Models) != 2 || pages.Load() != 2 {
		t.Fatal(result, err, pages.Load())
	}
	facts := f.latestDiscoveryFacts(t)
	var state string
	var encoded []byte
	if err = f.store.DB().QueryRow("SELECT h.save_state,h.encoded FROM request_header_facts h JOIN request_logs l ON l.id=h.request_log_id WHERE l.logical_request_id=? AND h.attempt_seq=1", facts.requestID).Scan(&state, &encoded); err != nil {
		t.Fatal(err)
	}
	view, err := headerfacts.DecodeCollection(state, encoded, false)
	if err != nil || len(view.Responses) != 2 {
		t.Fatal(view, err)
	}
	for i, response := range view.Responses {
		found := false
		for _, entry := range response.Headers {
			if entry.Name == "X-Page" {
				found = entry.Value == fmt.Sprint(i+1)
			}
			if entry.Name == "Set-Cookie" && entry.Value != "[redacted]" {
				t.Fatal("response cookie persisted")
			}
		}
		if !found || response.ResponseIndex != i+1 {
			t.Fatal(view)
		}
	}
}

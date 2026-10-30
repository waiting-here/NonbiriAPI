package agentrequest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

func TestDriverDiscoveryFailureFactsAndPersistence(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		code       string
		httpStatus int
	}{
		{"authentication", 401, `{"error":{"code":"invalid_api_key","message":"bad key"}}`, "upstream_authentication", 502},
		{"ambiguous forbidden", 403, `{"error":{"code":"forbidden"}}`, "upstream_status", 502},
		{"workspace restriction", 403, `{"error":{"code":"workspace_deactivated"}}`, "upstream_account_restricted", 502},
		{"rate limited", 429, `{"error":{"code":"rate_limit"}}`, "upstream_rate_limited", 503},
		{"unavailable", 503, "<html>unavailable</html>", "upstream_status", 502},
		{"invalid json", 200, "<html>unexpected</html>", "invalid_json", 502},
		{"shape", 200, `{"data":{}}`, "json_type_mismatch", 502},
		{"missing catalogue", 200, `{"other":[]}`, "invalid_response", 502},
		{"oversize", 200, strings.Repeat("x", (4<<20)+1), "response_limit", 502},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer "+fixtureKey {
					t.Error("incorrect catalogue request")
				}
				w.Header().Set("X-Request-ID", fixtureKey)
				w.Header().Set("CF-Ray", "safe-trace")
				w.Header().Set("Retry-After", "9")
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			})
			ddl, err := os.ReadFile("../db/agent_operation_diagnostics.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.database.Exec(string(ddl)); err != nil {
				t.Fatal(err)
			}
			_, err = f.runtime.DiscoverDriverModels(t.Context(), 0)
			var failure *diagnostic.OperationError
			if !errors.As(err, &failure) || failure.SiteStatus != tc.httpStatus || failure.Details.Code != tc.code || failure.Details.UpstreamStatus != tc.status || !failure.Saved {
				t.Fatalf("wrong discovery failure: %+v %v", failure, err)
			}
			raw, _ := json.Marshal(failure)
			if strings.Contains(string(raw), fixtureKey) || strings.Contains(string(raw), "<html>") {
				t.Fatal("unsafe diagnostic")
			}
			saved, err := f.runtime.OperationDiagnostic(t.Context(), failure.ID)
			if err != nil || saved.Details.Code != tc.code {
				t.Fatal("diagnostic not retrievable", err)
			}
			denied := errors.New("denied")
			f.runtime.adminAuthorize = func(context.Context, *sql.Tx) error { return denied }
			if _, err = f.runtime.OperationDiagnostic(t.Context(), failure.ID); !errors.Is(err, denied) {
				t.Fatal("diagnostic bypassed admin gate", err)
			}
		})
	}
}
func TestCodexCatalogueIdentityEmptyAndAdditionalFields(t *testing.T) {
	for _, body := range []string{`{"models":[]}`, `{"models":[{"slug":"model-a","display_name":"Model A","visibility":"list","future":{"any":1}},{"slug":"hidden","visibility":"hide"}],"future":true}`} {
		f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.RawQuery != "client_version=0.0.0" || r.Header.Get("ChatGPT-Account-ID") != "workspace" || r.Header.Get("originator") != "nonbiriapi" || r.Header.Get("Authorization") != "Bearer codex-access-secret" {
				t.Error("incorrect native catalogue identity")
			}
			w.Write([]byte(body))
		})
		original, _ := f.runtime.Settings(t.Context())
		p := attachCodex(t, f)
		p.Settings.BaseURL = original.BaseURL
		raw, _ := json.Marshal(p.Settings)
		if _, err := f.database.Exec("UPDATE agent_driver_profiles SET settings_json=? WHERE id=?", string(raw), p.ID); err != nil {
			t.Fatal(err)
		}
		models, err := f.runtime.DiscoverDriverModels(t.Context(), p.ID)
		if err != nil || models == nil || len(models) > 1 {
			t.Fatal(models, err)
		}
	}
}
func TestCatalogueInterruptedReadKeepsReceivedStatus(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(200)
		w.Write([]byte(`{"data":`))
	})
	_, err := f.runtime.DiscoverDriverModels(t.Context(), 0)
	var failure *diagnostic.OperationError
	if !errors.As(err, &failure) || failure.Details.Phase != "read_response" || failure.Details.UpstreamStatus != 200 || failure.SiteStatus != 502 {
		t.Fatal(failure, err)
	}
}
func TestCodexRetainedUsageAndBoundedRecordScope(t *testing.T) {
	f := newFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("local usage contacted upstream") })
	attachCodex(t, f)
	now := f.clock.Load()
	add := func(provider string, at int64, unknown bool) int64 {
		result, err := f.database.Exec(`INSERT INTO agent_requests(client_request_id,source,kind,actor_id,actor_role,actor_label,input_json,state,created_at,completed_at,consumed_at) VALUES(?,'admin_chat','model',0,'admin','','{}','succeeded',?,?,?)`, fmt.Sprintf("%s-%d", provider, at), at, at, at)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := result.LastInsertId()
		if _, err = f.database.Exec(`INSERT INTO agent_request_drivers VALUES(?,0,?,'{}')`, id, provider); err != nil {
			t.Fatal(err)
		}
		if _, err = f.database.Exec(`INSERT INTO agent_request_usage(request_id,client_request_id,source,kind,actor_id,actor_role,actor_label,recorded_at,input_tokens,cached_input_tokens,cache_write_input_tokens,output_tokens,reasoning_tokens,unknown) VALUES(?,?,'admin_chat','model',0,'admin','',?,10,0,0,2,0,?)`, id, fmt.Sprint(id), at, unknown); err != nil {
			t.Fatal(err)
		}
		return id
	}
	add(ProviderCodex, now-2000, false)
	add(ProviderCodex, now-1000, true)
	other := add(ProviderChatGPT, now-500, false)
	add(ProviderCodex, now-(25*time.Hour).Milliseconds(), false)
	// Keep the only write connection busy. A pure usage read must still finish.
	tx, err := f.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	usage := f.runtime.codexLocalUsage(t.Context())
	tx.Rollback()
	if usage.Data == nil || usage.Data.Requests != 2 || usage.Data.InputTokens != 20 || usage.Data.UnknownCount != 1 || usage.Data.Scope != "all_codex_connections" {
		t.Fatal(usage)
	}
	filter := ListFilter{Provider: ProviderCodex, From: usage.Data.WindowStart, To: now, Page: 1, PageSize: 20}
	page, err := f.runtime.List(t.Context(), filter)
	if err != nil || page.Total != 2 {
		t.Fatal(page, err)
	}
	cleaned, err := f.runtime.Cleanup(t.Context(), CleanupInput{Provider: ProviderCodex, From: filter.From, To: filter.To, ThroughID: page.CutoffID})
	if err != nil || cleaned.DeletedCount != 2 {
		t.Fatal(cleaned, err)
	}
	if _, err = f.runtime.Detail(t.Context(), other); err != nil {
		t.Fatal("filtered cleanup removed SIWC", err)
	}
	filter.From = now - (25 * time.Hour).Milliseconds()
	if _, err = f.runtime.List(t.Context(), filter); !errors.Is(err, ErrInvalid) {
		t.Fatal("unbounded range accepted")
	}
}

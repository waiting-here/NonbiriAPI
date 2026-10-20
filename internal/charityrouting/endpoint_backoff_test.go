package charityrouting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

func TestEndpointBackoffOptionsUseAuthorizedRegistry(t *testing.T) {
	e := endpointPolicyEnv(t)
	level := int64(6)
	actor := e.seedUser(t, false, &level)
	api := httpAPI{service: e.service}
	for _, role := range []roleKind{roleAdmin, roleSteward} {
		w := httptest.NewRecorder()
		api.getEndpointBackoffOptions(w, httptest.NewRequest(http.MethodGet, "/options", nil), role, actor)
		var out EndpointBackoffOptions
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		want := []string{}
		for _, kind := range e.service.connectors.Types() {
			want = append(want, string(kind))
		}
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !reflect.DeepEqual(out.ConnectorTypes, want) {
			t.Fatal(w.Code, out)
		}
	}
	if _, err := e.store.DB().Exec(`UPDATE users SET level=5 WHERE id=?`, actor); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	api.getEndpointBackoffOptions(w, httptest.NewRequest(http.MethodGet, "/options", nil), roleSteward, actor)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	for _, r := range []*http.Request{httptest.NewRequest(http.MethodGet, "/options?q=x", nil), httptest.NewRequest(http.MethodGet, "/options", strings.NewReader("{}"))} {
		w := httptest.NewRecorder()
		api.getEndpointBackoffOptions(w, r, roleAdmin, 0)
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}

func endpointPolicyEnv(t *testing.T) *routingTestEnv {
	t.Helper()
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	e.service.connectors = connector.NewDefaultRegistry()
	p, err := egress.NewEgressPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	e.service.baseURLs = p
	return e
}

func TestEndpointBackoffPolicyRevisionReplayAndRoleBoundary(t *testing.T) {
	e := endpointPolicyEnv(t)
	level := int64(6)
	steward := e.seedUser(t, false, &level)
	input := EndpointBackoffPolicyInput{ConnectorType: "openai-compatible", BaseURL: "https://api.runable.com/api/gateway/v3/ai", ExpectedRevision: "0", Rules: []smartbackoff.Rule{{Kind: "code", Value: "insufficient_credits"}}}
	current, err := e.service.endpointBackoffPolicy(t.Context(), roleSteward, steward, input.ConnectorType, input.BaseURL)
	if err != nil || current.Revision != "0" || current.Rules == nil {
		t.Fatal(current, err)
	}
	mutation := routingMutation(t, 'a', http.MethodPut, routeStewardEndpointBackoffPolicy, nil, input)
	for i := 0; i < 2; i++ {
		out, err := e.service.saveEndpointBackoffPolicy(t.Context(), roleSteward, steward, mutation, input)
		if err != nil || out.Value.Revision != "1" || out.Value.Rules[0].Period != "auto" || out.Replayed != (i == 1) {
			t.Fatal(out, err)
		}
	}
	if _, err := e.store.DB().Exec(`UPDATE users SET level=5 WHERE id=?`, steward); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.saveEndpointBackoffPolicy(t.Context(), roleSteward, steward, mutation, input); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked replay", err)
	}
	if _, err := e.service.endpointBackoffPolicy(t.Context(), roleSteward, steward, input.ConnectorType, input.BaseURL); !errors.Is(err, ErrForbidden) {
		t.Fatal("trainee read", err)
	}
	if _, err := e.service.endpointBackoffPolicies(t.Context(), roleSteward, steward, "", pagination.Default()); !errors.Is(err, ErrForbidden) {
		t.Fatal("trainee list", err)
	}
	clear := input
	clear.ExpectedRevision = "1"
	clear.Rules = []smartbackoff.Rule{}
	out, err := e.service.saveEndpointBackoffPolicy(t.Context(), roleAdmin, 0, routingMutation(t, 'b', http.MethodPut, routeAdminEndpointBackoffPolicy, nil, clear), clear)
	if err != nil || out.Value.Revision != "2" || out.Value.Rules == nil || len(out.Value.Rules) != 0 {
		t.Fatal(out, err)
	}
	if _, err := e.service.saveEndpointBackoffPolicy(t.Context(), roleAdmin, 0, routingMutation(t, 'c', http.MethodPut, routeAdminEndpointBackoffPolicy, nil, input), input); !errors.Is(err, ErrConflict) {
		t.Fatal("ABA accepted", err)
	}
	for _, role := range []roleKind{roleAdmin, roleSteward} {
		if role == roleSteward {
			if _, err := e.store.DB().Exec(`UPDATE users SET level=6 WHERE id=?`, steward); err != nil {
				t.Fatal(err)
			}
		}
		got, err := e.service.endpointBackoffPolicy(t.Context(), role, steward, input.ConnectorType, input.BaseURL+"/other")
		if err != nil || got.Revision != "0" {
			t.Fatal("path shared policy", got, err)
		}
		got, err = e.service.endpointBackoffPolicy(t.Context(), role, steward, "anthropic-compatible", input.BaseURL)
		if err != nil || got.Revision != "0" {
			t.Fatal("connector shared policy", got, err)
		}
	}
}

func TestEndpointBackoffPolicyListAndStrictHTTP(t *testing.T) {
	e := endpointPolicyEnv(t)
	owner := e.seedUser(t, false, nil)
	e.seedCandidate(t, owner, 'a', "first")
	e.seedCandidate(t, owner, 'b', "second")
	input := EndpointBackoffPolicyInput{ConnectorType: "openai-compatible", BaseURL: "HTTPS://API.RUNABLE.COM/api/gateway/v3/ai", ExpectedRevision: "0", Rules: []smartbackoff.Rule{{Kind: "code", Value: "insufficient_credits", Period: "auto"}}}
	got, err := e.service.saveEndpointBackoffPolicy(t.Context(), roleAdmin, 0, routingMutation(t, 'a', http.MethodPut, routeAdminEndpointBackoffPolicy, nil, input), input)
	if err != nil || got.Value.BaseURL != "https://api.runable.com/api/gateway/v3/ai" {
		t.Fatal(got, err)
	}
	page, err := e.service.endpointBackoffPolicies(t.Context(), roleAdmin, 0, "RUNABLE", pagination.Default())
	if err != nil || len(page.Data) != 1 || page.Data[0].KeyCount != "0" || page.Data[0].RuleCount != "1" || page.Pagination.TotalItems != "1" {
		t.Fatal(page, err)
	}
	page, err = e.service.endpointBackoffPolicies(t.Context(), roleAdmin, 0, "", pagination.Default())
	if err != nil || len(page.Data) != 3 || page.Pagination.TotalItems != "3" {
		t.Fatal(page, err)
	}
	wantCounts := map[string]string{"https://a.routing.test/v1": "1", "https://b.routing.test/v1": "1", got.Value.BaseURL: "0"}
	for _, item := range page.Data {
		if item.KeyCount != wantCounts[item.BaseURL] {
			t.Fatal("endpoint key aggregation", item)
		}
		delete(wantCounts, item.BaseURL)
	}
	if len(wantCounts) != 0 {
		t.Fatal("endpoints omitted", wantCounts)
	}
	for i := 0; i < 10; i++ {
		base := fmt.Sprintf("https://z%02d.routing.test/v1", i)
		if i == 0 {
			base = "https://a.routing.test/v1"
		}
		if _, err := e.store.DB().Exec(`INSERT INTO endpoint_backoff_policies(connector_type,base_url,revision,rules_json) VALUES('openai-compatible',?,1,'[]')`, base); err != nil {
			t.Fatal(err)
		}
	}
	for number, size := range map[int64]int{1: 10, 2: 2} {
		page, err := e.service.endpointBackoffPolicies(t.Context(), roleAdmin, 0, "", pagination.Request{Page: number, Size: 10})
		if err != nil || len(page.Data) != size || page.Pagination.TotalItems != "12" || page.Pagination.TotalPages != "2" {
			t.Fatal("grouped pagination", page, err)
		}
		for _, item := range page.Data {
			if item.BaseURL == "https://a.routing.test/v1" && (item.KeyCount != "1" || item.Revision != "1" || item.RuleCount != "0") {
				t.Fatal("configured donation endpoint counted twice", item)
			}
		}
	}
	if _, err := e.service.endpointBackoffPolicies(t.Context(), roleAdmin, 0, strings.Repeat("文", 128), pagination.Default()); err != nil {
		t.Fatal("maximum search rejected", err)
	}
	if _, err := e.service.endpointBackoffPolicies(t.Context(), roleAdmin, 0, strings.Repeat("文", 129), pagination.Default()); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("oversized search accepted", err)
	}
	api := httpAPI{service: e.service}
	base := `{"connector_type":"openai-compatible","base_url":"https://api.runable.com/api/gateway/v3/ai","expected_revision":"1","rules":[{"kind":"code","value":"insufficient_credits"}]}`
	for _, body := range []string{strings.Replace(base, `"rules":[`, `"extra":true,"rules":[`, 1), strings.Replace(base, `"value":"insufficient_credits"`, `"value":"insufficient_credits","period":null`, 1), strings.Replace(base, `"kind":"code"`, `"kind":"code","other":true`, 1), strings.Replace(base, `"openai-compatible"`, `"unknown"`, 1), strings.Replace(base, `https://api.runable.com/api/gateway/v3/ai`, `http://localhost`, 1), strings.Replace(base, `"rules":[{"kind":"code","value":"insufficient_credits"}]`, `"rules":null`, 1)} {
		r := httptest.NewRequest(http.MethodPut, routeAdminEndpointBackoffPolicy, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", strings.Repeat("d", 32))
		w := httptest.NewRecorder()
		api.adminSaveEndpointBackoffPolicy(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"?q=a&q=b", "?cursor=x", "?page_size=9"} {
		w := httptest.NewRecorder()
		api.adminEndpointBackoffPolicies(w, httptest.NewRequest(http.MethodGet, routeAdminEndpointBackoffPolicies+query, nil))
		if w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	q := url.Values{"connector_type": {"openai-compatible"}, "base_url": {got.Value.BaseURL}}
	w := httptest.NewRecorder()
	api.adminEndpointBackoffPolicy(w, httptest.NewRequest(http.MethodGet, routeAdminEndpointBackoffPolicy+"?"+q.Encode(), nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	e.service.baseURLs = nil
	if _, err := e.service.endpointBackoffPolicies(t.Context(), roleAdmin, 0, "", pagination.Default()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestEndpointBackoffPolicyMaximumEscapedHTTPBody(t *testing.T) {
	e := endpointPolicyEnv(t)
	prefix := "https://api.runable.com/"
	input := EndpointBackoffPolicyInput{ConnectorType: "openai-compatible", BaseURL: prefix + strings.Repeat("&", 4096-len(prefix)), ExpectedRevision: "0", Rules: make([]smartbackoff.Rule, 8)}
	for i := range input.Rules {
		input.Rules[i] = smartbackoff.Rule{Kind: "message", Value: strings.Repeat("<", 160), Period: "five_hour"}
	}
	encoded, err := json.Marshal(input)
	if err != nil || len(encoded) <= 16<<10 || len(encoded) >= 36<<10 {
		t.Fatal("unexpected maximum escaped request size", len(encoded), err)
	}
	api := httpAPI{service: e.service}
	for _, size := range []int{len(encoded), 36<<10 + 1} {
		body := string(encoded) + strings.Repeat(" ", size-len(encoded))
		r := httptest.NewRequest(http.MethodPut, routeAdminEndpointBackoffPolicy, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", strings.Repeat("M", 32))
		w := httptest.NewRecorder()
		api.adminSaveEndpointBackoffPolicy(w, r)
		want := http.StatusOK
		if size > 36<<10 {
			want = http.StatusRequestEntityTooLarge
		}
		if w.Code != want {
			t.Fatal(size, w.Code, w.Body.String())
		}
	}
	t.Logf("maximum escaped request bytes=%d", len(encoded))
}

func TestEndpointBackoffPolicyReadsDoNotWaitForWriter(t *testing.T) {
	e := endpointPolicyEnv(t)
	level := int64(6)
	steward := e.seedUser(t, false, &level)
	level = 5
	trainee := e.seedUser(t, false, &level)
	writer, err := e.store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	for _, role := range []roleKind{roleAdmin, roleSteward} {
		if _, err := e.service.endpointBackoffOptions(ctx, role, steward); err != nil {
			t.Fatal("options waited for writer", role, err)
		}
		if _, err := e.service.endpointBackoffPolicy(ctx, role, steward, "openai-compatible", "https://api.runable.com/v1"); err != nil {
			t.Fatal("detail waited for writer", role, err)
		}
		if _, err := e.service.endpointBackoffPolicies(ctx, role, steward, "", pagination.Default()); err != nil {
			t.Fatal("list waited for writer", role, err)
		}
	}
	if _, err := e.service.endpointBackoffOptions(ctx, roleSteward, trainee); !errors.Is(err, ErrForbidden) {
		t.Fatal("trainee options authorized", err)
	}
	if _, err := e.service.endpointBackoffPolicy(ctx, roleSteward, trainee, "openai-compatible", "https://api.runable.com/v1"); !errors.Is(err, ErrForbidden) {
		t.Fatal("trainee detail authorized", err)
	}
	if _, err := e.service.endpointBackoffPolicies(ctx, roleSteward, trainee, "", pagination.Default()); !errors.Is(err, ErrForbidden) {
		t.Fatal("trainee list authorized", err)
	}
	if e.service.readDB.Stats().InUse != 0 {
		t.Fatal("read connection leaked")
	}
}

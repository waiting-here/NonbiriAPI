package app

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

// Reuse the pure connector preflight and production zero-cost rejection
// recorder; the original option name and content never enter the log seed.
func seedRequestRejectionsBrowser(t *testing.T, f *imageBrowserFixture) map[string]any {
	t.Helper()
	const field = "PRIVATE_REJECTION_OPTION"
	const value = "PRIVATE_REJECTION_VALUE"
	const input = "PRIVATE_REJECTION_INPUT"
	body := `{"model":"[公益]synthetic/rejection","messages":[{"role":"user","content":"` + input + `"}],"` + field + `":"` + value + `"}`
	request, err := openai.DecodeChatRequest(strings.NewReader(body), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	err = connector.NewDefaultRegistry().CheckTargetRequest(contract.NewTarget(contract.TypeAnthropicCompatible, "https://synthetic.invalid", "model"), contract.OperationChatCompletions, request, nil, contract.AttemptPolicy{})
	var rejected *contract.RequestRejection
	if !errors.As(err, &rejected) {
		t.Fatal("expected synthetic unsupported field rejection", err)
	}
	owner, err := strconv.ParseInt(f.users[0].ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	record := func(detail *requestattempt.RejectionDetail) string {
		ctx, id, err := requestattempt.New(t.Context(), owner, "POST", "/v1/chat/completions")
		if err != nil {
			t.Fatal(err)
		}
		requestattempt.Classify(ctx, "charity")
		fact := requestattempt.Fact{ID: id, Method: "POST", Path: "/v1/chat/completions", Stage: "preflight", Reason: "invalid_request", Model: "[公益]synthetic/rejection", Status: 400, Code: "invalid_request", Detail: detail}
		if err = f.app.Load().claims.RecordRejection(ctx, owner, fact); err != nil {
			t.Fatal(err)
		}
		return id
	}
	return map[string]any{
		"precise_id":      record(requestattempt.NewDetail(rejected.Field, rejected.Reason)),
		"historical_id":   record(requestattempt.NewDetail("request", "unsupported top-level field")),
		"unbound_id":      requestUnboundBrowserModel(t, f),
		"private_markers": []string{field, value, input},
	}
}

func requestUnboundBrowserModel(t *testing.T, f *imageBrowserFixture) string {
	t.Helper()
	if _, err := f.store.DB().Exec("UPDATE site_config SET value='1' WHERE key='charity_enabled'"); err != nil {
		t.Fatal(err)
	}
	f.call("POST", "/admin/api/charity-models", map[string]any{
		"provider": "browser", "model": "unbound", "enabled": true, "is_mainstream": false,
		"flatten_tool_calls": false, "pricing": map[string]any{"mode": "per_request", "user_price": "0", "donor_reward": "0"},
		"discount": map[string]any{"enabled": false, "percent": 100, "start_at": nil, "end_at": nil},
	}, f.adminCookie, true)
	cookie := f.users[0].Cookie
	keyState := f.request("GET", "/api/caller-key", nil, cookie, false)
	reply := f.call("POST", "/api/caller-key/regenerate", map[string]string{
		"expected_generation": keyState.Header().Get("X-Nonbiri-CallerKey-Generation"),
	}, cookie, false)
	out := testApplicationRequest(t, f.app.Load().handler, http.MethodPost, f.cfg.UserHost, "/v1/chat/completions",
		wireBody(t, map[string]any{"model": "[公益]browser/unbound", "messages": []map[string]string{
			{"role": "user", "content": strings.Repeat("sample text ", 64)},
		}}), nil, map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + reply["secret"].(string)})
	if out.Code != http.StatusServiceUnavailable || out.Header().Get("X-Request-ID") == "" {
		t.Fatalf("unbound charity request: %d %s", out.Code, out.Body.String())
	}
	return out.Header().Get("X-Request-ID")
}

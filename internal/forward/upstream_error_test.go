package forward

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func reportedErrorForTest() upstreamerror.Detail {
	context := upstreamerror.Context{BaseURL: "https://upstream.example/v1", ContainsSecret: func(value []byte) bool { return bytes.Contains(value, []byte("secret-value")) }}
	return context.Parse([]byte(`{"error":{"message":"Capacity exhausted; retry later.","code":"overloaded"}}`))
}

type errorTestResolver struct{}

func (errorTestResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}

// Exercise the real egress, registry, adapters and caller sink together. Route
// admission and accounting use the same deterministic rails as the other
// service tests; no external provider or persistent account is involved.
func TestForwardCallerReceivesSafeUpstreamError(t *testing.T) {
	for _, kind := range []connectorcontract.Type{connectorcontract.TypeOpenAICompatible, connectorcontract.TypeAnthropicCompatible} {
		for _, charity := range []bool{false, true} {
			for _, status := range []int{200, 400, 401, 429, 500, 503, 504, 599} {
				t.Run(fmt.Sprintf("%s/charity=%t/status=%d", kind, charity, status), func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("Location", "https://hidden.example/retry")
						w.Header().Set("Set-Cookie", "private=1")
						w.WriteHeader(status)
						_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
							"code": "quota_exhausted", "message": "Quota exhausted; retry later. secret-value ciphertext-value hidden.example " + r.Host + " upstream-model", "headers": "hidden diagnostics",
						}})
					}))
					defer server.Close()
					stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{server.URL}, Resolver: errorTestResolver{}, RequestTimeout: 3 * time.Second, Concurrency: egress.ConcurrencyLimits{Global: 8, PerEndpoint: 4}})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(stack.CloseIdleConnections)
					if err := stack.AddSelfOrigins(context.Background(), &config.Config{SiteBaseURL: "https://gateway.example", UserHost: "gateway.example", AdminHost: "admin.gateway.example", ListenAddr: "127.0.0.1:1"}); err != nil {
						t.Fatal(err)
					}
					local, err := backend.NewLocal(stack)
					if err != nil {
						t.Fatal(err)
					}
					f := newServiceFixture(t, nil)
					actual, err := f.service.registry.NewConnector(kind, connector.Dependencies{Backend: local})
					if err != nil {
						t.Fatal(err)
					}
					f.service.connectors[kind] = actual
					candidate := f.personal.snapshot.Candidates[0]
					model := "provider/model"
					if charity {
						candidate = f.charity.snapshot.Candidates[0]
						model = "[公益]care/model"
					}
					candidate.CanonicalBaseURL, candidate.ConnectorType = server.URL, kind
					if charity {
						f.charity.snapshot.Candidates = []RouteCandidate{candidate}
					} else {
						f.personal.snapshot.Candidates = []RouteCandidate{candidate}
					}
					f.addDispatch(candidate)
					f.charges.charge = 0
					request := decodeChatForTest(t, fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}]}`, model))
					recorder := httptest.NewRecorder()
					f.service.Chat(context.Background(), recorder, 1, request, []byte(`{}`), "application/json", "en")
					wantStatus := status
					if status == 200 {
						wantStatus = 502
					}
					var envelope httperr.Envelope
					if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
						t.Fatalf("status=%d body=%q err=%v", recorder.Code, recorder.Body.String(), err)
					}
					if recorder.Code != wantStatus || envelope.Error.Code != httperr.CodeUpstream || envelope.Error.Source != httperr.SourceUpstream || envelope.Error.UpstreamCode != "quota_exhausted" || !strings.Contains(envelope.Error.Message, "Quota exhausted; retry later.") {
						t.Fatalf("status=%d envelope=%+v", recorder.Code, envelope)
					}
					for _, forbidden := range []string{"secret-value", "ciphertext-value", "hidden.example", "127.0.0.1", "upstream-model", "hidden diagnostics"} {
						if strings.Contains(recorder.Body.String(), forbidden) {
							t.Fatalf("caller error leaked %q", forbidden)
						}
					}
					if charity && envelope.Error.Diag != "" {
						t.Fatal("charity exposed a diagnostic")
					}
					if recorder.Header().Get("Location") != "" || recorder.Header().Get("Set-Cookie") != "" {
						t.Fatal("upstream headers reached caller")
					}
					if len(f.claims.outcomes) != 1 || len(f.claims.requestResults) != 1 {
						t.Fatalf("outcomes=%d completions=%d", len(f.claims.outcomes), len(f.claims.requestResults))
					}
					outcome, terminal := f.claims.outcomes[0], f.claims.requestResults[0]
					if outcome.ProtocolSuccess || outcome.ResponseStarted || outcome.UpstreamStatus != status || outcome.UpstreamCode != "quota_exhausted" || terminal.Caller.Status != wantStatus || terminal.Caller.ErrorCode != httperr.CodeUpstream || terminal.Disposition != claim.AccountingCommit || terminal.ActualChargeMilli != 0 {
						t.Fatalf("outcome=%+v terminal=%+v", outcome, terminal)
					}
				})
			}
		}
	}
}

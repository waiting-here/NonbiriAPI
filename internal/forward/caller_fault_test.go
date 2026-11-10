package forward

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
)

func TestForwardCallerParameterFailureKeepsErrorAndRetryContract(t *testing.T) {
	for _, tc := range []struct{ charity, stream, adapted, inputLength, gateway bool }{
		{false, false, false, false, false}, {false, true, false, false, false}, {true, false, false, false, false}, {true, true, false, false, false}, {true, false, true, false, false},
		{false, false, false, true, false}, {true, false, false, true, false}, {true, true, false, true, false}, {true, false, true, true, false},
		{true, false, false, false, true}, {true, true, false, false, true}, {false, false, false, false, true}, {true, false, true, false, true},
	} {
		charity, stream := tc.charity, tc.stream
		t.Run(fmt.Sprintf("charity=%t/stream=%t/adapted=%t/inputLength=%t/gateway=%t", charity, stream, tc.adapted, tc.inputLength, tc.gateway), func(t *testing.T) {
			code := "unsupported_parameter"
			if tc.inputLength {
				code = "invalid_value"
			}
			kind := contract.TypeOpenAICompatible
			if tc.gateway {
				code, kind = "AI_APICallError", contract.TypeAISDKGatewayV3
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var sent map[string]json.RawMessage
				expected := "0.25"
				if tc.adapted {
					expected = "0.5"
				}
				if json.NewDecoder(r.Body).Decode(&sent) != nil || string(sent["temperature"]) != expected {
					t.Error("outbound override not exercised")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(400)
				if tc.gateway {
					if string(sent["maxOutputTokens"]) != "100000" {
						t.Error("caller output budget was not translated")
					}
					const message = "Unable to submit request because it has a maxOutputTokens value of 100000 but the supported range is from 1 (inclusive) to 65537 (exclusive). Update the value and try again."
					_, _ = fmt.Fprintf(w, `{"error":{"type":"AI_APICallError","message":%q,"param":{"type":"AI_APICallError","message":%q,"statusCode":400,"isRetryable":false}}}`, message, message)
				} else if tc.inputLength {
					_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","code":"invalid_value","param":"messages","message":"system/developer 消息字符数超限：6001，最多 5000"}}`)
				} else {
					_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","code":"unsupported_parameter","param":"temperature","message":"Parameter is unsupported."}}`)
				}
			}))
			defer server.Close()
			stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{server.URL}, Resolver: errorTestResolver{}, RequestTimeout: 3 * time.Second, Concurrency: egress.ConcurrencyLimits{Global: 2, PerEndpoint: 2}})
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
			candidate, model := f.personal.snapshot.Candidates[0], "provider/model"
			if charity {
				candidate, model = f.charity.snapshot.Candidates[0], "[公益]care/model"
			}
			candidate.ConnectorType = kind
			candidate.CanonicalBaseURL = server.URL
			candidate.BindingID = 31
			attempts := 1
			if charity {
				f.charity.snapshot.Candidates = []RouteCandidate{candidate}
				second := candidate
				second.EndpointKeyID++
				second.DonationKeyID++
				second.BindingID++
				f.charity.snapshot.Candidates = append(f.charity.snapshot.Candidates, second)
				f.addDispatch(candidate)
				candidate = second
				attempts = 2
			} else {
				f.personal.snapshot.Candidates = []RouteCandidate{candidate}
			}
			f.addDispatch(candidate)
			if tc.adapted {
				f.service.adaptations = adaptationReaderFunc(func(refs []requestadaptation.Ref) map[requestadaptation.Ref]requestadaptation.Snapshot {
					out := make(map[requestadaptation.Ref]requestadaptation.Snapshot)
					for _, ref := range refs {
						doc := requestadaptation.Empty(ref.Scope)
						if ref.Scope == requestadaptation.ScopeCharityModel {
							doc.BodyForced.Values["/temperature"] = json.RawMessage(`0.5`)
						}
						out[ref] = requestadaptation.Snapshot{Document: doc, Revision: "1"}
					}
					return out
				})
			}
			f.charges.charge = 0
			request := decodeChatForTest(t, fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}],"temperature":0.25,"max_tokens":100000,"stream":%t}`, model, stream))
			writer := httptest.NewRecorder()
			f.service.Chat(context.Background(), writer, 1, request, []byte(`{}`), "application/json", "en")
			if writer.Code != 400 || !strings.Contains(writer.Body.String(), code) && !tc.adapted || len(f.claims.outcomes) != attempts || len(f.claims.requestResults) != 1 {
				t.Fatalf("status=%d body=%s attempts=%d", writer.Code, writer.Body.String(), len(f.claims.outcomes))
			}
			outcome, terminal := f.claims.outcomes[0], f.claims.requestResults[0]
			want := contract.StreakUpstreamFailure
			if charity && (!tc.adapted || tc.inputLength) {
				want = contract.StreakNeutral
			}
			if outcome.StreakDisposition != want || outcome.CallerFault.Matches(400, code) != (charity && (!tc.adapted || tc.inputLength)) || outcome.ProtocolSuccess || outcome.ResponseStarted || outcome.UpstreamStatus != 400 || terminal.Caller.Status != 400 || terminal.Disposition != claim.AccountingCommit || terminal.ActualChargeMilli != 0 {
				t.Fatalf("outcome=%+v terminal=%+v", outcome, terminal)
			}
		})
	}
}

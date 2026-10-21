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
	for _, tc := range []struct{ charity, stream, adapted bool }{{false, false, false}, {false, true, false}, {true, false, false}, {true, true, false}, {true, false, true}} {
		charity, stream := tc.charity, tc.stream
		t.Run(fmt.Sprintf("charity=%t/stream=%t/adapted=%t", charity, stream, tc.adapted), func(t *testing.T) {
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
				_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","code":"unsupported_parameter","param":"temperature","message":"Parameter is unsupported."}}`)
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
			actual, err := f.service.registry.NewConnector(contract.TypeOpenAICompatible, connector.Dependencies{Backend: local})
			if err != nil {
				t.Fatal(err)
			}
			f.service.connectors[contract.TypeOpenAICompatible] = actual
			candidate, model := f.personal.snapshot.Candidates[0], "provider/model"
			if charity {
				candidate, model = f.charity.snapshot.Candidates[0], "[公益]care/model"
			}
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
			request := decodeChatForTest(t, fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}],"temperature":0.25,"stream":%t}`, model, stream))
			writer := httptest.NewRecorder()
			f.service.Chat(context.Background(), writer, 1, request, []byte(`{}`), "application/json", "en")
			if writer.Code != 400 || !strings.Contains(writer.Body.String(), "unsupported_parameter") && !tc.adapted || len(f.claims.outcomes) != attempts || len(f.claims.requestResults) != 1 {
				t.Fatalf("status=%d body=%s attempts=%d", writer.Code, writer.Body.String(), len(f.claims.outcomes))
			}
			outcome, terminal := f.claims.outcomes[0], f.claims.requestResults[0]
			want := contract.StreakUpstreamFailure
			if charity && !tc.adapted {
				want = contract.StreakNeutral
			}
			if outcome.StreakDisposition != want || outcome.CallerFault.Matches(400, "unsupported_parameter") != (charity && !tc.adapted) || outcome.ProtocolSuccess || outcome.ResponseStarted || outcome.UpstreamStatus != 400 || terminal.Caller.Status != 400 || terminal.Disposition != claim.AccountingCommit || terminal.ActualChargeMilli != 0 {
				t.Fatalf("outcome=%+v terminal=%+v", outcome, terminal)
			}
		})
	}
}

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

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
)

func TestContinuationIdentityBindsEveryPhysicalSourceDimension(t *testing.T) {
	factory, err := NewSafetyIdentifierFactory(fixedSubkeyDeriver{})
	if err != nil {
		t.Fatal(err)
	}
	defer factory.Close()
	want, err := factory.ContinuationID(1, 2, 3, "https://source.example/v1", "model", "openai-responses")
	if err != nil || len(want) != 43 {
		t.Fatal(want, err)
	}
	for _, source := range []struct {
		user, key, version    int64
		base, model, protocol string
	}{
		{2, 2, 3, "https://source.example/v1", "model", "openai-responses"},
		{1, 3, 3, "https://source.example/v1", "model", "openai-responses"},
		{1, 2, 4, "https://source.example/v1", "model", "openai-responses"},
		{1, 2, 3, "https://source.example/v2", "model", "openai-responses"},
		{1, 2, 3, "https://source.example/v1", "another-model", "openai-responses"},
		{1, 2, 3, "https://source.example/v1", "model", "openai-compatible"},
	} {
		got, err := factory.ContinuationID(source.user, source.key, source.version, source.base, source.model, source.protocol)
		if err != nil || got == want {
			t.Fatalf("source binding collision: %v", err)
		}
	}
}

func TestContinuationRejectsChangedHistoryBeforeAdmission(t *testing.T) {
	for _, mode := range []string{"stale_version", "mixed", "flatten", "remove", "replace"} {
		t.Run(mode, func(t *testing.T) {
			f := newServiceFixture(t, nil)
			f.personal.snapshot.Candidates = authorizedFixtureCandidates(f.personal.snapshot.Candidates)
			candidate := f.personal.snapshot.Candidates[0]
			token, err := f.safety.ContinuationID(1, candidate.EndpointKeyID, candidate.Authorization.CredentialVersionID, candidate.CanonicalBaseURL, candidate.UpstreamModelID, string(candidate.ConnectorType))
			if err != nil {
				t.Fatal(err)
			}
			message := func(id string) map[string]any {
				return map[string]any{"role": "assistant", "content": "prior answer", "extra_content": map[string]any{"nonbiri": map[string]any{"continuation_id": id}}}
			}
			messages := []any{message(token)}
			switch mode {
			case "stale_version":
				f.personal.snapshot.Candidates[0].Authorization.CredentialVersionID++
			case "mixed":
				messages = append(messages, message(strings.Repeat("A", 43)))
			case "flatten":
				f.personal.preflight.FlattenToolCalls = true
				f.personal.snapshot.FlattenToolCalls = true
			case "remove", "replace":
				// Whole-history overrides are themselves forbidden. They must
				// never bypass source binding or create an accepted request.
				f.service.adaptations = adaptationReaderFunc(func(refs []requestadaptation.Ref) map[requestadaptation.Ref]requestadaptation.Snapshot {
					out := map[requestadaptation.Ref]requestadaptation.Snapshot{}
					for _, ref := range refs {
						doc := requestadaptation.Empty(ref.Scope)
						if ref.Scope == requestadaptation.ScopeEndpoint {
							value := []any{map[string]any{"role": "user", "content": "replacement"}}
							if mode == "replace" {
								value = []any{message(strings.Repeat("A", 43))}
							}
							doc.BodyForced.Values["/messages"], _ = json.Marshal(value)
						}
						out[ref] = requestadaptation.Snapshot{Document: doc, Revision: "1"}
					}
					return out
				})
			}
			raw, _ := json.Marshal(map[string]any{"model": "provider/model", "messages": messages})
			w := httptest.NewRecorder()
			f.service.Chat(t.Context(), w, 1, decodeChatForTest(t, string(raw)), raw, "application/json", "en")
			if w.Code < 400 || len(f.claims.accepts) != 0 || len(f.claims.claims) != 0 || f.openAI.calls != 0 {
				t.Fatalf("bound history was admitted: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestContinuationBindingSurvivesRealResponsesAndForcedTransports(t *testing.T) {
	for _, rule := range []transportpolicy.Rule{transportpolicy.Passthrough, transportpolicy.ForceNonStream, transportpolicy.ForceStream} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", rule, stream), func(t *testing.T) {
				f := newServiceFixture(t, nil)
				backend := &delayedStreamBackend{do: func(r *http.Request) (*http.Response, error) {
					var body struct{ Stream bool }
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if body.Stream {
						return streamResponse(200, responsesTransportStream()), nil
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(transportJSON(contract.TypeOpenAIResponses)))}, nil
				}}
				useRealStreamConnector(t, f, contract.TypeOpenAIResponses, backend, 0, false)
				f.charity.preflight.TransportRule = rule
				f.charity.snapshot.TransportRule = rule
				candidate := authorizedFixtureCandidates(f.charity.snapshot.Candidates)[0]
				token, err := f.safety.ContinuationID(1, candidate.EndpointKeyID, candidate.Authorization.CredentialVersionID, candidate.CanonicalBaseURL, candidate.UpstreamModelID, string(candidate.ConnectorType))
				if err != nil {
					t.Fatal(err)
				}
				raw := fmt.Sprintf(`{"model":"[公益]care/model","stream":%t,"messages":[{"role":"user","content":"hello"}]}`, stream)
				w := httptest.NewRecorder()
				f.service.Chat(context.Background(), w, 1, decodeChatForTest(t, raw), nil, "application/json", "en")
				if w.Code != 200 || !strings.Contains(w.Body.String(), token) || !strings.Contains(w.Body.String(), "opaque") || len(f.claims.outcomes) != 1 || !f.claims.outcomes[0].ProtocolSuccess {
					t.Fatalf("source history lost: %d %s", w.Code, w.Body)
				}
			})
		}
	}
}

package forward

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

func TestGatewayFidelityFiltersBeforeAcceptanceAndDecryption(t *testing.T) {
	for _, embedding := range []bool{false, true} {
		for _, fallback := range []bool{false, true} {
			f := newServiceFixture(t, nil)
			openAI := f.personal.snapshot.Candidates[0]
			gateway := openAI
			gateway.EndpointID = 51
			gateway.EndpointKeyID = 52
			gateway.ConnectorType = contract.TypeAISDKGatewayV3
			f.personal.snapshot.Candidates = []RouteCandidate{gateway}
			if fallback {
				f.personal.snapshot.Candidates = append(f.personal.snapshot.Candidates, openAI)
				f.addDispatch(openAI)
				f.openAI.results = []contract.AttemptResult{{Success: true, Committed: true, Usage: contract.Usage{Present: true}, UpstreamStatus: 200, ClientStatus: 200}}
			}
			w := httptest.NewRecorder()
			if embedding {
				r, err := openai.DecodeEmbeddingRequest(strings.NewReader(`{"model":"provider/model","input":[1,2],"dimensions":3}`), 0)
				if err != nil {
					t.Fatal(err)
				}
				f.service.Embeddings(context.Background(), w, 1, r, nil, "application/json", "en")
			} else {
				r := decodeChatForTest(t, `{"model":"provider/model","messages":[{"role":"developer","content":"x"}]}`)
				f.service.Chat(context.Background(), w, 1, r, nil, "application/json", "en")
			}
			if fallback {
				if len(f.claims.accepts) != 1 || f.claims.accepts[0].AttemptLimit != 1 || len(f.claims.claims) != 1 || f.claims.claims[0].Candidate.ConnectorType != contract.TypeOpenAICompatible {
					t.Fatal("incompatible candidate reached reservation")
				}
			} else if w.Code != 400 || len(f.claims.events) != 0 {
				t.Fatalf("unsupported reserved: %d %+v", w.Code, f.claims.events)
			}
		}
	}
}

package forward

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
)

type gatewayReaderFunc func(context.Context, []gatewaypolicy.Target) (map[gatewaypolicy.Target]gatewaypolicy.Model, error)

func (f gatewayReaderFunc) LoadMany(ctx context.Context, targets []gatewaypolicy.Target) (map[gatewaypolicy.Target]gatewaypolicy.Model, error) {
	return f(ctx, targets)
}

func TestGatewayPolicySnapshotPrecedesAdmissionAndSurvivesRetry(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.personal.snapshot.SilentRetry = true
	f.personal.preflight.SilentRetry = true
	first := f.personal.snapshot.Candidates[0]
	first.ConnectorType = contract.TypeAISDKGatewayV3
	second := first
	second.EndpointKeyID++
	f.personal.snapshot.Candidates = []RouteCandidate{first, second}
	f.addDispatch(first)
	f.addDispatch(second)
	reads := 0
	model := gatewaypolicy.Model{Adapter: gatewaypolicy.AnthropicEffort, MaxOutputTokens: 128000, Cache: gatewaypolicy.AnthropicCache}
	f.service.gatewayModels = gatewayReaderFunc(func(_ context.Context, targets []gatewaypolicy.Target) (map[gatewaypolicy.Target]gatewaypolicy.Model, error) {
		reads++
		if len(f.claims.claims) != 0 || len(targets) != 2 {
			t.Fatalf("snapshot occurred after claim or omitted candidate")
		}
		result := map[gatewaypolicy.Target]gatewaypolicy.Model{}
		for _, target := range targets {
			result[target] = model
		}
		return result, nil
	})
	driver := f.service.connectors[contract.TypeAISDKGatewayV3].(*fakeConnector)
	attempts := 0
	driver.attempt = func(_ context.Context, input connector.AttemptInput) contract.AttemptResult {
		attempts++
		if input.Policy.GatewayModel == nil || input.Policy.GatewayModel.MaxOutputTokens != 128000 || input.Policy.GatewayModel.Cache != gatewaypolicy.AnthropicCache {
			t.Fatal("attempt lost admission snapshot")
		}
		model.MaxOutputTokens = 1
		model.Cache = gatewaypolicy.RejectCache
		if attempts == 1 {
			return contract.AttemptResult{Failure: contract.FailureUpstream, UpstreamStatus: 503}
		}
		return contract.AttemptResult{Success: true, Committed: true, UpstreamStatus: 200, ClientStatus: 200, Usage: contract.Usage{Present: true}}
	}
	req := decodeChatForTest(t, `{"model":"provider/model","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":128000,"cache_control":{"type":"ephemeral"}}`)
	defer req.Clear()
	response := httptest.NewRecorder()
	f.service.Chat(context.Background(), response, 1, req, nil, "application/json", "en")
	if response.Code != http.StatusOK || reads != 1 || attempts != 2 {
		t.Fatalf("status=%d reads=%d attempts=%d body=%s", response.Code, reads, attempts, response.Body.String())
	}
}

package forward

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/headerfacts"
)

func TestAttemptSettlementRetainsResponseCollectorAfterCancellation(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.addDispatch(f.personal.snapshot.Candidates[0])
	f.service.errorScope = headerfacts.WithResponse
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.openAI.attempt = func(ctx context.Context, input connector.AttemptInput) contract.AttemptResult {
		headerfacts.CaptureResponse(ctx, &http.Response{StatusCode: 200, Header: http.Header{"X-Observed": {"before-cancellation"}}})
		cancel()
		return contract.AttemptResult{Failure: contract.FailureCanceled, UpstreamStatus: 200, StreakDisposition: contract.StreakNeutral, FailureOrigin: contract.OriginClientCancel}
	}
	seen := false
	f.claims.attemptHook = func(ctx context.Context, index int) {
		if ctx.Err() != nil {
			t.Fatal("settlement inherited cancellation")
		}
		id := "req_AAAAAAAAAAAAAAAAAAAAAA"
		fact := headerfacts.Response(headerfacts.PrepareResponse(ctx, id, index+1), id, index+1)
		view, err := headerfacts.DecodeCollection(fact.State(), fact.Compressed(), false)
		if err != nil || len(view.Responses) != 1 || view.Responses[0].Headers[0].Value != "before-cancellation" {
			t.Fatal("attempt context lost at settlement", view, err)
		}
		seen = true
	}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[{"role":"user","content":"hello"}]}`)
	f.service.Chat(parent, httptest.NewRecorder(), 1, request, []byte("{}"), "application/json", "en")
	if !seen {
		t.Fatal("attempt not settled")
	}
}

package forward

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
)

func TestPersonalBalancedRoutingSendsCandidateSetToAtomicClaim(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.personal.preflight.RouteStrategy = "cache_balanced"
	fixture.personal.snapshot.RouteStrategy = "cache_balanced"
	next := fixture.personal.snapshot.Candidates[0]
	next.EndpointID = 31
	next.EndpointKeyID = 32
	fixture.personal.snapshot.Candidates = append(fixture.personal.snapshot.Candidates, next)
	fixture.claims.claimErrors = map[int]error{0: claim.ErrKeyRateLimited}
	recorder := httptest.NewRecorder()
	fixture.service.Chat(context.Background(), recorder, 1,
		decodeChatForTest(t, `{"model":"provider/model","messages":[]}`), []byte(`{}`), "application/json", "en")
	if recorder.Code != http.StatusTooManyRequests || len(fixture.claims.claims) != 1 || fixture.openAI.calls != 0 || len(fixture.claims.outcomes) != 0 {
		t.Fatalf("limited balanced call=%d claims=%d upstream=%d", recorder.Code, len(fixture.claims.claims), fixture.openAI.calls)
	}
	input := fixture.claims.claims[0]
	if input.PersonalModelID != fixture.personal.preflight.ModelID || input.Purpose != claim.PurposeSelf ||
		input.Candidate.EndpointKeyID != 0 || len(input.BalancedCandidates) != 2 {
		t.Fatalf("balanced routing lost model association or selected outside claim: %+v", input)
	}
	seen := map[int64]bool{}
	for _, candidate := range input.BalancedCandidates {
		if candidate.DonationKeyID != 0 || candidate.OutputTokenFloor != 0 || seen[candidate.Candidate.EndpointKeyID] {
			t.Fatalf("invalid personal balanced candidate=%+v", candidate)
		}
		seen[candidate.Candidate.EndpointKeyID] = true
	}
	if !seen[fixture.personal.snapshot.Candidates[0].EndpointKeyID] || !seen[next.EndpointKeyID] {
		t.Fatal("candidate permutation lost a connection")
	}
	if len(fixture.claims.requestResults) != 1 || fixture.claims.requestResults[0].Disposition != claim.AccountingRelease {
		t.Fatal("rejected key selection consumed request accounting")
	}
}

func TestPersonalBalancedModelRemovalReturnsNotFoundBeforeDispatch(t *testing.T) {
	fixture := newServiceFixture(t, nil)
	fixture.personal.preflight.RouteStrategy = "cache_balanced"
	fixture.personal.snapshot.RouteStrategy = "cache_balanced"
	fixture.claims.claimErrors = map[int]error{0: claim.ErrModelUnavailable}
	recorder := httptest.NewRecorder()
	fixture.service.Chat(context.Background(), recorder, 1,
		decodeChatForTest(t, `{"model":"provider/model","messages":[]}`), []byte(`{}`), "application/json", "en")
	if recorder.Code != http.StatusNotFound || len(fixture.claims.claims) != 1 || fixture.openAI.calls != 0 || len(fixture.claims.outcomes) != 0 {
		t.Fatalf("removed balanced model=%d claims=%d upstream=%d", recorder.Code, len(fixture.claims.claims), fixture.openAI.calls)
	}
	if len(fixture.claims.requestResults) != 1 || fixture.claims.requestResults[0].Disposition != claim.AccountingRelease {
		t.Fatal("removed model consumed request accounting")
	}
}

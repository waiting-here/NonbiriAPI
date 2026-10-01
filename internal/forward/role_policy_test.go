package forward

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
	"github.com/waiting-here/NonbiriAPI/internal/rolepolicy"
)

func assertAttemptRole(t *testing.T, input connector.AttemptInput, want string) {
	t.Helper()
	raw, ok := input.Ingress.RawField("messages")
	if !ok {
		t.Fatal("messages missing")
	}
	defer clear(raw)
	var messages []struct{ Role string }
	if json.Unmarshal(raw, &messages) != nil || len(messages) != 1 || messages[0].Role != want {
		t.Fatalf("attempt role must be %s", want)
	}
}
func TestRolePolicyLogicalSnapshotSurvivesRetrySettingChange(t *testing.T) {
	f := newServiceFixture(t, nil)
	p := rolepolicy.Default()
	p.Rules["developer"] = "user"
	f.personal.preflight.RolePolicy = p
	f.personal.preflight.SilentRetry = true
	f.personal.snapshot.PersonalPreflight = f.personal.preflight
	next := f.personal.snapshot.Candidates[0]
	next.EndpointKeyID++
	next.EndpointID++
	f.personal.snapshot.Candidates = append(f.personal.snapshot.Candidates, next)
	f.addDispatch(f.personal.snapshot.Candidates[0])
	f.addDispatch(next)
	attempts := 0
	f.openAI.attempt = func(_ context.Context, input connector.AttemptInput) contract.AttemptResult {
		assertAttemptRole(t, input, "user")
		attempts++
		if attempts == 1 {
			f.personal.preflight.RolePolicy.DefaultAction = "reject"
			f.personal.preflight.RolePolicy.Rules = map[string]string{}
			return contract.AttemptResult{Failure: contract.FailureUpstream, UpstreamStatus: 503}
		}
		return contract.AttemptResult{Success: true, Committed: true, UpstreamStatus: 200, Usage: contract.Usage{Present: true}}
	}
	raw := []byte(`{"model":"provider/model","messages":[{"role":"developer","content":"hello"}]}`)
	request := decodeChatForTest(t, string(raw))
	defer request.Clear()
	recorder := httptest.NewRecorder()
	f.service.Chat(context.Background(), recorder, 1, request, raw, "application/json", "en")
	if recorder.Code != http.StatusOK || attempts != 2 {
		t.Fatalf("retry result=%d attempts=%d", recorder.Code, attempts)
	}
	if f.personal.preCalls != 1 {
		t.Fatal("model policy read again on retry")
	}
}
func TestRolePolicyCharityIngressSnapshotSurvivesPreflightChange(t *testing.T) {
	f := newServiceFixture(t, nil)
	p := rolepolicy.Default()
	p.Rules["developer"] = "user"
	f.charity.preflight.RolePolicy = p
	raw := []byte(`{"model":"[公益]care/model","messages":[{"role":"developer","content":"hello"}]}`)
	request, filtered, _, err := f.service.decodeIngress(context.Background(), 1, raw, contract.OperationChatCompletions)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	defer clear(filtered)
	f.charity.preflight.RolePolicy = rolepolicy.Default()
	f.charity.preflight.RolePolicy.DefaultAction = "reject"
	f.charity.preflight.Revision++
	f.charity.snapshot.CharityPreflight = f.charity.preflight
	f.addDispatch(f.charity.snapshot.Candidates[0])
	called := false
	f.openAI.attempt = func(_ context.Context, input connector.AttemptInput) contract.AttemptResult {
		assertAttemptRole(t, input, "user")
		called = true
		return contract.AttemptResult{Success: true, Committed: true, UpstreamStatus: 200, Usage: contract.Usage{Present: true}}
	}
	recorder := httptest.NewRecorder()
	f.service.execute(context.Background(), recorder, 1, request, filtered, "application/json", "en", nil)
	if recorder.Code != http.StatusOK || !called {
		t.Fatalf("snapshot response=%d called=%v", recorder.Code, called)
	}
}

func TestRolePolicyPassthroughSurvivesBodyAdaptation(t *testing.T) {
	source := decodeChatForTest(t, "{\"model\":\"provider/model\",\"messages\":[{\"role\":\"developer\",\"content\":\"keep\"}]}")
	policy := rolepolicy.Default()
	policy.Rules["developer"] = "passthrough"
	transformed, err := source.ApplyRolePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	defer transformed.Clear()
	document := requestadaptation.Empty(requestadaptation.ScopeEndpoint)
	document.BodyForced.Values["/temperature"] = json.RawMessage("0.2")
	prepared, err := prepareAdaptedAttempt(chatRequest(transformed), contract.TypeAnthropicCompatible, document, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.clear()
	if prepared.request.chat.SupportsRolePassthrough(string(contract.TypeAnthropicCompatible)) || !prepared.request.chat.SupportsRolePassthrough(string(contract.TypeOpenAICompatible)) {
		t.Fatal("adaptation lost logical passthrough fidelity")
	}
	assertAttemptRole(t, connector.AttemptInput{Ingress: prepared.request.chat}, "developer")
}

func TestDefinitiveAttemptOutcomeSurvivesLateCancellation(t *testing.T) {
	for _, success := range []bool{false, true} {
		f := newServiceFixture(t, nil)
		f.addDispatch(f.charity.snapshot.Candidates[0])
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.openAI.attempt = func(context.Context, connector.AttemptInput) contract.AttemptResult {
			cancel()
			result := contract.AttemptResult{Failure: contract.FailureUpstream, UpstreamStatus: 503}
			if success {
				result.Failure = contract.FailureSink
				result.UpstreamStatus = 200
				return contract.ProtocolSucceeded(result)
			}
			return contract.UpstreamFailed(result, contract.OriginUpstreamResponse)
		}
		raw := "{\"model\":\"[公益]care/model\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}"
		f.service.Chat(ctx, httptest.NewRecorder(), 1, decodeChatForTest(t, raw), []byte(raw), "application/json", "en")
		if len(f.claims.outcomes) != 1 {
			t.Fatal("attempt not settled")
		}
		outcome := f.claims.outcomes[0]
		if success {
			if outcome.StreakDisposition != contract.StreakSuccess || outcome.FailureOrigin != contract.OriginNone || !outcome.ProtocolSuccess {
				t.Fatalf("confirmed success lost: %+v", outcome)
			}
		} else if outcome.StreakDisposition != contract.StreakUpstreamFailure || outcome.FailureOrigin != contract.OriginUpstreamResponse || outcome.UpstreamStatus != 503 {
			t.Fatalf("confirmed error lost: %+v", outcome)
		}
	}
}

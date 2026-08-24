package forward

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
)

func policyStoreValue(t *testing.T, body []byte) bool {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("decode upstream request: %v; body=%s", err, body)
	}
	var value bool
	if err := json.Unmarshal(root["store"], &value); err != nil {
		t.Fatalf("decode store: %v; body=%s", err, body)
	}
	return value
}

func TestForwardRechecksStorePolicyPerFinalKeyAcrossFailover(t *testing.T) {
	bodies := make(chan []byte, 2)
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		bodies <- append([]byte(nil), body...)
		if hits.Add(1) == 1 {
			http.Error(writer, "first candidate failed", http.StatusBadGateway)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, forwardCompletion("second candidate"))
	}))
	defer upstream.Close()

	fixture := newForwardFixture(t, []string{upstream.URL}, Hooks{}, nil, nil)
	user := fixture.addUser(t, "policy-final-key")
	first := fixture.addRouteCfg(t, user.id, upstream.URL, "provider", "model", "up/first", "sk-policy-first", 0, "ordered", true)
	secondBinding := fixture.addBindingToModel(t, user.id, first.modelID, upstream.URL, "sk-policy-second", "up/second", 1)
	var secondKeyID int64
	if err := fixture.store.DB().QueryRow(`SELECT endpoint_key_id FROM model_bindings WHERE id=?`, secondBinding).Scan(&secondKeyID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`UPDATE endpoint_keys SET force_store_false=1 WHERE id=?`, first.keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`UPDATE endpoint_keys SET force_store_false=0 WHERE id=?`, secondKeyID); err != nil {
		t.Fatal(err)
	}

	request := decodedForwardRequest(t, `{"model":"provider/model","messages":[{"role":"user","content":"hi"}],"store":true}`)
	result, err := fixture.service.Forward(context.Background(), httptest.NewRecorder(), user.id, request)
	if err != nil || !result.Success {
		t.Fatalf("forward result=%+v err=%v", result, err)
	}
	firstBody, secondBody := <-bodies, <-bodies
	if got := policyStoreValue(t, firstBody); got {
		t.Fatalf("first final key policy did not force false: %s", firstBody)
	}
	if got := policyStoreValue(t, secondBody); !got {
		t.Fatalf("second final key inherited first key policy: %s", secondBody)
	}
}

func TestForwardCachesStorePolicyForSameKeyAcrossSilentRetry(t *testing.T) {
	bodies := make(chan []byte, 2)
	var hits atomic.Int32
	var keyID int64
	var store *sql.DB
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		bodies <- append([]byte(nil), body...)
		if hits.Add(1) == 1 {
			// Change the authoritative row after the first final-key read. The
			// second binding uses the same physical key; request-local policy
			// caching must keep the first value for this logical request.
			if store == nil {
				t.Errorf("test store was not initialized")
			} else if _, err := store.Exec(`UPDATE endpoint_keys SET force_store_false=1 WHERE id=?`, keyID); err != nil {
				t.Errorf("flip key policy: %v", err)
			}
			http.Error(writer, "first candidate failed", http.StatusBadGateway)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, forwardCompletion("same key retry"))
	}))
	defer upstream.Close()

	fixture := newForwardFixture(t, []string{upstream.URL}, Hooks{}, nil, nil)
	store = fixture.store.DB()
	user := fixture.addUser(t, "policy-same-key")
	first := fixture.addRouteCfg(t, user.id, upstream.URL, "provider", "same-key", "up/first", "sk-policy-same", 0, "ordered", true)
	keyID = first.keyID
	if _, err := fixture.store.DB().Exec(`INSERT INTO fetched_models (endpoint_key_id, upstream_model_id, provider, fetched_at, status) VALUES (?, ?, ?, ?, 'ok')`, first.keyID, "up/second", "upstream", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CreateBinding(context.Background(), user.id, first.modelID, first.keyID, "up/second", 1, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}

	request := decodedForwardRequest(t, `{"model":"provider/same-key","messages":[{"role":"user","content":"hi"}],"store":true}`)
	result, err := fixture.service.Forward(context.Background(), httptest.NewRecorder(), user.id, request)
	if err != nil || !result.Success {
		t.Fatalf("forward result=%+v err=%v", result, err)
	}
	firstBody, secondBody := <-bodies, <-bodies
	if got := policyStoreValue(t, firstBody); !got {
		t.Fatalf("first same-key request did not preserve the initial false policy: %s", firstBody)
	}
	if got := policyStoreValue(t, secondBody); !got {
		t.Fatalf("same-key retry reread a changed policy instead of request-local value: %s", secondBody)
	}
}

func TestForwardFreezesFlattenPolicyAcrossSilentRetry(t *testing.T) {
	route := capabilityRoute(endpoint.ConnectorOpenAICompatible, endpoint.ConnectorOpenAICompatible)
	route.SilentRetry = true
	route.FlattenToolCalls = true
	var observed atomic.Int32
	service, err := NewService(ServiceConfig{
		Repository: fixedRouteRepository{route: route},
		Runner: attemptRunnerFunc(func(_ context.Context, _ http.ResponseWriter, input AttemptInput) contract.AttemptResult {
			if !input.FlattenToolCalls {
				t.Errorf("attempt %d lost frozen flatten policy", input.AttemptIndex)
			}
			if observed.Add(1) == 1 {
				return contract.AttemptResult{Failure: contract.FailureUpstream}
			}
			return contract.AttemptResult{Success: true}
		}),
		Backoff: BackoffConfig{Base: -1, Max: -1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	request := decodedForwardRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}]}`)
	result, err := service.Forward(context.Background(), httptest.NewRecorder(), 42, request)
	if err != nil || !result.Success || observed.Load() != 2 {
		t.Fatalf("flatten retry result=%+v err=%v attempts=%d", result, err, observed.Load())
	}
}

type policyCharityTargetRepository struct{ target db.CharityForwardTarget }

func (r policyCharityTargetRepository) GetCharityForwardTarget(context.Context, int64, string, int64) (db.CharityForwardTarget, error) {
	return r.target, nil
}

func TestSecureRunnerRejectsOpenAIOnlyPoliciesBeforePersonalOrCharityDecrypt(t *testing.T) {
	key := randomSafetyIdentifierKey(t)
	factory, err := NewSafetyIdentifierFactory(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	defer factory.Close()
	codec := &dispatchSafetyCodec{}
	adapter := &dispatchSafetyAdapter{}
	target := db.ForwardTarget{
		BindingID: 1, EndpointID: 2, EndpointKeyID: 3,
		ConnectorType: string(endpoint.ConnectorAnthropicCompatible), BaseURL: testSafetyOrigin,
		UpstreamModelID: "up/model", ForceStoreFalse: true,
	}
	for _, test := range []struct {
		name    string
		flatten bool
		charity bool
	}{
		{name: "personal store", charity: false},
		{name: "charity store", charity: true},
		{name: "personal flatten", flatten: true, charity: false},
		{name: "charity flatten", flatten: true, charity: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			caseTarget := target
			if test.flatten {
				// Keep flatten-only cases independent of the physical key
				// policy, proving both OpenAI-only guards separately.
				caseTarget.ForceStoreFalse = false
			}
			runner, err := NewSecureRunner(SecureRunnerConfig{
				Repository:        dispatchSafetyRepo{target: caseTarget},
				CharityTargets:    policyCharityTargetRepository{target: db.CharityForwardTarget{ForwardTarget: caseTarget, BindingID: 1, DonorUserID: 99}},
				Secrets:           codec,
				Registry:          endpoint.NewRegistry(),
				Adapters:          []Adapter{adapter},
				Backend:           unavailableSafetyBackend{},
				SafetyIdentifiers: factory,
			})
			if err != nil {
				t.Fatal(err)
			}
			before := codec.opens.Load()
			var result contract.AttemptResult
			if test.charity {
				result = runner.RunCharity(context.Background(), httptest.NewRecorder(), CharityAttemptInput{
					BindingID: 1, FullName: "[公益]provider/model", ExpectedConnectorType: contract.TypeAnthropicCompatible,
					Now: time.Now().Unix(), ConsumerUserID: 42, Request: &openai.ChatRequest{Model: "[公益]provider/model"},
					FlattenToolCalls: test.flatten,
				})
			} else {
				result = runner.Run(context.Background(), httptest.NewRecorder(), AttemptInput{
					UserID: 42, FullName: "provider/model", BindingID: 1, ExpectedConnectorType: contract.TypeAnthropicCompatible,
					Request: &openai.ChatRequest{Model: "provider/model"}, FlattenToolCalls: test.flatten,
				})
			}
			if result.Failure != contract.FailureInternal || codec.opens.Load() != before || adapter.calls.Load() != 0 {
				t.Fatalf("result=%+v decryptions_before=%d after=%d adapter_calls=%d", result, before, codec.opens.Load(), adapter.calls.Load())
			}
		})
	}
}

package forward

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
)

func decodedForwardRequest(t *testing.T, body string) *openai.ChatRequest {
	t.Helper()
	request, err := openai.DecodeChatRequest(strings.NewReader(body), openai.MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(request.Clear)
	return request
}

func capabilityRoute(connectorTypes ...endpoint.ConnectorType) db.ForwardRoute {
	candidates := make([]db.ForwardCandidate, 0, len(connectorTypes))
	for index, connectorType := range connectorTypes {
		id := int64(index + 1)
		candidates = append(candidates, db.ForwardCandidate{
			BindingID: id, ModelID: 1, EndpointID: id, EndpointKeyID: id,
			ConnectorType: string(connectorType), UpstreamModelID: "upstream/model", Ord: id,
		})
	}
	return db.ForwardRoute{ModelID: 1, UserID: 42, FullName: "p/m", RouteStrategy: "ordered", Candidates: candidates}
}

type connectorMutationRouteRepository struct {
	*db.Store
	endpointID int64
}

func (r connectorMutationRouteRepository) ResolveForwardRoute(ctx context.Context, userID int64, fullName string, limit int) (db.ForwardRoute, error) {
	route, err := r.Store.ResolveForwardRoute(ctx, userID, fullName, limit)
	if err != nil {
		return db.ForwardRoute{}, err
	}
	// Deterministically place a concurrent endpoint edit after the capability
	// snapshot and before SecureRunner's final target query.
	mutated := make(chan error, 1)
	go func() {
		_, updateErr := r.Store.DB().ExecContext(context.Background(), `UPDATE endpoints SET connector_type=? WHERE id=?`,
			string(endpoint.ConnectorAnthropicCompatible), r.endpointID)
		mutated <- updateErr
	}()
	if err := <-mutated; err != nil {
		return db.ForwardRoute{}, err
	}
	return route, nil
}

func TestConnectorTypeChangeAfterCapabilityFilterRetriesWithoutDecryptOrEgress(t *testing.T) {
	var upstreamHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		upstreamHits.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, forwardCompletion("second candidate"))
	}))
	defer upstream.Close()

	fixture := newForwardFixture(t, []string{upstream.URL}, Hooks{}, nil, nil)
	user := fixture.addUser(t, "connector-toctou")
	first := fixture.addRoute(t, user.id, upstream.URL, "provider", "model", "up/first", "sk-first", 0)
	ctx := context.Background()
	canonicalOrigin, err := fixture.stack.ValidateBaseURL(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	secondEndpoint, err := fixture.store.CreateEndpoint(ctx, user.id, string(endpoint.ConnectorOpenAICompatible), canonicalOrigin, "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	secondKey, err := fixture.store.CreateEndpointKey(ctx, user.id, secondEndpoint.ID, []byte("sk-second"), "", "", "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.ReplaceFetchedModels(ctx, user.id, secondEndpoint.ID, secondKey.ID, []db.FetchedModel{{
		EndpointKeyID: secondKey.ID, UpstreamModelID: "up/second", Provider: "upstream", Status: "ok",
	}}, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CreateBinding(ctx, user.id, first.modelID, secondKey.ID, "up/second", 1, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`UPDATE models SET silent_retry=1 WHERE id=?`, first.modelID); err != nil {
		t.Fatal(err)
	}

	service, err := NewService(ServiceConfig{
		Repository: connectorMutationRouteRepository{Store: fixture.store, endpointID: first.endpoint},
		Runner:     fixture.runner,
		Backoff:    BackoffConfig{Base: -1, Max: -1},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	fixture.codec.opens.Store(0)
	request := decodedForwardRequest(t, `{"model":"provider/model","messages":[{"role":"user","content":"hi"}]}`)
	recorder := httptest.NewRecorder()
	result, err := service.Forward(ctx, recorder, user.id, request)
	if err != nil || !result.Success || !result.Committed {
		t.Fatalf("result=%+v err=%v body=%s", result, err, recorder.Body.String())
	}
	if got := fixture.codec.opens.Load(); got != 1 {
		t.Fatalf("Vault opens=%d, want only the unchanged second candidate", got)
	}
	if got := upstreamHits.Load(); got != 1 {
		t.Fatalf("upstream hits=%d, want only the unchanged second candidate", got)
	}
}

func TestCapabilityFilterPrecedesSelectorAndRunner(t *testing.T) {
	repository := fixedRouteRepository{route: capabilityRoute(endpoint.ConnectorOpenAICompatible, endpoint.ConnectorAnthropicCompatible)}
	var selectorCalls atomic.Int32
	var runnerCalls atomic.Int32
	selector := selectorFunc(func(_ context.Context, selection Selection) ([]int64, error) {
		selectorCalls.Add(1)
		if len(selection.Candidates) != 1 || selection.Candidates[0].BindingID != 2 || selection.Candidates[0].ConnectorType != string(endpoint.ConnectorAnthropicCompatible) {
			t.Fatalf("selector candidates=%+v", selection.Candidates)
		}
		return []int64{2}, nil
	})
	runner := attemptRunnerFunc(func(_ context.Context, _ http.ResponseWriter, input AttemptInput) openai.AttemptResult {
		runnerCalls.Add(1)
		if input.BindingID != 2 || input.ExpectedConnectorType != connectorcontract.TypeAnthropicCompatible {
			t.Fatalf("runner binding=%d expected_connector=%q", input.BindingID, input.ExpectedConnectorType)
		}
		return openai.AttemptResult{Success: true}
	})
	service, err := NewService(ServiceConfig{Repository: repository, Selector: selector, Runner: runner, Backoff: BackoffConfig{Base: -1, Max: -1}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	request := decodedForwardRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}],"stream":null}`)
	result, err := service.Forward(context.Background(), httptest.NewRecorder(), 42, request)
	if err != nil || !result.Success || selectorCalls.Load() != 1 || runnerCalls.Load() != 1 {
		t.Fatalf("result=%+v err=%v selector=%d runner=%d", result, err, selectorCalls.Load(), runnerCalls.Load())
	}
}

func TestCapabilityFilterEvaluatesEachConnectorTypeOnce(t *testing.T) {
	connectorTypes := make([]endpoint.ConnectorType, MaxRouteCandidates)
	for index := range connectorTypes {
		connectorTypes[index] = endpoint.ConnectorAnthropicCompatible
	}
	var predicateCalls atomic.Int32
	registry, err := connector.NewRegistry(connector.Descriptor{
		Type:         connectorcontract.TypeAnthropicCompatible,
		Capabilities: connectorcontract.CapabilitySet(connectorcontract.CapabilityText),
		New:          func(connector.Dependencies) connector.Connector { return nil },
		Supports: func(*openai.ChatRequest) bool {
			predicateCalls.Add(1)
			return true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	route := capabilityRoute(connectorTypes...)
	route.SilentRetry = true
	var selectorCalls atomic.Int32
	var runnerCalls atomic.Int32
	service, err := NewService(ServiceConfig{
		Repository: fixedRouteRepository{route: route},
		Registry:   registry,
		Selector: selectorFunc(func(_ context.Context, selection Selection) ([]int64, error) {
			selectorCalls.Add(1)
			if len(selection.Candidates) != MaxRouteCandidates {
				t.Fatalf("candidates=%d", len(selection.Candidates))
			}
			return []int64{selection.Candidates[0].BindingID, selection.Candidates[1].BindingID, selection.Candidates[2].BindingID}, nil
		}),
		Runner: attemptRunnerFunc(func(context.Context, http.ResponseWriter, AttemptInput) connectorcontract.AttemptResult {
			if runnerCalls.Add(1) < 3 {
				return connectorcontract.AttemptResult{Failure: connectorcontract.FailureUpstream}
			}
			return connectorcontract.AttemptResult{Success: true}
		}),
		Backoff: BackoffConfig{Base: -1, Max: -1},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	request := decodedForwardRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}]}`)
	result, err := service.Forward(context.Background(), httptest.NewRecorder(), 42, request)
	if err != nil || !result.Success || predicateCalls.Load() != 1 || selectorCalls.Load() != 1 || runnerCalls.Load() != 3 {
		t.Fatalf("result=%+v err=%v predicate=%d selector=%d runner=%d", result, err, predicateCalls.Load(), selectorCalls.Load(), runnerCalls.Load())
	}
}

func TestAllOpenAIRejectionReusesCachedPredicateResult(t *testing.T) {
	var predicateCalls atomic.Int32
	registry, err := connector.NewRegistry(connector.Descriptor{
		Type:         connectorcontract.TypeOpenAICompatible,
		Capabilities: connectorcontract.CapabilitySet(connectorcontract.CapabilityText | connectorcontract.CapabilityUnknownOpenAIFields),
		New:          func(connector.Dependencies) connector.Connector { return nil },
		Supports: func(*openai.ChatRequest) bool {
			predicateCalls.Add(1)
			return false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceConfig{
		Repository: fixedRouteRepository{route: capabilityRoute(endpoint.ConnectorOpenAICompatible, endpoint.ConnectorOpenAICompatible)},
		Registry:   registry,
		Runner: attemptRunnerFunc(func(context.Context, http.ResponseWriter, AttemptInput) connectorcontract.AttemptResult {
			t.Fatal("runner called")
			return connectorcontract.AttemptResult{}
		}),
		Backoff: BackoffConfig{Base: -1, Max: -1},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	request := decodedForwardRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}],"stream":null}`)
	_, gotErr := service.Forward(context.Background(), httptest.NewRecorder(), 42, request)
	if !errors.Is(gotErr, openai.ErrInvalidRequest) || predicateCalls.Load() != 1 {
		t.Fatalf("err=%v predicate=%d", gotErr, predicateCalls.Load())
	}
}

func TestCapabilityRejectionHasNoSelectorOrRunnerSideEffects(t *testing.T) {
	tests := []struct {
		name      string
		route     db.ForwardRoute
		body      string
		wantError error
	}{
		{
			name:      "Anthropic unsupported field",
			route:     capabilityRoute(endpoint.ConnectorAnthropicCompatible),
			body:      `{"model":"p/m","messages":[{"role":"user","content":"hi"}],"n":1}`,
			wantError: ErrUnsupportedCapabilities,
		},
		{
			name:      "legacy OpenAI stream null",
			route:     capabilityRoute(endpoint.ConnectorOpenAICompatible),
			body:      `{"model":"p/m","messages":[{"role":"user","content":"hi"}],"stream":null}`,
			wantError: openai.ErrInvalidRequest,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var selectorCalls atomic.Int32
			var runnerCalls atomic.Int32
			service, err := NewService(ServiceConfig{
				Repository: fixedRouteRepository{route: test.route},
				Selector: selectorFunc(func(context.Context, Selection) ([]int64, error) {
					selectorCalls.Add(1)
					return nil, nil
				}),
				Runner: attemptRunnerFunc(func(context.Context, http.ResponseWriter, AttemptInput) connectorcontract.AttemptResult {
					runnerCalls.Add(1)
					return connectorcontract.AttemptResult{}
				}),
				Backoff: BackoffConfig{Base: -1, Max: -1},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			request := decodedForwardRequest(t, test.body)
			result, gotErr := service.Forward(context.Background(), httptest.NewRecorder(), 42, request)
			if !errors.Is(gotErr, test.wantError) || result != (connectorcontract.AttemptResult{}) || selectorCalls.Load() != 0 || runnerCalls.Load() != 0 {
				t.Fatalf("result=%+v err=%v selector=%d runner=%d", result, gotErr, selectorCalls.Load(), runnerCalls.Load())
			}
		})
	}
}

func TestUnknownCandidateConnectorFailsClosedBeforeSelection(t *testing.T) {
	route := capabilityRoute(endpoint.ConnectorOpenAICompatible)
	route.Candidates[0].ConnectorType = "unknown-compatible"
	var calls atomic.Int32
	service, err := NewService(ServiceConfig{
		Repository: fixedRouteRepository{route: route},
		Selector: selectorFunc(func(context.Context, Selection) ([]int64, error) {
			calls.Add(1)
			return nil, nil
		}),
		Runner: attemptRunnerFunc(func(context.Context, http.ResponseWriter, AttemptInput) connectorcontract.AttemptResult {
			calls.Add(1)
			return connectorcontract.AttemptResult{}
		}),
		Backoff: BackoffConfig{Base: -1, Max: -1},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	request := decodedForwardRequest(t, `{"model":"p/m","messages":[{"role":"user","content":"hi"}]}`)
	_, gotErr := service.Forward(context.Background(), httptest.NewRecorder(), 42, request)
	if !errors.Is(gotErr, ErrInternal) || calls.Load() != 0 {
		t.Fatalf("err=%v calls=%d", gotErr, calls.Load())
	}
}

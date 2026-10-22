package forward

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/lifecyclegate"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
	"github.com/waiting-here/NonbiriAPI/internal/requestkind"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
)

func (router *fakeCharityRouter) PreflightInference(ctx context.Context, userID int64, model string, _ *openai.InferenceRequest, now int64) (CharityPreflight, error) {
	return router.Preflight(ctx, userID, model, nil, now)
}

func TestInferenceModelDetailUsesClosedLogPath(t *testing.T) {
	lifecycle, err := lifecyclegate.New(lifecyclegate.Config{MaxUsers: 4})
	if err != nil {
		t.Fatal(err)
	}
	var facts []requestattempt.Fact
	middleware, err := NewCallerKeyMiddleware(&fakeCallerKeyResolver{identity: resources.CallerIdentity{UserID: 1}}, lifecycle, func(ctx context.Context, user int64, fact requestattempt.Fact) error {
		if requestattempt.Kind(ctx) != "discovery" || user != 1 {
			t.Error("model lookup classification changed")
		}
		facts = append(facts, fact)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://gateway.example/v1/models/provider%2Fmissing", nil)
	r.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeFailure(w, platformFailure(httperr.CodeNotFound, "model not found"))
	})).ServeHTTP(w, r)
	if w.Code != http.StatusNotFound || len(facts) != 1 || facts[0].Path != "/v1/models" {
		t.Fatalf("unbounded log path: status=%d facts=%+v", w.Code, facts)
	}
}

func TestInferenceOriginalAuditPrecedesResourceRefusal(t *testing.T) {
	for _, charity := range []bool{false, true} {
		for _, control := range []string{"store", "background"} {
			f := newServiceFixture(t, nil)
			guard := &originalDecisionGuard{t: t}
			f.service.clientGuard = guard
			f.charity.policyFields = []string{control}
			model := "provider/model"
			if charity {
				model = "[公益]care/model"
			}
			body := `{"model":"` + model + `","input":"hi","` + control + `":true}`
			r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
			r = r.WithContext(observability.WithSource(r.Context(), observability.Source{UserAgent: "original-client"}))
			r = withCallerIdentity(r, resources.CallerIdentity{UserID: 1})
			w := httptest.NewRecorder()
			NewHandler(f.service).ServeHTTP(w, r)
			if w.Code != http.StatusForbidden || guard.calls != 1 || len(f.claims.accepts) != 0 || f.openAI.calls != 0 {
				t.Fatalf("charity=%t control=%s status=%d guards=%d claims=%v", charity, control, w.Code, guard.calls, f.claims.events)
			}
		}
	}
	f := newServiceFixture(t, nil)
	guard := &originalDecisionGuard{t: t}
	f.service.clientGuard = guard
	f.personal.preErr = routing.ErrNotFound
	w := policyIngress(f, "/v1/responses", `{"model":"provider/model","input":"hi","store":true}`, 1)
	if w.Code != http.StatusNotFound || guard.calls != 0 {
		t.Fatal("resource refusal bypassed ownership")
	}
}

func TestInferenceSelectsPersonalAndCharityOperations(t *testing.T) {
	for _, operation := range []contract.Operation{contract.OperationResponses, contract.OperationResponsesCompact, contract.OperationResponsesInputTokens, contract.OperationCompletions, contract.OperationImagesEdits, contract.OperationAudioSpeech, contract.OperationModerations, contract.OperationRerank} {
		for _, charity := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/charity=%t", operation, charity), func(t *testing.T) {
				f := newServiceFixture(t, nil)
				types := modeltype.Set{operation}
				f.personal.preflight.ModelTypes = types
				f.personal.snapshot.ModelTypes = types
				f.charity.preflight.ModelTypes = types
				f.charity.snapshot.ModelTypes = types
				f.personal.preflight.TransportRule = transportpolicy.ForceStream
				f.charity.preflight.TransportRule = transportpolicy.ForceStream
				model := "provider/model"
				candidate := f.personal.snapshot.Candidates[0]
				if charity {
					model = "[公益]care/model"
					candidate = f.charity.snapshot.Candidates[0]
				}
				services, err := contract.ParseNativeServices([]contract.NativeService{contract.NativeService(operation)})
				if err != nil {
					t.Fatal(err)
				}
				candidate.NativeServices = services
				candidate = authorizedFixtureCandidates([]RouteCandidate{candidate})[0]
				if operation == contract.OperationRerank {
					candidate.Authorization.RerankFormat = contract.RerankFormatGeneric
				}
				if charity {
					f.charity.snapshot.Candidates = []RouteCandidate{candidate}
				} else {
					f.personal.snapshot.Candidates = []RouteCandidate{candidate}
				}
				routes, err := connector.ResolveServiceRoutes(candidate.ConnectorType, services, candidate.Authorization.RerankFormat, operation)
				if err != nil || len(routes) != 1 || !routes[0].NativeMatch {
					t.Fatalf("native fixture route: %+v %v", routes, err)
				}
				protocol := f.service.connectors[routes[0].WireType].(*fakeConnector)
				body := `{"model":"` + model + `","input":"hi"}`
				switch operation {
				case contract.OperationCompletions:
					body = `{"model":"` + model + `","prompt":"hi"}`
				case contract.OperationImagesEdits:
					body = `{"model":"` + model + `","prompt":"edit","images":[{"image_url":"data:image/png;base64,aW1hZ2U="}]}`
				case contract.OperationAudioSpeech:
					body = `{"model":"` + model + `","input":"hi","voice":"alloy"}`
				case contract.OperationRerank:
					body = `{"model":"` + model + `","query":"hi","documents":["d"]}`
				}
				request, err := openai.DecodeInferenceRequest(strings.NewReader(body), operation, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer request.Clear()
				dispatch := candidate
				dispatch.ConnectorType = routes[0].WireType
				f.addDispatch(dispatch)
				var captured *openai.InferenceRequest
				protocol.attempt = func(_ context.Context, input connector.AttemptInput) contract.AttemptResult {
					if input.Inference == nil || input.Operation != operation || input.Ingress != nil || input.Embedding != nil || input.Image != nil || input.Decision != nil || input.Policy.FlattenToolCalls || input.Policy.ForceStoreFalse {
						t.Fatal("operation snapshot mixed with chat")
					}
					captured = input.Inference
					_, _ = input.Sink.Write([]byte(`{"ok":true}`))
					return contract.AttemptResult{Success: true, Committed: true, Failure: contract.FailureNone, UpstreamStatus: 200, ClientStatus: 200}
				}
				writer := httptest.NewRecorder()
				f.service.Inference(context.Background(), writer, 1, request, []byte(body), "application/json", "en")
				if writer.Code != 200 || protocol.calls != 1 || len(f.claims.accepts) != 1 || f.claims.accepts[0].Route != requestkind.ForOperation(operation, charity) || captured.Model != "" || request.Model != model {
					t.Fatalf("status=%d calls=%d claims=%v body=%s", writer.Code, protocol.calls, f.claims.events, writer.Body.String())
				}
			})
		}
	}
}

func TestInferenceRejectsUnsupportedConnectorBeforeClaim(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.personal.preflight.ModelTypes = modeltype.Set{contract.OperationResponses}
	f.personal.snapshot.ModelTypes = f.personal.preflight.ModelTypes
	f.personal.snapshot.Candidates[0].ConnectorType = contract.TypeAnthropicCompatible
	r, err := openai.DecodeInferenceRequest(strings.NewReader(`{"model":"provider/model","input":"hi"}`), contract.OperationResponses, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Clear()
	w := httptest.NewRecorder()
	f.service.Inference(context.Background(), w, 1, r, nil, "application/json", "en")
	if w.Code != 400 || len(f.claims.accepts) != 0 || f.anthropic.calls != 0 {
		t.Fatalf("unsupported dispatched: %d %v", w.Code, f.claims.events)
	}
}

func TestInferenceHTTPMultipartAndResourceRejection(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.personal.preflight.ModelTypes = modeltype.Set{contract.OperationAudioTranscriptions}
	f.personal.snapshot.ModelTypes = f.personal.preflight.ModelTypes
	services, err := contract.ParseNativeServices([]contract.NativeService{contract.NativeServiceAudioTranscriptions})
	if err != nil {
		t.Fatal(err)
	}
	f.personal.snapshot.Candidates[0].NativeServices = services
	f.addDispatch(f.personal.snapshot.Candidates[0])
	f.openAI.attempt = func(_ context.Context, input connector.AttemptInput) contract.AttemptResult {
		_, _ = input.Sink.Write([]byte(`{"text":"hello"}`))
		return contract.AttemptResult{Success: true, Committed: true, Failure: contract.FailureNone, UpstreamStatus: 200, ClientStatus: 200}
	}
	var body bytes.Buffer
	upload := multipart.NewWriter(&body)
	_ = upload.WriteField("model", "provider/model")
	file, _ := upload.CreateFormFile("file", "speech.wav")
	_, _ = file.Write([]byte{1, 2, 3})
	_ = upload.Close()
	r := withCallerIdentity(httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &body), resources.CallerIdentity{UserID: 1, Generation: 1})
	r.Header.Set("Content-Type", upload.FormDataContentType())
	w := httptest.NewRecorder()
	NewHandler(f.service).ServeHTTP(w, r)
	if w.Code != 200 || f.openAI.calls != 1 {
		t.Fatalf("multipart rejected: %d %s", w.Code, w.Body.String())
	}
	f = newServiceFixture(t, nil)
	f.personal.preflight.ModelTypes = modeltype.Set{contract.OperationResponses}
	r = withCallerIdentity(httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"provider/model","input":"hi","previous_response_id":"r1"}`)), resources.CallerIdentity{UserID: 1, Generation: 1})
	w = httptest.NewRecorder()
	NewHandler(f.service).ServeHTTP(w, r)
	if w.Code != 400 || len(f.claims.accepts) != 0 || f.openAI.calls != 0 {
		t.Fatal("resource reference reached dispatch")
	}
}

func TestInferenceSingleModelUsesAccessibleProjection(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.personal.models = []ListedModel{{ModelID: 7, Provider: "provider", FullName: "provider/model", CreatedAt: 1}}
	for _, test := range []struct {
		path   string
		status int
	}{{"/v1/models/provider%2Fmodel", 200}, {"/v1/models/other%2Fmodel", 404}, {"/v1/%6dodels/provider%2Fmodel", 404}} {
		r := withCallerIdentity(httptest.NewRequest(http.MethodGet, test.path, nil), resources.CallerIdentity{UserID: 1, Generation: 1})
		w := httptest.NewRecorder()
		NewHandler(f.service).ServeHTTP(w, r)
		if w.Code != test.status || test.status == 200 && !strings.Contains(w.Body.String(), `"id":"provider/model"`) {
			t.Fatalf("path=%s status=%d body=%s", test.path, w.Code, w.Body.String())
		}
	}
}

func TestInferenceCORSUsesClosedPaths(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/responses/compact", "/v1/responses/input_tokens", "/v1/completions", "/v1/images/edits", "/v1/images/variations", "/v1/audio/speech", "/v1/audio/transcriptions", "/v1/audio/translations", "/v1/moderations", "/v1/rerank", "/v1/systemone"} {
		r := httptest.NewRequest(http.MethodOptions, path, nil)
		r.Header.Set("Origin", "https://client.example")
		r.Header.Set("Access-Control-Request-Method", http.MethodPost)
		r.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
		w := httptest.NewRecorder()
		BrowserCORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("preflight reached handler") })).ServeHTTP(w, r)
		if w.Code != 204 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}

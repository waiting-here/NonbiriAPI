package forward

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
)

func TestImageRoutesRespectModelTypesBeforeDispatch(t *testing.T) {
	for _, charity := range []bool{false, true} {
		f := newServiceFixture(t, nil)
		model := "provider/model"
		candidate := f.personal.snapshot.Candidates[0]
		f.personal.preflight.ModelTypes = modeltype.Default()
		if charity {
			model = "[公益]care/model"
			candidate = f.charity.snapshot.Candidates[0]
			f.charity.preflight.ModelTypes = modeltype.Default()
		}
		request, err := openai.DecodeImageRequest(strings.NewReader(`{"model":"`+model+`","prompt":"test"}`), 0)
		if err != nil {
			t.Fatal(err)
		}
		defer request.Clear()
		denied := httptest.NewRecorder()
		f.service.Images(context.Background(), denied, 1, request, nil, "application/json", "en")
		if denied.Code != 400 || !strings.Contains(denied.Body.String(), "model does not support this request type") || len(f.claims.events) != 0 || f.openAI.calls != 0 || f.personal.snapCalls != 0 || f.charity.snapCalls != 0 {
			t.Fatalf("denied=%d %s events=%v", denied.Code, denied.Body.String(), f.claims.events)
		}
		types := modeltype.Set{contract.OperationImagesGenerations}
		f.personal.preflight.ModelTypes = types
		f.personal.snapshot.ModelTypes = types
		f.charity.preflight.ModelTypes = types
		f.charity.snapshot.ModelTypes = types
		f.addDispatch(candidate)
		f.openAI.attempt = func(_ context.Context, input connector.AttemptInput) contract.AttemptResult {
			if input.Image == nil || input.Ingress != nil || input.Embedding != nil || input.Operation != contract.OperationImagesGenerations || input.Policy.FlattenToolCalls || input.Policy.ForceStoreFalse {
				t.Fatal("incorrect image attempt")
			}
			input.Sink.Write([]byte(`{"created":0,"data":[{"b64_json":"aW1hZ2U="}]}`))
			return contract.AttemptResult{Success: true, Committed: true, Failure: contract.FailureNone, UpstreamStatus: 200, ClientStatus: 200}
		}
		accepted := httptest.NewRecorder()
		f.service.Images(context.Background(), accepted, 1, request, nil, "application/json", "en")
		want := claim.RouteOpenAIImages
		if charity {
			want = claim.RouteCharityImages
		}
		if accepted.Code != 200 || f.openAI.calls != 1 || len(f.claims.accepts) != 1 || f.claims.accepts[0].Route != want {
			t.Fatalf("accepted=%d %s events=%v", accepted.Code, accepted.Body.String(), f.claims.events)
		}
	}
}

func TestImageCORSPreflight(t *testing.T) {
	called := false
	handler := BrowserCORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	request := httptest.NewRequest("OPTIONS", "/v1/images/generations", nil)
	request.Header.Set("Origin", "https://client.example")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
	writer := httptest.NewRecorder()
	handler.ServeHTTP(writer, request)
	if writer.Code != 204 || called || writer.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("invalid image CORS preflight")
	}
}

func TestModelTypeRemovalBetweenPreflightAndCandidateSnapshot(t *testing.T) {
	for _, charity := range []bool{false, true} {
		f := newServiceFixture(t, nil)
		model := "provider/model"
		f.personal.snapshot.Revision++
		f.personal.snapshot.ModelTypes = modeltype.Default()
		if charity {
			model = "[公益]care/model"
			f.charity.snapshot.Revision++
			f.charity.snapshot.ModelTypes = modeltype.Default()
		}
		request, err := openai.DecodeImageRequest(strings.NewReader(`{"model":"`+model+`","prompt":"test"}`), 0)
		if err != nil {
			t.Fatal(err)
		}
		defer request.Clear()
		writer := httptest.NewRecorder()
		f.service.Images(context.Background(), writer, 1, request, nil, "application/json", "en")
		if writer.Code != 400 || !strings.Contains(writer.Body.String(), "model does not support this request type") || len(f.claims.accepts) != 0 || len(f.claims.events) != 0 || len(f.claims.outcomes) != 0 || f.openAI.calls != 0 {
			t.Fatalf("charity=%t status=%d accepts=%d claim events=%v outcomes=%v upstream=%d", charity, writer.Code, len(f.claims.accepts), f.claims.events, f.claims.outcomes, f.openAI.calls)
		}
	}
}

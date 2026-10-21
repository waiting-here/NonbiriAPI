package forward

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
	"github.com/waiting-here/NonbiriAPI/internal/requestkind"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestInferenceDirectOriginalAuditBeforeProtocolRefusal(t *testing.T) {
	for _, charity := range []bool{false, true} {
		for _, operation := range []contract.Operation{contract.OperationResponses, contract.OperationSystemOne} {
			f := newServiceFixture(t, nil)
			guard := &originalDecisionGuard{t: t}
			f.service.clientGuard = guard
			f.service.adaptations = adaptationReaderFunc(func(refs []requestadaptation.Ref) map[requestadaptation.Ref]requestadaptation.Snapshot {
				if len(refs) != 1 || refs[0].Scope != requestadaptation.ScopePersonalModel {
					t.Fatal("physical adaptation preceded original audit")
				}
				doc := requestadaptation.Empty(refs[0].Scope)
				doc.FixedHeaders.Values["User-Agent"] = "configured"
				return map[requestadaptation.Ref]requestadaptation.Snapshot{refs[0]: {Document: doc, Revision: "1"}}
			})
			model := "provider/model"
			if charity {
				model = "[公益]care/model"
			}
			body := `{"model":"` + model + `","input":"hi","reasoning":{"effort":"low"}}`
			if operation == contract.OperationSystemOne {
				body = `{"model":"` + model + `","state":"hi","questions":{"q":{"type":"box"}}}`
			}
			request, err := openai.DecodeInferenceRequest(strings.NewReader(body), operation, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer request.Clear()
			ctx := observability.WithSource(context.Background(), observability.Source{UserAgent: "original-client"})
			writer := httptest.NewRecorder()
			f.service.Inference(ctx, writer, 1, request, []byte(body), "application/json", "en", http.Header{"Typesafe-Danger-Zone-Images": {"invalid"}})
			if writer.Code != 403 || guard.calls != 1 || len(f.claims.accepts) != 0 || f.personal.snapCalls+f.charity.snapCalls != 0 {
				t.Fatalf("operation=%s charity=%t status=%d guard=%d", operation, charity, writer.Code, guard.calls)
			}
		}
	}
}

func TestSystemOneHTTPMultipartUsesIndependentClaimAndOptions(t *testing.T) {
	for _, charity := range []bool{false, true} {
		f := newServiceFixture(t, nil)
		f.personal.preflight.ModelTypes = modeltype.Set{contract.OperationSystemOne}
		f.personal.snapshot.ModelTypes = f.personal.preflight.ModelTypes
		f.charity.preflight.ModelTypes = modeltype.Set{contract.OperationSystemOne}
		f.charity.snapshot.ModelTypes = f.charity.preflight.ModelTypes
		model := "provider/model"
		candidate := f.personal.snapshot.Candidates[0]
		if charity {
			model = "[公益]care/model"
			candidate = f.charity.snapshot.Candidates[0]
		}
		candidate.ConnectorType = contract.TypeTypeSafeSystemOne
		if charity {
			f.charity.snapshot.Candidates = []RouteCandidate{candidate}
		} else {
			f.personal.snapshot.Candidates = []RouteCandidate{candidate}
		}
		descriptor, _ := f.service.registry.Descriptor(contract.TypeTypeSafeSystemOne)
		driver := &fakeConnector{connectorType: contract.TypeTypeSafeSystemOne, capabilities: descriptor.Capabilities, consume: true}
		f.service.connectors[contract.TypeTypeSafeSystemOne] = driver
		f.addDispatch(candidate)
		driver.attempt = func(_ context.Context, input connector.AttemptInput) contract.AttemptResult {
			if input.Operation != contract.OperationSystemOne || input.Decision != nil || input.Inference == nil || input.Inference.CheckTarget(contract.TypeTypeSafeSystemOne, input.Policy) != nil {
				t.Fatal("independent protocol or image opt-in lost")
			}
			logical, err := input.Inference.LogicalBody()
			if err != nil || !json.Valid(logical) || !bytes.Contains(logical, []byte(`"state":"image-placeholder"`)) {
				t.Fatal("policy logical body lost")
			}
			_, _ = input.Sink.Write([]byte(`{"answers":{"q":{"type":"noul","noul":0.5}}}`))
			return contract.AttemptResult{Success: true, Committed: true, Failure: contract.FailureNone, UpstreamStatus: 200, ClientStatus: 200, Usage: contract.Usage{Present: true, UncachedInputTokens: 2, OutputTokens: 1}}
		}
		var body bytes.Buffer
		upload := multipart.NewWriter(&body)
		part, _ := upload.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="request"`}, "Content-Type": {"application/json"}})
		_, _ = io.WriteString(part, `{"model":"`+model+`","state":"image-placeholder","questions":{"q":{"type":"noul"}},"assets":{"image-placeholder":{"part":"image"}}}`)
		part, _ = upload.CreateFormFile("image", "image.png")
		_, _ = part.Write([]byte{1, 2, 3})
		_ = upload.Close()
		r := withCallerIdentity(httptest.NewRequest(http.MethodPost, "/v1/systemone", &body), resources.CallerIdentity{UserID: 1, Generation: 1})
		r.Header.Set("Content-Type", upload.FormDataContentType())
		r.Header.Set("TypeSafe-Danger-Zone-Images", "true")
		// The classifier retains the multipart bytes while auditing its JSON envelope.
		classify := RPMClassifier()
		prepared, isCharity, cleanup, err := classify(r)
		if err != nil || isCharity != charity {
			t.Fatalf("classifier: %v %t", err, isCharity)
		}
		defer cleanup()
		writer := httptest.NewRecorder()
		NewHandler(f.service).ServeHTTP(writer, prepared)
		if writer.Code != 200 || driver.calls != 1 || len(f.claims.accepts) != 1 || f.claims.accepts[0].Route != requestkind.ForOperation(contract.OperationSystemOne, charity) {
			t.Fatalf("charity=%t status=%d calls=%d claims=%v body=%s", charity, writer.Code, driver.calls, f.claims.events, writer.Body.String())
		}
	}
}

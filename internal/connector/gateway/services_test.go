package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
)

const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aS1sAAAAASUVORK5CYII="

func imageRequest(t *testing.T, extra string) *openai.ImageRequest {
	t.Helper()
	request, err := openai.DecodeImageRequest(strings.NewReader(`{"model":"public-model","prompt":"A blue circle"`+extra+`}`), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(request.Clear)
	return request
}

func embeddingRequest(t *testing.T, extra string) *openai.EmbeddingRequest {
	t.Helper()
	request, err := openai.DecodeEmbeddingRequest(strings.NewReader(`{"model":"public-model","input":["hello"]`+extra+`}`), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(request.Clear)
	return request
}

func rerankRequest(t *testing.T, documents, extra string) *openai.InferenceRequest {
	t.Helper()
	request, err := openai.DecodeInferenceRequest(strings.NewReader(`{"model":"public-model","query":"fruit","documents":`+documents+extra+`}`), contract.OperationRerank, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(request.Clear)
	return request
}

func versionTarget(kind contract.Type) contract.Target {
	return contract.NewTarget(kind, "https://upstream.example/native/v3/ai", "provider/private-model")
}

func TestFourServicesUseExplicitVersionAndSharedTransport(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
		for _, service := range []string{"language", "embedding", "image", "reranking"} {
			t.Run(string(kind)+"/"+service, func(t *testing.T) {
				var sent object
				b := &fakeBackend{do: func(r *http.Request) (*http.Response, error) {
					modelHeader := "ai-model-id"
					if service == "language" {
						modelHeader = "ai-language-model-id"
					}
					if r.URL.Path != "/native/v3/ai/"+service+"-model" ||
						r.Header.Get("ai-"+service+"-model-specification-version") != specificationVersion(kind) ||
						r.Header.Get(modelHeader) != "provider/private-model" ||
						r.Header.Get("ai-gateway-protocol-version") != "0.0.1" ||
						r.Header.Get("Authorization") != "Bearer sk-test-credential-long" ||
						r.Header.Get("X-Trace") != "fixture" {
						t.Fatal(r.URL, r.Header)
					}
					body, err := io.ReadAll(r.Body)
					if err != nil || json.Unmarshal(body, &sent) != nil {
						t.Fatalf("body: %s %v", body, err)
					}
					if sent["model"] != nil || !bytes.Contains(body, []byte(`"user":"attribution-label"`)) ||
						!bytes.Contains(body, []byte(`"extra":true`)) {
						t.Fatal(string(body))
					}
					switch service {
					case "language":
						if r.Header.Get("ai-language-model-streaming") != "false" {
							t.Fatal(r.Header)
						}
						return response(chatOK, "application/json"), nil
					case "embedding":
						if sent["encoding_format"] != nil || string(sent["values"]) != `["hello"]` {
							t.Fatal(string(body))
						}
						return response(`{"embeddings":[[0.25,-0.5]],"usage":{"tokens":2},"warnings":[{"type":"deprecated","setting":"old","message":"private detail"}]}`, "application/json"), nil
					case "image":
						if string(sent["seed"]) != "0" || string(sent["size"]) != `"1024x1024"` || string(sent["aspectRatio"]) != `"1:1"` || sent["response_format"] != nil {
							t.Fatal(string(body))
						}
						return response(`{"images":["`+tinyPNG+`"],"usage":{"inputTokens":2,"outputTokens":3,"totalTokens":5}}`, "application/json"), nil
					default:
						if string(sent["topN"]) != "1" || sent["return_documents"] != nil || !bytes.Contains(sent["documents"], []byte(`"type":"text"`)) {
							t.Fatal(string(body))
						}
						return response(`{"ranking":[{"index":1,"relevanceScore":2.5}],"usage":{"tokens":999},"providerMetadata":{"x":{"search_units":999}}}`, "application/json"), nil
					}
				}}
				a, _ := NewAdapter(b)
				w := httptest.NewRecorder()
				policy := contract.AttemptPolicy{
					AdditionalHeaders: http.Header{"X-Trace": {"fixture"}},
					NativeExtensions:  map[string]json.RawMessage{"/providerOptions/gateway/extra": json.RawMessage("true")},
				}
				var result contract.AttemptResult
				switch service {
				case "language":
					result = a.AttemptWithPolicy(context.Background(), w, versionTarget(kind), secret(), chat(t, prompt), nil, "attribution-label", policy)
				case "embedding":
					extra := `,"encoding_format":"base64"`
					if kind == contract.TypeAISDKGatewayV4 {
						extra += `,"dimensions":2`
					}
					result = a.AttemptWithPolicy(context.Background(), w, versionTarget(kind), secret(), nil, embeddingRequest(t, extra), "attribution-label", policy)
				case "image":
					result = a.AttemptImageWithPolicy(context.Background(), w, versionTarget(kind), secret(), imageRequest(t, `,"seed":0,"size":"1024x1024","aspect_ratio":"1:1"`), "attribution-label", policy)
				default:
					result = a.AttemptInferenceWithPolicy(context.Background(), w, versionTarget(kind), secret(), rerankRequest(t, `["sky","apple"]`, `,"top_n":1,"return_documents":true`), "attribution-label", policy)
				}
				if !result.Success || !result.Committed || b.calls != 1 || strings.Contains(w.Body.String(), "private-model") || strings.Contains(w.Body.String(), "private detail") {
					t.Fatalf("result=%+v body=%s calls=%d", result, w.Body.String(), b.calls)
				}
				switch service {
				case "embedding":
					var output struct{ Data []struct{ Embedding string } }
					if json.Unmarshal(w.Body.Bytes(), &output) != nil || len(output.Data) != 1 {
						t.Fatal(w.Body.String())
					}
					vector, err := base64.StdEncoding.DecodeString(output.Data[0].Embedding)
					if err != nil || len(vector) != 8 || math.Float32frombits(binary.LittleEndian.Uint32(vector)) != 0.25 || math.Float32frombits(binary.LittleEndian.Uint32(vector[4:])) != -0.5 {
						t.Fatal(output, err)
					}
				case "image":
					if !strings.Contains(w.Body.String(), `"b64_json"`) || !result.Usage.Present || result.Usage.OutputTokens != 3 || result.Usage.Modalities.InputPresent || result.Usage.Modalities.OutputPresent {
						t.Fatalf("%+v %s", result, w.Body.String())
					}
				case "reranking":
					if result.Usage.Present || result.Usage.SearchUnitsPresent || strings.Contains(w.Body.String(), "usage") || !strings.Contains(w.Body.String(), `"text":"apple"`) || !strings.Contains(w.Body.String(), `"relevance_score":2.5`) {
						t.Fatalf("%+v %s", result, w.Body.String())
					}
				}
			})
		}
	}
}

func TestV4PromptFilesAndReasoningPreserveTheirMeaning(t *testing.T) {
	request := chat(t, `{"model":"public","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://images.example/a.png"}},{"type":"image_url","image_url":{"url":"data:image/png;base64,aGk="}}]}],"reasoning_effort":"high"}`)
	defer request.Clear()
	if !SupportsRequestForType(contract.TypeAISDKGatewayV4, request) {
		t.Fatal("v4 preflight rejected standard reasoning")
	}
	body, err := CompileWithModelForType(contract.TypeAISDKGatewayV4, request, "", gatewaypolicy.Model{}, nil)
	if err != nil || !bytes.Contains(body, []byte(`"reasoning":"high"`)) || !bytes.Contains(body, []byte(`"data":{"type":"url","url":"https://images.example/a.png"}`)) || !bytes.Contains(body, []byte(`"data":{"data":"aGk=","type":"data"}`)) || bytes.Contains(body, []byte("reasoningEffort")) {
		t.Fatalf("%s %v", body, err)
	}
	clear(body)
	body, err = CompileWithModel(request, "", gatewaypolicy.Model{}, nil)
	clear(body)
	if err == nil {
		t.Fatal("v3 lost model-specific reasoning preflight")
	}
	for _, path := range []string{"/reasoning", "/providerOptions/openai/reasoningEffort"} {
		body, err = CompileWithModelForType(contract.TypeAISDKGatewayV4, request, "", gatewaypolicy.Model{}, map[string]json.RawMessage{path: json.RawMessage(`"low"`)})
		clear(body)
		if err == nil {
			t.Fatal("conflicting native reasoning accepted", path)
		}
	}
}

func TestNewServicesRejectUnsupportedFieldsBeforeDispatch(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
		b := &fakeBackend{do: func(*http.Request) (*http.Response, error) {
			t.Fatal("unsupported request dispatched")
			return nil, nil
		}}
		a, _ := NewAdapter(b)
		for _, tc := range []struct{ field, extra string }{
			{"response_format", `,"response_format":"url"`}, {"size", `,"size":"auto"`}, {"stream", `,"stream":true`},
			{"quality", `,"quality":"high"`}, {"style", `,"style":"natural"`}, {"partial_images", `,"partial_images":0`},
			{"seed", `,"seed":1.5`}, {"unknown", `,"unknown":null`},
		} {
			request := imageRequest(t, tc.extra)
			err := CheckImageTarget(versionTarget(kind), request, contract.AttemptPolicy{})
			var rejected *contract.RequestRejection
			if !errors.As(err, &rejected) || rejected.Field != tc.field {
				t.Fatalf("%s: %v", tc.extra, err)
			}
			result := a.AttemptImageWithPolicy(context.Background(), httptest.NewRecorder(), versionTarget(kind), secret(), request, "", contract.AttemptPolicy{})
			if result.Success {
				t.Fatal(tc.extra)
			}
		}
		for _, tc := range []struct{ documents, extra, field string }{
			{`["text",{"text":"object"}]`, "", "documents"},
			{`["text"]`, `,"rank_fields":["text"]`, "rank_fields"},
			{`["text"]`, `,"truncation":false`, "truncation"},
		} {
			request := rerankRequest(t, tc.documents, tc.extra)
			err := CheckInferenceTarget(versionTarget(kind), request, contract.AttemptPolicy{})
			var rejected *contract.RequestRejection
			if !errors.As(err, &rejected) || rejected.Field != tc.field {
				t.Fatalf("%+v: %v", tc, err)
			}
			result := a.AttemptInferenceWithPolicy(context.Background(), httptest.NewRecorder(), versionTarget(kind), secret(), request, "", contract.AttemptPolicy{})
			if result.Success {
				t.Fatal(tc)
			}
		}
		if b.calls != 0 {
			t.Fatal(b.calls)
		}
	}
	request := embeddingRequest(t, `,"dimensions":2`)
	if SupportsEmbeddingForType(contract.TypeAISDKGatewayV3, request) || !SupportsEmbeddingForType(contract.TypeAISDKGatewayV4, request) {
		t.Fatal("dimensions were not version-specific")
	}
	if err := CheckEmbeddingTarget(versionTarget(contract.TypeAISDKGatewayV3), request, contract.AttemptPolicy{}); err == nil {
		t.Fatal("v3 dimensions accepted")
	}
	base64Request := embeddingRequest(t, `,"encoding_format":"base64"`)
	if SupportsEmbedding(base64Request) || !SupportsEmbeddingForType(contract.TypeAISDKGatewayV3, base64Request) {
		t.Fatal("legacy entry point or extended encoding changed")
	}
}

func TestServiceResponseValidityAndUnknownUsage(t *testing.T) {
	image := imageRequest(t, "")
	for _, usage := range []string{`null`, `{}`, `{"inputTokens":2,"outputTokens":3}`, `{"inputTokens":-1,"outputTokens":2,"totalTokens":1}`, `{"inputTokens":"2","outputTokens":3,"totalTokens":5}`} {
		body, u, err := translateImage([]byte(`{"images":["`+tinyPNG+`"],"usage":`+usage+`}`), image, 1)
		if err != nil || u.Present || bytes.Contains(body, []byte("usage")) {
			t.Fatalf("%s %+v %v", body, u, err)
		}
	}
	for _, raw := range []string{`{"images":[]}`, `{"images":["not base64"]}`, `{"images":["aGk=\\n"]}`, `{"images":[null]}`, `{"images":["aGk="],"isRetryable":2}`} {
		if _, _, err := translateImage([]byte(raw), image, 1); err == nil {
			t.Fatal(raw)
		}
	}
	embedding := embeddingRequest(t, `,"encoding_format":"base64","dimensions":2`)
	for _, raw := range []string{`{"embeddings":[[1]]}`, `{"embeddings":[[1,3.5e38]]}`, `{"embeddings":[[1,null]]}`, `{"embeddings":[[1,2]],"usage":{"tokens":-1}}`, `{"embeddings":[[1,2]],"warnings":null}`} {
		if _, _, err := translateEmbeddingRequest([]byte(raw), embedding); err == nil {
			t.Fatal(raw)
		}
	}
	request := rerankRequest(t, `[{"title":"a"},{"title":"b"}]`, `,"return_documents":true`)
	body, u, err := translateRerank([]byte(`{"ranking":[{"index":1,"relevanceScore":-2}],"providerMetadata":{"cohere":{"billedUnits":{"searchUnits":100}}}}`), request)
	if err != nil || u.Present || u.SearchUnitsPresent || !bytes.Contains(body, []byte(`"document":{"title":"b"}`)) {
		t.Fatalf("%s %+v %v", body, u, err)
	}
	for _, raw := range []string{`{"ranking":[]}`, `{"ranking":[{"index":2,"relevanceScore":1}]}`, `{"ranking":[{"index":0,"relevanceScore":1},{"index":0,"relevanceScore":2}]}`, `{"ranking":[{"index":0.5,"relevanceScore":1}]}`, `{"ranking":[{"index":0,"relevanceScore":null}]}`, `{"ranking":[{"index":0,"relevanceScore":1e400}]}`} {
		if _, _, err := translateRerank([]byte(raw), request); err == nil {
			t.Fatal(raw)
		}
	}
}

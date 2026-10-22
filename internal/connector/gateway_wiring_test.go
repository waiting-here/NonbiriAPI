package connector

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
)

func TestGatewayTargetHooksRejectBeforeDispatch(t *testing.T) {
	r := NewDefaultRegistry()
	for _, tc := range []struct {
		kind        contract.Type
		op          contract.Operation
		body, field string
	}{
		{contract.TypeAISDKGatewayV3, contract.OperationEmbeddings, `{"model":"public","input":"hello","dimensions":2}`, "dimensions"},
		{contract.TypeAISDKGatewayV4, contract.OperationImagesGenerations, `{"model":"public","prompt":"sunrise","quality":"high"}`, "quality"},
		{contract.TypeAISDKGatewayV4, contract.OperationRerank, `{"model":"public","query":"q","documents":["a",{"text":"b"}]}`, "documents"},
	} {
		input := gatewayWiringInput(t, tc.kind, tc.op, tc.body)
		var rejected *contract.RequestRejection
		if err := gatewayWiringCheck(r, input); !errors.As(err, &rejected) || rejected.Field != tc.field {
			t.Fatalf("target hook lost field: %v", err)
		}
		b := &gatewayWiringBackend{do: func(*http.Request) (*http.Response, error) { t.Fatal("rejected request dispatched"); return nil, nil }}
		driver, _ := r.NewConnector(tc.kind, Dependencies{Backend: b})
		if result := driver.Attempt(context.Background(), input); result.Success || b.calls != 0 {
			t.Fatal(result)
		}
		input.Policy.ForceStoreFalse = true
		if err := gatewayWiringCheck(r, input); !errors.As(err, &rejected) || rejected.Field != "store" {
			t.Fatalf("incompatible policy admitted: %v", err)
		}
	}
}

type gatewayCommitSink struct {
	*httptest.ResponseRecorder
	started                     int
	checkpointError, flushError bool
	cancel                      context.CancelFunc
}

func (w *gatewayCommitSink) MarkResponseStarted() error {
	w.started++
	if w.checkpointError {
		return errors.New("checkpoint failed")
	}
	return nil
}
func (w *gatewayCommitSink) Write(b []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(b)
	if w.cancel != nil {
		w.cancel()
	}
	return n, err
}
func (w *gatewayCommitSink) FlushError() error {
	if w.flushError {
		return io.ErrClosedPipe
	}
	w.ResponseRecorder.Flush()
	return nil
}
func TestGatewayResponsesDownstreamFailuresRemainNeutral(t *testing.T) {
	wire := gatewayWiringSSE(`{"type":"text-start","id":"t"}`, `{"type":"text-delta","id":"t","delta":"answer"}`, `{"type":"text-end","id":"t"}`, `{"type":"finish","finishReason":{"unified":"stop"},"usage":`+gatewayWiringUsage+`}`)
	for _, mode := range []string{"checkpoint", "cancel", "flush"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			input := gatewayWiringInput(t, contract.TypeAISDKGatewayV4, contract.OperationResponses, `{"model":"public","input":"hello","stream":true}`)
			input.Policy.GatewayModel = &gatewaypolicy.Model{Storage: gatewaypolicy.OmitFalse}
			sink := &gatewayCommitSink{ResponseRecorder: httptest.NewRecorder(), checkpointError: mode == "checkpoint", flushError: mode == "flush"}
			if mode == "cancel" {
				sink.cancel = cancel
			}
			input.Sink = sink
			b := &gatewayWiringBackend{do: func(*http.Request) (*http.Response, error) { return gatewayWiringResponse(wire, true), nil }}
			driver, _ := NewDefaultRegistry().NewConnector(input.Target.Type(), Dependencies{Backend: b})
			result := driver.Attempt(ctx, input)
			origin := contract.OriginDownstream
			failure := contract.FailureSink
			if mode == "checkpoint" {
				origin = contract.OriginPlatform
				failure = contract.FailureInternal
			}
			if mode == "cancel" {
				origin = contract.OriginClientCancel
				failure = contract.FailureCanceled
			}
			if result.Success || result.Failure != failure || result.FailureOrigin != origin || result.StreakDisposition != contract.StreakNeutral || result.Committed != (mode != "checkpoint") || sink.started != 1 {
				t.Fatalf("%s: %+v starts=%d", mode, result, sink.started)
			}
		})
	}
}

const gatewayWiringUsage = `{"inputTokens":{"total":4,"noCache":4,"cacheRead":0,"cacheWrite":0},"outputTokens":{"total":3,"text":3,"reasoning":0}}`
const gatewayWiringJSON = `{"content":[{"type":"reasoning","text":"real thought"},{"type":"text","text":"answer"}],"finishReason":{"unified":"stop"},"usage":` + gatewayWiringUsage + `}`

type gatewayWiringBackend struct {
	do    func(*http.Request) (*http.Response, error)
	calls int
	limit int64
}

func (b *gatewayWiringBackend) Open(string) (backend.EndpointClient, error) { return b, nil }
func (b *gatewayWiringBackend) BaseURL() string                             { return "https://gateway.example/native" }
func (b *gatewayWiringBackend) MaxResponseBytes() int64 {
	if b.limit > 0 {
		return b.limit
	}
	return 1 << 20
}
func (b *gatewayWiringBackend) Do(r *http.Request) (*http.Response, error) { b.calls++; return b.do(r) }

type gatewayWiringAttribution struct{}

func (gatewayWiringAttribution) GatewayUserAttributionEnabled(context.Context) (bool, error) {
	return true, nil
}
func gatewayWiringResponse(raw string, stream bool) *http.Response {
	kind := "application/json"
	if stream {
		kind = "text/event-stream"
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(raw))}
}
func gatewayWiringInput(t *testing.T, kind contract.Type, op contract.Operation, body string) AttemptInput {
	t.Helper()
	input := AttemptInput{Operation: op, Target: contract.NewTarget(kind, "https://gateway.example/native", "private"), Credential: contract.NewShortLivedSecret([]byte("wiring-secret-material"), nil), Sink: httptest.NewRecorder(), Policy: contract.AttemptPolicy{SafetyIdentifier: "server-attribution"}}
	t.Cleanup(input.Credential.Clear)
	var err error
	switch op {
	case contract.OperationChatCompletions:
		input.Ingress, err = openai.DecodeChatRequest(strings.NewReader(body), 0)
		if err == nil {
			t.Cleanup(input.Ingress.Clear)
		}
	case contract.OperationEmbeddings:
		input.Embedding, err = openai.DecodeEmbeddingRequest(strings.NewReader(body), 0)
		if err == nil {
			t.Cleanup(input.Embedding.Clear)
		}
	case contract.OperationImagesGenerations:
		input.Image, err = openai.DecodeImageRequest(strings.NewReader(body), 0)
		if err == nil {
			t.Cleanup(input.Image.Clear)
		}
	default:
		input.Inference, err = openai.DecodeInferenceRequest(strings.NewReader(body), op, 0)
		if err == nil {
			t.Cleanup(input.Inference.Clear)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	return input
}
func gatewayWiringCheck(r *Registry, input AttemptInput) error {
	if input.Inference != nil {
		return r.CheckInferenceTarget(input.Target, input.Inference, input.Policy)
	}
	return r.CheckTargetRequest(input.Target, input.Operation, input.Ingress, input.Embedding, input.Policy, input.Image)
}

func TestGatewayRegistryDispatchesBothVersionsAndServices(t *testing.T) {
	r := NewDefaultRegistry()
	for _, kind := range []contract.Type{contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
		for _, tc := range []struct {
			op                      contract.Operation
			service, body, response string
		}{
			{contract.OperationChatCompletions, "language", `{"model":"public","messages":[{"role":"user","content":"hello"}]}`, gatewayWiringJSON},
			{contract.OperationResponses, "language", `{"model":"public","input":"hello"}`, gatewayWiringJSON},
			{contract.OperationEmbeddings, "embedding", `{"model":"public","input":"hello","encoding_format":"base64"}`, `{"embeddings":[[0.5,-1]],"usage":{"tokens":4}}`},
			{contract.OperationImagesGenerations, "image", `{"model":"public","prompt":"sunrise"}`, `{"images":["iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jg4cAAAAASUVORK5CYII="]}`},
			{contract.OperationRerank, "reranking", `{"model":"public","query":"q","documents":["a","b"],"top_n":1,"return_documents":true}`, `{"ranking":[{"index":1,"relevanceScore":2.5}]}`},
		} {
			t.Run(string(kind)+"/"+string(tc.op), func(t *testing.T) {
				input := gatewayWiringInput(t, kind, tc.op, tc.body)
				input.Policy.GatewayModel = &gatewaypolicy.Model{Storage: gatewaypolicy.OmitFalse}
				if err := gatewayWiringCheck(r, input); err != nil {
					t.Fatal(err)
				}
				b := &gatewayWiringBackend{do: func(req *http.Request) (*http.Response, error) {
					version := "3"
					if kind == contract.TypeAISDKGatewayV4 {
						version = "4"
					}
					if req.URL.Path != "/native/"+tc.service+"-model" || req.Header.Get("ai-"+tc.service+"-model-specification-version") != version {
						t.Fatal(req.URL, req.Header)
					}
					raw, _ := io.ReadAll(req.Body)
					defer clear(raw)
					if !strings.Contains(string(raw), `"user":"server-attribution"`) || strings.Contains(string(raw), `"input":`) {
						t.Fatal("native protocol or attribution missing")
					}
					return gatewayWiringResponse(tc.response, false), nil
				}}
				driver, err := r.NewConnector(kind, Dependencies{Backend: b, GatewayAttribution: gatewayWiringAttribution{}})
				if err != nil {
					t.Fatal(err)
				}
				observed := []Observation{}
				observer, err := NewSafeObserver(4, func(event Observation) { observed = append(observed, event) })
				if err != nil {
					t.Fatal(err)
				}
				defer observer.Close()
				input.Observer = observer
				result := driver.Attempt(context.Background(), input)
				_ = observer.Close()
				if !result.Success || !result.Committed || b.calls != 1 || len(observed) != 2 || !observed[1].GatewayUserAttributionSent || result.GatewayUserAttributionSent == nil || !*result.GatewayUserAttributionSent {
					t.Fatalf("%+v observations=%+v", result, observed)
				}
				if tc.service == "language" && (!result.Usage.Modalities.OutputPresent || result.Usage.Modalities.Output[contract.ModalityText] != 3 || !observed[1].Usage.Modalities.OutputPresent) {
					t.Fatal("Gateway modality usage was replaced by projection usage")
				}
				wire := input.Sink.(*httptest.ResponseRecorder).Body.String()
				if strings.Contains(wire, "private") || strings.Contains(wire, "server-attribution") {
					t.Fatal("private metadata exposed")
				}
				if tc.op == contract.OperationResponses && (!strings.Contains(wire, `"type":"reasoning_text"`) || !strings.Contains(wire, `"summary":[]`)) {
					t.Fatal(wire)
				}
			})
		}
	}
}

func TestGatewayResponsesAdmissionKeepsFiniteMappingAndStoragePolicy(t *testing.T) {
	r := NewDefaultRegistry()
	kind := contract.TypeAISDKGatewayV4
	input := gatewayWiringInput(t, kind, contract.OperationResponses, `{"model":"public","input":"hello"}`)
	err := gatewayWiringCheck(r, input)
	var rejected *contract.RequestRejection
	if !errors.As(err, &rejected) || rejected.Field != "store" {
		t.Fatalf("missing verified storage policy admitted: %v", err)
	}
	for _, extra := range []string{`,"reasoning":{"effort":"low"}`, `,"metadata":{"a":1}`, `,"tools":[{"type":"web_search"}]`, `,"include":["reasoning.encrypted_content"]`} {
		input := gatewayWiringInput(t, kind, contract.OperationResponses, `{"model":"public","input":"hello"`+extra+`}`)
		input.Policy.GatewayModel = &gatewaypolicy.Model{Storage: gatewaypolicy.OmitFalse}
		if r.SupportsInferenceRequest(kind, input.Inference) || gatewayWiringCheck(r, input) == nil {
			t.Fatal("expanded finite input: " + extra)
		}
		b := &gatewayWiringBackend{do: func(*http.Request) (*http.Response, error) {
			t.Fatal("unsupported request dispatched")
			return nil, nil
		}}
		driver, _ := r.NewConnector(kind, Dependencies{Backend: b})
		if result := driver.Attempt(context.Background(), input); result.Success || b.calls != 0 {
			t.Fatal(result)
		}
	}
	for _, mode := range []string{"storage", "flatten", "native-conflict"} {
		input := gatewayWiringInput(t, kind, contract.OperationResponses, `{"model":"public","input":"hello"}`)
		input.Policy.GatewayModel = &gatewaypolicy.Model{Storage: gatewaypolicy.OmitFalse}
		switch mode {
		case "storage":
			input.Policy.ForceStoreFalse = true
		case "flatten":
			input.Policy.FlattenToolCalls = true
		default:
			input.Policy.NativeExtensions = map[string]json.RawMessage{"/providerOptions/openai/store": json.RawMessage("true")}
		}
		if gatewayWiringCheck(r, input) == nil {
			t.Fatal(mode)
		}
		b := &gatewayWiringBackend{do: func(*http.Request) (*http.Response, error) { t.Fatal("policy rejection dispatched"); return nil, nil }}
		driver, _ := r.NewConnector(kind, Dependencies{Backend: b})
		if result := driver.Attempt(context.Background(), input); result.Success || b.calls != 0 {
			t.Fatal(result)
		}
	}
}

func gatewayWiringSSE(parts ...string) string {
	return "data: " + strings.Join(parts, "\n\ndata: ") + "\n\n"
}
func TestGatewayResponsesStreamFailuresAndCommitment(t *testing.T) {
	start := `{"type":"text-start","id":"text"}`
	delta := `{"type":"text-delta","id":"text","delta":"hello"}`
	end := `{"type":"text-end","id":"text"}`
	finish := `{"type":"finish","finishReason":{"unified":"stop"},"usage":` + gatewayWiringUsage + `}`
	for _, tc := range []struct {
		name, wire         string
		limit              int64
		success, committed bool
	}{
		{"complete", gatewayWiringSSE(start, delta, end, finish), 0, true, true},
		{"early-eof", gatewayWiringSSE(start, delta), 0, false, true},
		{"before-output", gatewayWiringSSE(`{"type":"error","error":{"message":"private"}}`), 0, false, false},
		{"reported-error", gatewayWiringSSE(start, delta, `{"type":"error","error":{"message":"private"}}`), 0, false, true},
		{"reflection", gatewayWiringSSE(`{"type":"reasoning-start","id":"r"}`, `{"type":"reasoning-delta","id":"r","delta":"wiring-secret-material"}`), 0, false, false},
		{"projection-limit-before-usage", gatewayWiringSSE(start, `{"type":"text-delta","id":"text","delta":"`+strings.Repeat("a", 3000)+`"}`, end, finish), 8192, false, true},
		{"projection-limit-after-usage", gatewayWiringSSE(start, `{"type":"text-delta","id":"text","delta":"`+strings.Repeat("a", 1000)+`"}`, end, finish), 8192, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewDefaultRegistry()
			input := gatewayWiringInput(t, contract.TypeAISDKGatewayV4, contract.OperationResponses, `{"model":"public","input":"hello","stream":true}`)
			input.Policy.GatewayModel = &gatewaypolicy.Model{Storage: gatewaypolicy.OmitFalse}
			b := &gatewayWiringBackend{limit: tc.limit, do: func(*http.Request) (*http.Response, error) { return gatewayWiringResponse(tc.wire, true), nil }}
			driver, _ := r.NewConnector(input.Target.Type(), Dependencies{Backend: b})
			result := driver.Attempt(context.Background(), input)
			wire := input.Sink.(*httptest.ResponseRecorder).Body.String()
			if result.Success != tc.success || result.Committed != tc.committed || strings.Contains(wire, "wiring-secret-material") || strings.Contains(wire, "response.completed") != tc.success {
				t.Fatalf("%+v %s", result, wire)
			}
			if strings.HasPrefix(tc.name, "projection-limit-") && (result.FailureOrigin != contract.OriginPlatform || result.StreakDisposition == contract.StreakUpstreamFailure || result.Usage.Modalities.OutputPresent != (tc.name == "projection-limit-after-usage")) {
				t.Fatalf("local projection affected upstream classification or usage: %+v", result)
			}
			if tc.success && (!result.Usage.Present || !result.Usage.Modalities.OutputPresent) {
				t.Fatal("terminal usage lost")
			}
		})
	}
}

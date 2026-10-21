package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestInferenceCancelClosesUpstreamStream(t *testing.T) {
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(closed)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	r := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"m","input":"hi","stream":true}`)
	result := adapterForServer(t, server.URL, nil, nil).AttemptInference(ctx, httptest.NewRecorder(), NewTarget(server.URL, "private", NewCredential([]byte("secret-material"), nil)), r, contract.TypeOpenAICompatible, contract.AttemptPolicy{})
	if result.Success || !result.Committed {
		t.Fatalf("cancellation lost state: %+v", result)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("upstream request survived cancellation")
	}
}

func TestInferenceBinaryBoundsAndSecretCleanup(t *testing.T) {
	for _, data := range [][]byte{bytes.Repeat([]byte{1}, 128), []byte("secret-material")} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(data)
		}))
		adapter := adapterForServer(t, server.URL, nil, nil)
		adapter.maxImageResponseBytes = 64
		r := inferenceRequestForTest(t, contract.OperationAudioSpeech, `{"model":"m","input":"hi","voice":"alloy"}`)
		writer := httptest.NewRecorder()
		key := []byte("secret-material")
		cipher := []byte("ciphertext-material")
		result := adapter.AttemptInference(context.Background(), writer, NewTarget(server.URL, "private", NewCredential(key, cipher)), r, contract.TypeOpenAICompatible, contract.AttemptPolicy{})
		server.Close()
		if result.Success || result.Committed || writer.Body.Len() != 0 || !bytes.Equal(key, make([]byte, len(key))) || !bytes.Equal(cipher, make([]byte, len(cipher))) {
			t.Fatalf("unsafe binary result: %+v", result)
		}
	}
}

func TestInferenceTranscriptionTerminalAndMalformedUsage(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		request := uploadForTest(t, contract.OperationAudioTranscriptions, map[string]string{"model": "m", "stream": "true"}, map[string][]byte{"file": {1, 2}})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"transcript.text.delta\",\"delta\":\"hi\"}\n\n")
			if terminal {
				_, _ = io.WriteString(w, "data: {\"type\":\"transcript.text.done\",\"text\":\"hi\",\"usage\":{\"input_tokens\":null,\"output_tokens\":2,\"total_tokens\":3}}\n\n")
			}
		}))
		result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), httptest.NewRecorder(), NewTarget(server.URL, "private", NewCredential([]byte("secret-material"), nil)), request, contract.TypeOpenAICompatible, contract.AttemptPolicy{})
		server.Close()
		if result.Success != terminal || result.Usage.Present {
			t.Fatalf("terminal=%t result=%+v", terminal, result)
		}
	}
}

func TestInferenceResponsesIdentityAndSafetyProjection(t *testing.T) {
	for _, changed := range []bool{false, true} {
		request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"public/model","input":"hi","stream":true}`)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\",\"model\":\"private\",\"safety_identifier\":\"safe-id\"}}\n\n")
			id := "r1"
			if changed {
				id = "r2"
			}
			_, _ = io.WriteString(w, `data: {"type":"response.completed","response":{"id":"`+id+`","object":"response","status":"completed","model":"private","safety_identifier":"safe-id","output":[]}}`+"\n\n")
		}))
		writer := httptest.NewRecorder()
		result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), writer, NewTarget(server.URL, "private", NewCredential([]byte("secret-material"), nil)), request, contract.TypeOpenAICompatible, contract.AttemptPolicy{SafetyIdentifier: "safe-id"})
		server.Close()
		if result.Success == changed || strings.Contains(writer.Body.String(), "safe-id") || strings.Contains(writer.Body.String(), "private") {
			t.Fatalf("result=%+v body=%s", result, writer.Body.String())
		}
	}
}

func TestChatStandardMultimodalFieldsRemainNative(t *testing.T) {
	body := `{"model":"public/model","messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"YXVkaW8=","format":"wav"}},{"type":"file","file":{"filename":"report.pdf","file_data":"data:application/pdf;base64,YQ=="}}]}],"modalities":["text","audio"],"audio":{"voice":"alloy","format":"wav"},"store":true}`
	r, err := DecodeChatRequest(strings.NewReader(body), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Clear()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var object map[string]json.RawMessage
		_ = json.NewDecoder(request.Body).Decode(&object)
		if !bytes.Equal(object["store"], []byte("true")) || !bytes.Contains(object["messages"], []byte("input_audio")) || !bytes.Contains(object["messages"], []byte("file_data")) || !bytes.Contains(object["modalities"], []byte("audio")) {
			t.Error("standard fields changed")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c1","object":"chat.completion","created":1,"model":"private","choices":[{"index":0,"message":{"role":"assistant","content":null,"audio":{"id":"a1","data":"YXVkaW8=","expires_at":2,"transcript":"hi"}},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	writer := httptest.NewRecorder()
	result := adapterForServer(t, server.URL, nil, nil).AttemptWithPolicy(context.Background(), writer, NewTarget(server.URL, "private", NewCredential([]byte("secret-material"), nil)), r, contract.AttemptPolicy{SafetyIdentifier: "safe-id"})
	if !result.Success || !strings.Contains(writer.Body.String(), `"data":"YXVkaW8="`) {
		t.Fatalf("multimodal response lost: %+v %s", result, writer.Body.String())
	}
}

func TestInferenceRejectsInconsistentTerminals(t *testing.T) {
	for _, test := range []struct {
		name         string
		operation    contract.Operation
		body, events string
	}{
		{"response status", contract.OperationResponses, `{"model":"m","input":"hi","stream":true}`, `{"type":"response.completed","response":{"id":"r1","object":"response","status":"incomplete","output":[]}}`},
		{"completion index", contract.OperationCompletions, `{"model":"m","prompt":"hi","stream":true}`, `{"choices":[{"index":1,"text":"hi","finish_reason":"stop"}]}`},
		{"completion after finish", contract.OperationCompletions, `{"model":"m","prompt":"hi","stream":true}`, `{"choices":[{"index":0,"text":"hi","finish_reason":"stop"}]}` + "\n\ndata: " + `{"choices":[{"index":0,"text":"later","finish_reason":null}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := inferenceRequestForTest(t, test.operation, test.body)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: "+test.events+"\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()
			writer := httptest.NewRecorder()
			result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), writer, NewTarget(server.URL, "private", NewCredential([]byte("secret-material"), nil)), request, contract.TypeOpenAICompatible, contract.AttemptPolicy{})
			if result.Success || strings.Contains(writer.Body.String(), "[DONE]") {
				t.Fatalf("invalid completion accepted: %+v %s", result, writer.Body.String())
			}
		})
	}
	request := inferenceRequestForTest(t, contract.OperationCompletions, `{"model":"m","prompt":["a","b"]}`)
	if _, _, err := nativeResponseProjection([]byte(`{"choices":[{"index":0,"text":"a","finish_reason":"stop"}]}`), request); err == nil {
		t.Fatal("missing batched completion accepted")
	}
}

func TestInferenceToolsKeepInlineSchemasAndRejectHostedResources(t *testing.T) {
	for _, tools := range []string{
		`[{"type":"namespace","name":"client","tools":[{"type":"function","name":"f","parameters":{"properties":{"file_id":{"type":"string"}}}}]},{"type":"tool_search","execution":"client"},{"type":"apply_patch"},{"type":"shell","environment":{"type":"local"}}]`,
		`[{"type":"image_generation","input_image_mask":{"image_url":"data:image/png;base64,YQ=="}}]`,
	} {
		_ = inferenceRequestForTest(t, contract.OperationResponses, `{"model":"m","input":"hi","tools":`+tools+`}`)
	}
	for _, tools := range []string{
		`[{"type":"namespace","name":"hosted","tools":[{"type":"file_search","vector_store_ids":["vs_1"]}]}]`,
		`[{"type":"image_generation","input_image_mask":{"file_id":"file_1"}}]`,
		`[{"type":"shell","environment":{"type":"container_auto"}}]`,
		`[{"type":"shell","environment":{"type":"local","skills":[{"id":"skill_1"}]}}]`,
	} {
		request, err := DecodeInferenceRequest(strings.NewReader(`{"model":"m","input":"hi","tools":`+tools+`}`), contract.OperationResponses, 0)
		if err == nil {
			request.Clear()
			t.Fatalf("hosted resource accepted: %s", tools)
		}
	}
}

func TestInferenceSpeechEnvelopeRecordsNativeStreaming(t *testing.T) {
	for _, format := range []string{"audio", "sse"} {
		envelope, err := DecodeRequestEnvelope(strings.NewReader(`{"model":"m","stream_format":"`+format+`","input":"hi","voice":"alloy"}`), 0, contract.OperationAudioSpeech)
		if err != nil {
			t.Fatal(err)
		}
		if envelope.Stream != (format == "sse") {
			t.Fatal("native audio streaming metadata was lost")
		}
		envelope.Clear()
	}
}

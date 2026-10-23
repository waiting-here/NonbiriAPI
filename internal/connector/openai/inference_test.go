package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func inferenceRequestForTest(t *testing.T, operation contract.Operation, body string) *InferenceRequest {
	t.Helper()
	r, err := DecodeInferenceRequest(strings.NewReader(body), operation, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Clear)
	return r
}
func uploadForTest(t *testing.T, operation contract.Operation, fields map[string]string, files map[string][]byte) *InferenceRequest {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := w.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range files {
		part, err := w.CreateFormFile(name, "input.bin")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(value)
	}
	_ = w.Close()
	m, err := DecodeInferenceMultipart(body.Bytes(), w.FormDataContentType(), operation, MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Clear()
	logical := m.LogicalBody()
	defer clear(logical)
	r, err := m.Decode(logical, operation, MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Clear)
	return r
}

func TestInferenceRejectsPersistentReferencesBeforeDispatch(t *testing.T) {
	for _, extra := range []string{`"store":true`, `"background":true`, `"previous_response_id":"resp_other"`, `"conversation":"conv_other"`, `"prompt":{"id":"pmpt_other"}`, `"input":[{"type":"item_reference","id":"msg_other"}]`, `"input":[{"role":"user","content":[{"type":"input_file","file_id":"file_other"}]}]`, `"tools":[{"type":"file_search","vector_store_ids":["vs_other"]}]`, `"tools":[{"type":"code_interpreter","container":"auto"}]`} {
		body := `{"model":"m",` + extra + `}`
		if !strings.HasPrefix(extra, `"input"`) {
			body = `{"model":"m","input":"x",` + extra + `}`
		}
		if r, err := DecodeInferenceRequest(strings.NewReader(body), contract.OperationResponses, 0); err == nil {
			r.Clear()
			t.Errorf("accepted resource: %s", extra)
		}
	}
	valid := `{"model":"m","input":[{"role":"user","content":[{"type":"input_file","file_data":"data:application/pdf;base64,YQ==","filename":"x.pdf"}]},{"type":"reasoning","encrypted_content":"opaque"}],"store":false,"background":false,"tools":[{"type":"function","name":"f","parameters":{"type":"object","properties":{"file_id":{"type":"string"}}}}]}`
	r := inferenceRequestForTest(t, contract.OperationResponses, valid)
	if r.CheckTarget(contract.TypeAnthropicCompatible, contract.AttemptPolicy{}) == nil {
		t.Fatal("unsupported connector accepted")
	}
	for _, kind := range []contract.Type{contract.TypeOpenAICompatible, contract.TypeOpenAIResponses} {
		if r.CheckTarget(kind, contract.AttemptPolicy{}) != nil {
			t.Fatal("native response connector refused")
		}
	}
}

func TestInferenceAdapterJSONOperations(t *testing.T) {
	tests := []struct {
		operation         contract.Operation
		body, reply, path string
	}{
		{contract.OperationResponses, `{"model":"public/model","input":"hi","temperature":0.3}`, `{"id":"r1","object":"response","status":"completed","model":"private-model","output":[],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`, "/responses"},
		{contract.OperationResponsesCompact, `{"model":"public/model","input":[{"role":"user","content":"hi"}]}`, `{"id":"r1","object":"response.compaction","output":[{"type":"compaction","encrypted_content":"opaque"}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`, "/responses/compact"},
		{contract.OperationResponsesInputTokens, `{"model":"public/model","input":"hi"}`, `{"object":"response.input_tokens","input_tokens":42}`, "/responses/input_tokens"},
		{contract.OperationCompletions, `{"model":"public/model","prompt":"hi","max_tokens":5}`, `{"object":"text_completion","model":"private-model","choices":[{"index":0,"text":"hello","finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`, "/completions"},
		{contract.OperationImagesEdits, `{"model":"public/model","prompt":"edit","background":"transparent","images":[{"image_url":"data:image/png;base64,aW1hZ2U="}]}`, `{"created":0,"data":[{"b64_json":"aW1hZ2U="}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`, "/images/edits"},
		{contract.OperationModerations, `{"model":"public/model","input":[{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]}`, `{"id":"m1","model":"private-model","results":[{"flagged":false,"categories":{"violence":false},"category_scores":{"violence":0.01}}]}`, "/moderations"},
	}
	for _, test := range tests {
		t.Run(string(test.operation), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1"+test.path || r.Header.Get("Authorization") != "Bearer secret-material" {
					t.Error("wrong dispatch target")
				}
				var object map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&object) != nil || !bytes.Equal(object["model"], []byte(`"private-model"`)) {
					t.Error("model projection failed")
				}
				if test.operation == contract.OperationResponses && (!bytes.Equal(object["store"], []byte("false")) || !bytes.Equal(object["background"], []byte("false"))) {
					t.Error("stateless defaults absent")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, test.reply)
			}))
			defer server.Close()
			request := inferenceRequestForTest(t, test.operation, test.body)
			writer := &decisionCheckpoint{ResponseRecorder: httptest.NewRecorder()}
			result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), writer, NewTarget(server.URL+"/v1", "private-model", NewCredential([]byte("secret-material"), nil)), request, contract.TypeOpenAICompatible, contract.AttemptPolicy{})
			if !result.Success || !result.Committed || writer.started != 1 || writer.observed != 1 || strings.Contains(writer.Body.String(), "private-model") {
				t.Fatalf("result=%+v response=%s", result, writer.Body.String())
			}
			if test.operation == contract.OperationResponsesInputTokens && result.Usage.Present {
				t.Fatal("count was charged as consumption")
			}
		})
	}
}

func TestInferenceMultipartWireAndOwnedCleanup(t *testing.T) {
	for _, operation := range []contract.Operation{contract.OperationImagesEdits, contract.OperationImagesVariations, contract.OperationAudioTranscriptions, contract.OperationAudioTranslations} {
		t.Run(string(operation), func(t *testing.T) {
			fields := map[string]string{"model": "public/model"}
			fileField := "file"
			reply := `{"text":"hello","usage":{"type":"tokens","input_tokens":2,"output_tokens":3,"total_tokens":5}}`
			if operation == contract.OperationImagesEdits || operation == contract.OperationImagesVariations {
				fileField = "image"
				reply = `{"created":0,"data":[{"b64_json":"aW1hZ2U="}]}`
				if operation == contract.OperationImagesEdits {
					fields["prompt"] = "edit"
				}
			}
			request := uploadForTest(t, operation, fields, map[string][]byte{fileField: {0, 1, 2, 3}})
			clone := request.CloneForAttempt()
			request.Clear()
			defer clone.Clear()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if err != nil || media != "multipart/form-data" {
					t.Error("missing multipart")
					return
				}
				reader := multipart.NewReader(r.Body, params["boundary"])
				found := false
				model := ""
				for {
					part, err := reader.NextPart()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Error(err)
						break
					}
					raw, _ := io.ReadAll(part)
					if part.FormName() == fileField {
						found = bytes.Equal(raw, []byte{0, 1, 2, 3})
					}
					if part.FormName() == "model" {
						model = string(raw)
					}
				}
				if !found || model != "private-model" {
					t.Error("upload/model corrupted")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, reply)
			}))
			defer server.Close()
			writer := httptest.NewRecorder()
			result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), writer, NewTarget(server.URL+"/v1", "private-model", NewCredential([]byte("secret-material"), nil)), clone, contract.TypeOpenAICompatible, contract.AttemptPolicy{})
			if !result.Success {
				t.Fatalf("failed=%+v", result)
			}
		})
	}
}

func TestInferenceMultipartBoundsAndRepeatedFields(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("model", "m")
	_ = w.WriteField("model", "other")
	_ = w.Close()
	if m, err := DecodeInferenceMultipart(body.Bytes(), w.FormDataContentType(), contract.OperationAudioTranscriptions, MaxRequestBodyBytes); err == nil {
		m.Clear()
		t.Fatal("duplicate field accepted")
	}
	if m, err := DecodeInferenceMultipart(body.Bytes(), w.FormDataContentType(), contract.OperationAudioTranscriptions, 10); err == nil {
		m.Clear()
		t.Fatal("oversize accepted")
	}
	if m, err := DecodeInferenceMultipart(body.Bytes()[:body.Len()-12], w.FormDataContentType(), contract.OperationImagesEdits, MaxRequestBodyBytes); err == nil {
		m.Clear()
		t.Fatal("truncated multipart accepted")
	}
}

func TestInferenceBinaryAndTextResponses(t *testing.T) {
	for _, test := range []struct {
		media, body string
		operation   contract.Operation
	}{{"audio/mpeg", string([]byte{0xff, 0xfb, 0, 1, 2}), contract.OperationAudioSpeech}, {"text/plain", "translated text", contract.OperationAudioTranslations}, {"text/plain", "", contract.OperationAudioTranslations}} {
		t.Run(test.media, func(t *testing.T) {
			var request *InferenceRequest
			if test.operation == contract.OperationAudioSpeech {
				request = inferenceRequestForTest(t, test.operation, `{"model":"public/model","input":"hi","voice":"alloy"}`)
			} else {
				request = uploadForTest(t, test.operation, map[string]string{"model": "public/model", "response_format": "text"}, map[string][]byte{"file": {1, 2}})
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", test.media)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			writer := httptest.NewRecorder()
			result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), writer, NewTarget(server.URL, "private-model", NewCredential([]byte("secret-material"), nil)), request, contract.TypeOpenAICompatible, contract.AttemptPolicy{})
			if !result.Success || !result.Committed || writer.Body.String() != test.body || writer.Header().Get("Content-Type") != test.media || result.Usage.Present {
				t.Fatalf("result=%+v wire=%s", result, writer.Body.String())
			}
		})
	}
}

func TestInferenceRerankDialectsAndUsage(t *testing.T) {
	for _, test := range []struct {
		kind           contract.Type
		reply          string
		tokens, search int64
	}{
		{contract.TypeOpenAICompatible, `{"results":[{"index":1,"relevance_score":0.9}],"usage":{"total_tokens":7}}`, 7, 0},
		{contract.TypeJinaRerank, `{"results":[{"index":1,"relevance_score":0.9}],"usage":{"total_tokens":7}}`, 7, 0},
		{contract.TypeCohereRerank, `{"results":[{"index":1,"relevance_score":0.9}],"meta":{"billed_units":{"search_units":1.25}}}`, 0, 1250000},
		{contract.TypeSiliconFlowRerank, `{"results":[{"index":1,"relevance_score":0.9}],"tokens":{"input_tokens":7,"output_tokens":2}}`, 7, 0},
		{contract.TypeVoyageRerank, `{"data":[{"index":1,"relevance_score":0.9}],"usage":{"total_tokens":7}}`, 7, 0},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			request := inferenceRequestForTest(t, contract.OperationRerank, `{"model":"public/model","query":"q","documents":["first","second"],"top_n":1,"return_documents":true}`)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var object map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&object)
				if test.kind == contract.TypeVoyageRerank && (object["top_n"] != nil || string(object["top_k"]) != "1") {
					t.Error("Voyage conversion failed")
				}
				if test.kind == contract.TypeCohereRerank && object["return_documents"] != nil {
					t.Error("unsupported Cohere field sent")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, test.reply)
			}))
			defer server.Close()
			writer := httptest.NewRecorder()
			result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), writer, NewTarget(server.URL+"/v1", "private-model", NewCredential([]byte("secret-material"), nil)), request, test.kind, contract.AttemptPolicy{})
			if !result.Success || result.Usage.UncachedInputTokens != test.tokens || result.Usage.SearchUnitsMicros != test.search || !strings.Contains(writer.Body.String(), `"text":"second"`) {
				t.Fatalf("result=%+v body=%s", result, writer.Body.String())
			}
		})
	}
	request := inferenceRequestForTest(t, contract.OperationRerank, `{"model":"m","query":"q","documents":["d"]}`)
	for _, units := range []string{`null`, `-1`, `0.0000001`, `9223372036854775808`, `"1"`} {
		raw := []byte(`{"results":[{"index":0,"relevance_score":0.5}],"meta":{"billed_units":{"search_units":` + units + `}}}`)
		_, usage, err := projectRerankResponse(raw, request, contract.TypeCohereRerank)
		if err != nil || usage.SearchUnitsPresent {
			t.Fatalf("invalid units %s: %+v %v", units, usage, err)
		}
	}
}

func TestInferenceStreamsRequireProtocolCompletion(t *testing.T) {
	response := `{"id":"r1","object":"response","status":"completed","model":"private-model","output":[],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`
	for _, test := range []struct {
		operation    contract.Operation
		body, stream string
		valid        bool
	}{
		{contract.OperationResponses, `{"model":"public/model","input":"hi","stream":true}`, `data: {"type":"response.completed","response":` + response + "}\n\n", true},
		{contract.OperationResponses, `{"model":"public/model","input":"hi","stream":true}`, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n", false},
		{contract.OperationResponses, `{"model":"public/model","input":"hi","stream":true}`, "data: [DONE]\n\n", false},
		{contract.OperationCompletions, `{"model":"public/model","prompt":"hi","stream":true}`, "data: {\"choices\":[{\"index\":0,\"text\":\"hi\",\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", true},
		{contract.OperationCompletions, `{"model":"public/model","prompt":"hi","stream":true}`, "data: {\"choices\":[{\"index\":0,\"text\":\"hi\",\"finish_reason\":null}]}\n\ndata: [DONE]\n\n", false},
		{contract.OperationImagesEdits, `{"model":"public/model","prompt":"edit","images":[{"image_url":"data:image/png;base64,aW1hZ2U="}],"stream":true}`, "data: {\"type\":\"image_edit.completed\",\"b64_json\":\"aW1hZ2U=\"}\n\n", true},
		{contract.OperationAudioSpeech, `{"model":"public/model","input":"hi","voice":"alloy","stream_format":"sse"}`, "data: {\"type\":\"speech.audio.delta\",\"audio\":\"YXVkaW8=\"}\n\ndata: {\"type\":\"speech.audio.done\",\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"total_tokens\":3}}\n\n", true},
	} {
		t.Run(fmt.Sprintf("%s/%t/%d", test.operation, test.valid, len(test.stream)), func(t *testing.T) {
			request := inferenceRequestForTest(t, test.operation, test.body)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.stream)
			}))
			defer server.Close()
			writer := httptest.NewRecorder()
			result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), writer, NewTarget(server.URL, "private-model", NewCredential([]byte("secret-material"), nil)), request, contract.TypeOpenAICompatible, contract.AttemptPolicy{})
			if result.Success != test.valid || strings.Contains(writer.Body.String(), "private-model") {
				t.Fatalf("result=%+v wire=%s", result, writer.Body.String())
			}
		})
	}
}

func TestInferenceSecretReflectionRejectedWithoutCommit(t *testing.T) {
	for _, reply := range []string{`{"object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"secret-material"}]}]}`, `{"object":"response","status":"in_progress","output":[]}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, reply)
		}))
		request := inferenceRequestForTest(t, contract.OperationResponses, `{"model":"m","input":"hi"}`)
		writer := httptest.NewRecorder()
		result := adapterForServer(t, server.URL, nil, nil).AttemptInference(context.Background(), writer, NewTarget(server.URL, "private-model", NewCredential([]byte("secret-material"), nil)), request, contract.TypeOpenAICompatible, contract.AttemptPolicy{})
		server.Close()
		if result.Success || result.Committed || writer.Body.Len() != 0 {
			t.Fatalf("unsafe response escaped: %+v", result)
		}
	}
}

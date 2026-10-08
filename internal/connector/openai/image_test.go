package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func TestImageRequestBoundaryAndIsolation(t *testing.T) {
	for _, raw := range []string{`{}`, `{"model":"a/b"}`, `{"model":"a/b","prompt":null}`, `{"model":"a/b","prompt":""}`, `{"model":"a/b","prompt":"x","n":0}`, `{"model":"a/b","prompt":"x","n":11}`, `{"model":"a/b","prompt":"x","stream":"true"}`, `{"model":"a/b","prompt":"x","vendor":{"a":1,"a":2}}`} {
		if r, err := DecodeImageRequest(strings.NewReader(raw), 0); err == nil {
			r.Clear()
			t.Fatalf("accepted %s", raw)
		}
	}
	r, err := DecodeImageRequest(strings.NewReader(`{"model":"public/model","prompt":"test","n":2,"stream":true,"partial_images":3,"size":"1536x864","quality":"xhigh","user":"forged","vendor":{"a":1}}`), 0)
	if err != nil {
		t.Fatal(err)
	}
	clone := r.CloneForAttempt()
	r.Clear()
	defer clone.Clear()
	if !clone.Stream || clone.Count != 2 {
		t.Fatal("lost image metadata")
	}
	raw, err := clone.marshalUpstream("private/image", "anon_origin")
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || string(fields["model"]) != `"private/image"` || string(fields["user"]) != `"anon_origin"` || string(fields["vendor"]) != `{"a":1}` {
		t.Fatal("incorrect image projection")
	}
}

func TestImageAdapterResponseAndCompletedBoundary(t *testing.T) {
	const complete = `{"type":"image_generation.completed","b64_json":"aW1hZ2U=","usage":{"input_tokens":7,"output_tokens":11,"total_tokens":18}}`
	const partial = `{"type":"image_generation.partial_image","b64_json":"cGFydGlhbA==","partial_image_index":0}`
	for _, tc := range []struct {
		name, body      string
		stream, success bool
		input           int64
	}{
		{"base64", `{"created":0,"data":[{"b64_json":"aW1hZ2U="}],"usage":{"input_tokens":7,"output_tokens":11,"total_tokens":18},"private":"hidden"}`, false, true, 7},
		{"url", `{"created":0,"data":[{"url":"https://images.example/signed?expires=1","revised_prompt":"test"}]}`, false, true, 0},
		{"invalid base64", `{"created":0,"data":[{"b64_json":"invalid%"}]}`, false, false, 0},
		{"wrong count", `{"created":0,"data":[]}`, false, false, 0},
		{"completed", "event: image_generation.partial_image\ndata: " + partial + "\n\nevent: image_generation.completed\ndata: " + complete + "\n\n", true, true, 7},
		{"partial EOF", "data: " + partial + "\n\n", true, false, 0},
		{"done marker", "data: [DONE]\n\n", true, false, 0},
		{"mismatched event", "event: image_generation.partial_image\ndata: " + complete + "\n\n", true, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/images/generations" {
					t.Error("wrong path", r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer image-key-123" {
					t.Error("wrong credential")
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.stream {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			a := adapterForServer(t, server.URL, nil, nil)
			request, err := DecodeImageRequest(strings.NewReader(`{"model":"public/image","prompt":"test","stream":`+map[bool]string{true: "true", false: "false"}[tc.stream]+`}`), 0)
			if err != nil {
				t.Fatal(err)
			}
			defer request.Clear()
			w := httptest.NewRecorder()
			result := a.AttemptImage(context.Background(), w, testTarget(server.URL+"/v1", []byte("image-key-123"), []byte("encrypted-material")), request, contract.AttemptPolicy{SafetyIdentifier: "anon_origin"})
			if result.Success != tc.success || result.Usage.UncachedInputTokens != tc.input {
				t.Fatalf("result=%+v body=%s", result, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "hidden") {
				t.Fatal("vendor metadata leaked")
			}
		})
	}
}

func TestImagePayloadExceedsChatAndSSELimits(t *testing.T) {
	// Twelve MiB of valid base64 exceeds both the chat JSON and SSE line limits.
	encoded := strings.Repeat("aW1h", 3<<20)
	for _, stream := range []bool{false, true} {
		body := `{"created":0,"data":[{"b64_json":"` + encoded + `"}]}`
		media := "application/json"
		if stream {
			media = "text/event-stream"
			body = "event: image_generation.completed\ndata: " + `{"type":"image_generation.completed","b64_json":"` + encoded + `"}` + "\n\n"
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", media)
			io.WriteString(w, body)
		}))
		a := adapterForServer(t, server.URL, nil, nil)
		request, err := DecodeImageRequest(strings.NewReader(`{"model":"public/image","prompt":"test","stream":`+map[bool]string{true: "true", false: "false"}[stream]+`}`), 0)
		if err != nil {
			t.Fatal(err)
		}
		writer := httptest.NewRecorder()
		result := a.AttemptImage(context.Background(), writer, testTarget(server.URL, []byte("image-key-123"), nil), request, contract.AttemptPolicy{SafetyIdentifier: "anon_origin"})
		request.Clear()
		server.Close()
		if !result.Success {
			t.Fatalf("large image stream=%t: %+v", stream, result)
		}
	}
}

type cancelImageWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w *cancelImageWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(data)
	w.cancel()
	return n, err
}

func TestImageStreamCancellationAndLimits(t *testing.T) {
	for _, stream := range []bool{false, true} {
		media := "application/json"
		body := `{"created":0,"data":[{"b64_json":"` + strings.Repeat("aW1h", 1024) + `"}]}`
		if stream {
			media = "text/event-stream"
			body = "data: " + `{"type":"image_generation.completed","b64_json":"` + strings.Repeat("aW1h", 1024) + `"}` + "\n\n"
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", media)
			io.WriteString(w, body)
		}))
		a := adapterForServer(t, server.URL, func(c *AdapterConfig) { c.MaxImageResponseBytes = 1024 }, nil)
		request, _ := DecodeImageRequest(strings.NewReader(`{"model":"a/b","prompt":"test","stream":`+map[bool]string{true: "true", false: "false"}[stream]+`}`), 0)
		w := httptest.NewRecorder()
		result := a.AttemptImage(context.Background(), w, testTarget(server.URL, []byte("image-key-123"), nil), request, contract.AttemptPolicy{SafetyIdentifier: "anon_origin"})
		request.Clear()
		server.Close()
		if result.Success || result.Committed || w.Body.Len() != 0 {
			t.Fatalf("limit bypass stream=%t result=%+v", stream, result)
		}
	}
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: image_generation.partial_image\ndata: "+`{"type":"image_generation.partial_image","b64_json":"cGFydGlhbA==","partial_image_index":0}`+"\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	a := adapterForServer(t, server.URL, nil, nil)
	request, _ := DecodeImageRequest(strings.NewReader(`{"model":"a/b","prompt":"test","stream":true}`), 0)
	defer request.Clear()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &cancelImageWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	result := a.AttemptImage(ctx, w, testTarget(server.URL, []byte("image-key-123"), nil), request, contract.AttemptPolicy{SafetyIdentifier: "anon_origin"})
	if result.Success || result.Failure != FailureCanceled || !result.Committed {
		t.Fatalf("cancellation=%+v", result)
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream was not cancelled")
	}
}

func TestImageDiagnosticsRetainOnlyExplicitUpstreamErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body       string
		status, captures int
	}{
		{"success", `{"created":0,"data":[{"b64_json":"aW1hZ2U=","revised_prompt":"owner prompt"}]}`, 200, 0},
		{"malformed image", `{"created":0,"data":[{"b64_json":"image-contents"}]}`, 200, 0},
		{"HTTP error", `{"error":{"message":"capacity unavailable","code":"busy"}}`, 429, 1},
		{"protocol error", `{"error":{"message":"generation failed","code":"failed"}}`, 200, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			a := adapterForServer(t, server.URL, nil, nil)
			request, _ := DecodeImageRequest(strings.NewReader(`{"model":"a/b","prompt":"owner prompt"}`), 0)
			defer request.Clear()
			captured := 0
			ctx := upstreamerror.WithCapture(context.Background(), func(_ context.Context, event upstreamerror.Event) {
				captured++
				if string(event.Bytes()) != tc.body {
					t.Fatal("raw diagnostic changed")
				}
			})
			a.AttemptImage(ctx, httptest.NewRecorder(), testTarget(server.URL, []byte("image-key-123"), nil), request, contract.AttemptPolicy{SafetyIdentifier: "anon_origin"})
			if captured != tc.captures {
				t.Fatalf("diagnostic captures=%d want=%d", captured, tc.captures)
			}
		})
	}
}

func TestImageProjectionGuardsDecodedStringsAndUsage(t *testing.T) {
	request, _ := DecodeImageRequest(strings.NewReader(`{"model":"a/b","prompt":"test"}`), 0)
	defer request.Clear()
	raw := []byte(`{"created":0,"data":[{"url":"https://images.example/image\u002dkey-123"}],"usage":{"input_tokens":7,"output_tokens":11,"total_tokens":18,"input_tokens_details":{"image_tokens":2,"text_tokens":5,"vendor":"hidden"},"vendor":"hidden"}}`)
	projected, usage, _, err := projectImageResponse(raw, request, false)
	if err != nil || !usage.Present || strings.Contains(string(projected), "hidden") {
		t.Fatalf("usage projection=%s err=%v", projected, err)
	}
	guard := newResponseGuard([]byte("image-key-123"))
	defer guard.Clear()
	if !guard.containsImageProjection(projected, false) {
		t.Fatal("decoded credential string escaped")
	}
}

func TestImageStreamWaitsForAllRequestedImages(t *testing.T) {
	known := `{"type":"image_generation.completed","b64_json":"aW1hZ2U=","usage":{"input_tokens":7,"output_tokens":11,"total_tokens":18}}`
	unknown := `{"type":"image_generation.completed","b64_json":"aW1hZ2U="}`
	overflow := `{"type":"image_generation.completed","b64_json":"aW1hZ2U=","usage":{"input_tokens":9223372036854775807,"output_tokens":0,"total_tokens":9223372036854775807}}`
	for _, tc := range []struct {
		name           string
		events         []string
		success, known bool
	}{
		{"two completed", []string{known, known}, true, true},
		{"one completed then EOF", []string{known}, false, false},
		{"missing second usage", []string{known, unknown}, true, false},
		{"overflow total usage", []string{overflow, known}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, event := range tc.events {
					io.WriteString(w, "event: image_generation.completed\ndata: "+event+"\n\n")
				}
			}))
			defer server.Close()
			a := adapterForServer(t, server.URL, nil, nil)
			request, _ := DecodeImageRequest(strings.NewReader(`{"model":"a/b","prompt":"two images","n":2,"stream":true}`), 0)
			defer request.Clear()
			writer := httptest.NewRecorder()
			result := a.AttemptImage(context.Background(), writer, testTarget(server.URL, []byte("image-key-123"), nil), request, contract.AttemptPolicy{SafetyIdentifier: "anon_origin"})
			if result.Success != tc.success || result.Usage.Present != tc.known {
				t.Fatalf("result=%+v", result)
			}
			if got := strings.Count(writer.Body.String(), "event: image_generation.completed"); got != len(tc.events) {
				t.Fatalf("completed delivered=%d want=%d", got, len(tc.events))
			}
			if tc.known && (result.Usage.UncachedInputTokens != 14 || result.Usage.OutputTokens != 22) {
				t.Fatalf("usage=%+v", result.Usage)
			}
		})
	}
}

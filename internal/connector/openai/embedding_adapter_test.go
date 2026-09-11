package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

type embeddingCheckpointWriter struct {
	*httptest.ResponseRecorder
	marked    bool
	markError error
	cancel    context.CancelFunc
	failWrite bool
}

func (w *embeddingCheckpointWriter) MarkResponseStarted() error {
	if w.markError != nil {
		return w.markError
	}
	w.marked = true
	if w.cancel != nil {
		w.cancel()
	}
	return nil
}

func (w *embeddingCheckpointWriter) Write(body []byte) (int, error) {
	if !w.marked {
		return 0, errors.New("body before success checkpoint")
	}
	if w.failWrite {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(body)
}

func TestEmbeddingAdapterUsesSharedBoundaryAndSafeProjection(t *testing.T) {
	for _, encoding := range []string{"float", "base64"} {
		t.Run(encoding, func(t *testing.T) {
			var sent map[string]json.RawMessage
			var path, authorization string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path, authorization = r.URL.Path, r.Header.Get("Authorization")
				if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Set-Cookie", "private=value")
				w.Header().Set("X-Private-Header", "not-public")
				io.WriteString(w, embeddingResponse(encoding, `{"prompt_tokens":3,"total_tokens":3}`))
			}))
			defer server.Close()
			a := adapterForServer(t, server.URL, nil, nil)
			r, err := DecodeEmbeddingRequest(strings.NewReader(`{"model":"public/model","input":["one","two"],"encoding_format":"`+encoding+`","store":true,"user":"forged"}`), 0)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Clear()
			secret, cipher := []byte("sk-embedding-marker"), []byte("nbsec:embedding-cipher")
			writer := &embeddingCheckpointWriter{ResponseRecorder: httptest.NewRecorder()}
			result := a.AttemptEmbedding(context.Background(), writer, testTarget(server.URL+"/custom/v1", secret, cipher), r, connectorcontract.AttemptPolicy{SafetyIdentifier: "anon_origin", ForceStoreFalse: true, FlattenToolCalls: true})
			if !result.Success || !result.Committed || !writer.marked || result.Usage.UncachedInputTokens != 3 {
				t.Fatalf("result=%+v", result)
			}
			if path != "/custom/v1/embeddings" || authorization != "Bearer sk-embedding-marker" || string(sent["model"]) != `"upstream/model"` || string(sent["user"]) != `"anon_origin"` || string(sent["store"]) != "true" {
				t.Fatalf("incorrect request %s %+v", path, sent)
			}
			if _, exists := sent["safety_identifier"]; exists {
				t.Fatal("nonstandard safety field injected")
			}
			if !bytes.Equal(secret, make([]byte, len(secret))) || !bytes.Equal(cipher, make([]byte, len(cipher))) {
				t.Fatal("credential was retained")
			}
			for _, header := range []string{"Set-Cookie", "X-Private-Header", "Content-Length"} {
				if writer.Header().Get(header) != "" {
					t.Fatal("upstream header escaped")
				}
			}
			if strings.Contains(writer.Body.String(), "private/") || strings.Contains(writer.Body.String(), "not-public") {
				t.Fatal("upstream metadata escaped")
			}
		})
	}
}

func TestEmbeddingCheckpointAndFailureContract(t *testing.T) {
	valid := embeddingResponse("float", `{"prompt_tokens":3,"total_tokens":3}`)
	for _, tc := range []struct {
		name                         string
		status                       int
		body                         string
		markError, cancel, failWrite bool
		marked                       bool
		usage                        bool
	}{
		{"invalid", 200, strings.Replace(valid, `[0.25,-1.5]`, `[null,1]`, 1), false, false, false, false, false},
		{"reported_error", 200, `{"error":{"message":"unsupported model"}}`, false, false, false, false, false},
		{"upstream_error", 400, `{"error":{"message":"unsupported model","code":"bad_model"}}`, false, false, false, false, false},
		{"other_success_status", 201, valid, false, false, false, false, false},
		{"checkpoint_failure", 200, valid, true, false, false, false, true},
		{"cancel_after_checkpoint", 200, valid, false, true, false, true, true},
		{"write_failure", 200, valid, false, false, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			a := adapterForServer(t, server.URL, nil, nil)
			r, err := DecodeEmbeddingRequest(strings.NewReader(`{"model":"p/m","input":["a","b"]}`), 0)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Clear()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writer := &embeddingCheckpointWriter{ResponseRecorder: httptest.NewRecorder(), failWrite: tc.failWrite}
			if tc.markError {
				writer.markError = errors.New("checkpoint unavailable")
			}
			if tc.cancel {
				writer.cancel = cancel
			}
			result := a.AttemptEmbedding(ctx, writer, testTarget(server.URL, []byte("sk-test-marker"), nil), r, connectorcontract.AttemptPolicy{SafetyIdentifier: "anon_test"})
			if result.Success || writer.Body.Len() != 0 || writer.marked != tc.marked || result.Usage.Present != tc.usage {
				t.Fatalf("result=%+v marked=%v bytes=%d", result, writer.marked, writer.Body.Len())
			}
			if tc.cancel && result.Failure != FailureCanceled {
				t.Fatal("cancellation classification lost")
			}
		})
	}
}

func TestEmbeddingProtocolLimitCannotWidenSharedEgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, embeddingResponse("float", `null`))
	}))
	defer server.Close()
	a := adapterForServer(t, server.URL, func(c *AdapterConfig) { c.MaxEmbeddingResponseBytes = 1 << 40 }, func(s *egress.StackOptions) { s.MaxResponseBytes = 128 })
	if a.maxEmbeddingResponseBytes != 128 {
		t.Fatal("shared limit widened")
	}
	r, err := DecodeEmbeddingRequest(strings.NewReader(`{"model":"p/m","input":["a","b"]}`), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Clear()
	w := &embeddingCheckpointWriter{ResponseRecorder: httptest.NewRecorder()}
	result := a.AttemptEmbedding(context.Background(), w, testTarget(server.URL, []byte("sk-test-marker"), nil), r, connectorcontract.AttemptPolicy{SafetyIdentifier: "anon_test"})
	if result.Success || w.marked || w.Body.Len() != 0 {
		t.Fatal("oversize response checkpointed")
	}
}

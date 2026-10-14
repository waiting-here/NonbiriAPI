package openai

import (
	"context"
	"fmt"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMarkedAdaptationSuccessReflection(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, marked := range []bool{false, true} {
			for _, fromBody := range []bool{false, true} {
				t.Run(fmt.Sprintf("stream%v/marked%v/body%v", stream, marked, fromBody), func(t *testing.T) {
					const value = "configured-private-token"
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if stream {
							writeSSE(w, "data: "+strings.Replace(validChunk, `"ok"`, `"configured-private-token"`, 1)+"\n\n", "data: "+strings.Replace(validChunk, `"finish_reason":null`, `"finish_reason":"stop"`, 1)+"\n\n", "data: [DONE]\n\n")
						} else {
							w.Header().Set("Content-Type", "application/json")
							_, _ = io.WriteString(w, strings.Replace(validCompletion, `"ok"`, `"configured-private-token"`, 1))
						}
					}))
					defer server.Close()
					raw := fmt.Sprintf(`{"model":"p/m","messages":[{"role":"user","content":"hi"}],"stream":%v}`, stream)
					policy := contract.AttemptPolicy{HasAdaptation: true, SafetyIdentifier: "nbu_safe"}
					if fromBody {
						raw = strings.TrimSuffix(raw, "}") + `,"vendor_token":"configured-private-token"}`
					} else {
						policy.AdditionalHeaders = http.Header{"X-Token": {value}}
					}
					if marked {
						policy.ResponseSecrets = [][]byte{[]byte(value)}
					}
					out := httptest.NewRecorder()
					result := adapterForServer(t, server.URL, nil, nil).AttemptWithPolicy(context.Background(), out, testTarget(server.URL+"/v1", []byte("upstream-key"), nil), decodeAdapterRequest(t, raw), policy)
					if result.Success == marked || result.UpstreamStatus != http.StatusOK {
						t.Fatalf("wrong result: %+v", result)
					}
					if marked && (strings.Contains(out.Body.String(), value) || strings.Contains(result.Diagnostic, value)) {
						t.Fatal("protected value escaped")
					}
				})
			}
		}
	}
}

func TestMarkedAdaptationEmbeddingAndImageReflection(t *testing.T) {
	for _, operation := range []string{"embedding", "image"} {
		for _, marked := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/marked%v", operation, marked), func(t *testing.T) {
				value := "QUFB"
				body := strings.Replace(embeddingResponse("base64", `null`), "AACAPwAAAEA=", "QUFBQUFBQUE=", 1)
				if operation == "image" {
					value = "private-token"
					body = `{"created":0,"data":[{"url":"https://images.example/private-token"}]}`
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, body)
				}))
				defer server.Close()
				adapter := adapterForServer(t, server.URL, nil, nil)
				out := httptest.NewRecorder()
				policy := contract.AttemptPolicy{AdditionalHeaders: http.Header{"X-Token": {value}}, HasAdaptation: true, SafetyIdentifier: "nbu_safe"}
				if marked {
					policy.ResponseSecrets = [][]byte{[]byte(value)}
				}
				target := testTarget(server.URL+"/v1", []byte("upstream-key"), nil)
				var result AttemptResult
				if operation == "image" {
					request, err := DecodeImageRequest(strings.NewReader(`{"model":"p/m","prompt":"test"}`), 0)
					if err != nil {
						t.Fatal(err)
					}
					defer request.Clear()
					result = adapter.AttemptImage(context.Background(), out, target, request, policy)
				} else {
					request, err := DecodeEmbeddingRequest(strings.NewReader(`{"model":"p/m","input":["one","two"],"encoding_format":"base64"}`), MaxRequestBodyBytes)
					if err != nil {
						t.Fatal(err)
					}
					defer request.Clear()
					result = adapter.AttemptEmbedding(context.Background(), out, target, request, policy)
				}
				if result.Success == marked || result.UpstreamStatus != http.StatusOK || marked && (out.Body.Len() != 0 || result.Diagnostic != "upstream response was rejected") {
					t.Fatalf("wrong %s response boundary: %+v %s", operation, result, out.Body)
				}
			})
		}
	}
}

func TestMarkedAdaptationSplitStreamReflection(t *testing.T) {
	const value = "configured-secret-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		first := strings.Replace(validChunk, `"ok"`, `"configured-"`, 1)
		second := strings.Replace(validChunk, `"ok"`, `"secret-token"`, 1)
		writeSSE(w, "data: "+first+"\n\n", "data: "+second+"\n\n", "data: [DONE]\n\n")
	}))
	defer server.Close()
	out := httptest.NewRecorder()
	policy := contract.AttemptPolicy{AdditionalHeaders: http.Header{"X-Token": {value}}, ResponseSecrets: [][]byte{[]byte(value)}, HasAdaptation: true, SafetyIdentifier: "nbu_safe"}
	result := adapterForServer(t, server.URL, nil, nil).AttemptWithPolicy(context.Background(), out, testTarget(server.URL+"/v1", []byte("upstream-key"), nil), streamRequest(t), policy)
	if result.Success || result.UpstreamStatus != http.StatusOK || strings.Contains(out.Body.String(), "secret-token") || strings.Contains(out.Body.String(), "[DONE]") {
		t.Fatal("complete marked value or success escaped")
	}
}

func BenchmarkMarkedResponseGuard(b *testing.B) {
	for _, count := range []int{0, 1, 16} {
		for _, size := range []int{1024, 65536} {
			b.Run(fmt.Sprintf("markers%d/bytes%d", count, size), func(b *testing.B) {
				body := []byte(strings.Replace(validCompletion, `"ok"`, `"`+strings.Repeat("x", size)+`"`, 1))
				materials := [][]byte{[]byte("upstream-credential"), []byte("encrypted-credential")}
				for i := 0; i < count; i++ {
					materials = append(materials, []byte(fmt.Sprintf("private-token-%d", i)))
				}
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				for b.Loop() {
					guard := newResponseGuard(materials...)
					matched := guard.ContainsJSON(body, body)
					guard.Clear()
					if matched {
						b.Fatal("ordinary response rejected")
					}
				}
			})
		}
	}
}

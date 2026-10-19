package chatwire_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/connector/chatwire"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/gemini"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/connector/responses"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

type observedWriter struct {
	*httptest.ResponseRecorder
	headers, marks int
	physical       bool
	status         int
	refused        bool
}

func (w *observedWriter) ObserveUpstreamResponse(status int, physical bool, _ http.Header) error {
	w.headers++
	w.status = status
	w.physical = physical
	return nil
}
func (w *observedWriter) MarkResponseStarted() error {
	w.marks++
	if w.refused {
		return fmt.Errorf("checkpoint refused")
	}
	return nil
}

type resolver struct{}

func (resolver) LookupNetIP(ctx context.Context, _, _ string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, ctx.Err()
}
func outbound(t *testing.T, url string) backend.Backend {
	t.Helper()
	stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{url}, Resolver: resolver{}, RequestTimeout: 3 * time.Second, Concurrency: egress.ConcurrencyLimits{Global: 2, PerEndpoint: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if err := stack.AddSelfOrigins(context.Background(), &config.Config{SiteBaseURL: "https://gateway.example", UserHost: "gateway.example", AdminHost: "admin.gateway.example", ListenAddr: "127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.CloseIdleConnections)
	local, err := backend.NewLocal(stack)
	if err != nil {
		t.Fatal(err)
	}
	return local
}
func chat(t *testing.T, stream bool) *openai.ChatRequest {
	t.Helper()
	r, err := openai.DecodeChatRequest(strings.NewReader(fmt.Sprintf(`{"model":"public-model","messages":[{"role":"user","content":"hello"}],"stream":%t,"stream_options":{"include_usage":true}}`, stream)), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Clear)
	return r
}
func protocol(kind contract.Type) chatwire.Protocol {
	if kind == contract.TypeGoogleGemini {
		return chatwire.Protocol{Type: kind, Header: "X-Goog-Api-Key", Compile: gemini.Compile, URL: gemini.URL, Decoder: gemini.NewDecoder, LowercaseIdentity: true}
	}
	return chatwire.Protocol{Type: kind, Header: "Authorization", Compile: responses.Compile, URL: responses.URL, Decoder: responses.NewDecoder, ErrorEvent: responses.ErrorEvent}
}

const geminiJSON = `{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"totalTokenCount":12}}`
const responsesJSON = `{"id":"resp_private","status":"completed","output":[{"id":"msg_private","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]}],"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}`

func responseStream() string {
	events := []string{`{"type":"response.created","sequence_number":0,"response":{"id":"resp_private","status":"in_progress"}}`, `{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"msg_private","type":"message","role":"assistant","content":[]}}`, `{"type":"response.content_part.added","sequence_number":2,"output_index":0,"item_id":"msg_private","content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`, `{"type":"response.output_text.delta","sequence_number":3,"output_index":0,"item_id":"msg_private","content_index":0,"delta":"hello"}`, `{"type":"response.output_text.done","sequence_number":4,"output_index":0,"item_id":"msg_private","content_index":0,"text":"hello"}`, `{"type":"response.content_part.done","sequence_number":5,"output_index":0,"item_id":"msg_private","content_index":0,"part":{"type":"output_text","text":"hello","annotations":[]}}`, `{"type":"response.output_item.done","sequence_number":6,"output_index":0,"item":{"id":"msg_private","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]}}`, `{"type":"response.completed","sequence_number":7,"response":` + responsesJSON + `}`}
	var out string
	for _, event := range events {
		out += "data: " + event + "\n\n"
	}
	return out
}
func TestTranslatingHTTPCheckpointIdentityAndProtocolFailure(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeGoogleGemini, contract.TypeOpenAIResponses} {
		for _, stream := range []bool{false, true} {
			for _, mode := range []string{"valid", "truncated", "reflected", "marked", "checkpoint", "wrong-media", "error"} {
				t.Run(fmt.Sprintf("%s/%t/%s", kind, stream, mode), func(t *testing.T) {
					var received atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						received.Add(1)
						body, _ := io.ReadAll(r.Body)
						var root map[string]json.RawMessage
						if json.Unmarshal(body, &root) != nil {
							t.Error("bad request JSON")
						}
						if r.Header.Get("X-Fixed") != "configured" {
							t.Error("missing adaptation header")
						}
						if kind == contract.TypeGoogleGemini {
							if r.Header.Get("X-Goog-Api-Key") != "upstream-secret" || r.Header.Get("Authorization") != "" || !strings.Contains(string(body), "nbu_consumer") || !strings.HasPrefix(r.URL.Path, "/v1beta/models/private-model:") {
								t.Errorf("gemini wire %s %s", r.URL, string(body))
							}
						} else {
							if r.Header.Get("Authorization") != "Bearer upstream-secret" || r.URL.Path != "/v1beta/responses" || string(root["store"]) != "false" || string(root["model"]) != `"private-model"` {
								t.Errorf("responses wire %s %s", r.URL, string(body))
							}
						}
						media := "application/json"
						reply := responsesJSON
						if kind == contract.TypeGoogleGemini {
							reply = geminiJSON
						}
						if stream {
							media = "text/event-stream"
							reply = responseStream()
							if kind == contract.TypeGoogleGemini {
								reply = "data: " + geminiJSON + "\n\n"
							}
						}
						switch mode {
						case "truncated":
							if stream {
								if kind == contract.TypeGoogleGemini {
									reply = "data: " + strings.Replace(geminiJSON, `,"finishReason":"STOP"`, "", 1) + "\n\n"
								} else {
									reply = strings.Split(reply, `data: {"type":"response.output_text.done"`)[0]
								}
							} else {
								reply = reply[:len(reply)-1]
							}
						case "reflected":
							reply = strings.ReplaceAll(reply, "hello", "upstream-secret")
						case "marked":
							reply = strings.ReplaceAll(reply, "hello", "marked-value")
						case "wrong-media":
							media = "text/plain"
						case "error":
							reply = `{"error":{"message":"failed"}}`
							if stream {
								reply = "data: " + reply + "\n\n"
							}
						}
						w.Header().Set("Content-Type", media)
						_, _ = io.WriteString(w, reply)
					}))
					defer server.Close()
					adapter, err := chatwire.New(outbound(t, server.URL), protocol(kind))
					if err != nil {
						t.Fatal(err)
					}
					writer := &observedWriter{ResponseRecorder: httptest.NewRecorder(), refused: mode == "checkpoint"}
					result := adapter.Attempt(context.Background(), writer, contract.NewTarget(kind, server.URL+"/v1beta", "private-model"), contract.NewShortLivedSecret([]byte("upstream-secret"), []byte("ciphertext-secret")), chat(t, stream), contract.AttemptPolicy{SafetyIdentifier: "NBU_CONSUMER", AdditionalHeaders: http.Header{"X-Fixed": []string{"configured"}, "X-Marked": []string{"marked-value"}}, ResponseSecrets: [][]byte{[]byte("marked-value")}})
					if received.Load() != 1 || writer.headers != 1 || writer.status != 200 || writer.physical != stream {
						t.Fatalf("checkpoint headers=%+v result=%+v", writer, result)
					}
					if result.Success != (mode == "valid") {
						t.Fatalf("result %+v, body %s", result, writer.Body.String())
					}
					if strings.Contains(writer.Body.String(), "upstream-secret") || strings.Contains(writer.Body.String(), "marked-value") || strings.Contains(writer.Body.String(), "private-model") || strings.Contains(writer.Body.String(), "resp_private") {
						t.Fatal("source identity escaped")
					}
					if mode == "valid" && (writer.marks == 0 || !result.Usage.Present || result.Usage.OutputTokens != 2 || result.StreakDisposition != contract.StreakSuccess) {
						t.Fatalf("valid %+v marks=%d", result, writer.marks)
					}
					if !stream && mode != "valid" && writer.Body.Len() != 0 {
						t.Fatal("invalid JSON committed")
					}
					if mode == "truncated" && stream && (!result.Committed || strings.Contains(writer.Body.String(), "[DONE]") || result.StreakDisposition == contract.StreakSuccess) {
						t.Fatalf("truncated stream %+v %s", result, writer.Body.String())
					}
				})
			}
		}
	}
}
func TestTranslatingCanceledStreamClosesUpstream(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeGoogleGemini, contract.TypeOpenAIResponses} {
		t.Run(string(kind), func(t *testing.T) {
			started := make(chan struct{})
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, ": waiting\n\n")
				w.(http.Flusher).Flush()
				close(started)
				<-r.Context().Done()
				close(closed)
			}))
			defer server.Close()
			adapter, _ := chatwire.New(outbound(t, server.URL), protocol(kind))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan contract.AttemptResult, 1)
			go func() {
				done <- adapter.Attempt(ctx, httptest.NewRecorder(), contract.NewTarget(kind, server.URL, "private-model"), contract.NewShortLivedSecret([]byte("upstream-secret"), nil), chat(t, true), contract.AttemptPolicy{})
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream did not start")
			}
			cancel()
			select {
			case result := <-done:
				if result.Success || result.FailureOrigin != contract.OriginClientCancel || result.StreakDisposition != contract.StreakNeutral {
					t.Fatal(result)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("attempt did not cancel")
			}
			select {
			case <-closed:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream body stayed open")
			}
		})
	}
}

func TestResponsesExplicitStreamErrorsPreserveSafeDetailAndPrivateCapture(t *testing.T) {
	for _, envelope := range []string{
		`{"type":"error","sequence_number":1,"code":"server_error","message":"provider failed"}`,
		`{"type":"response.failed","sequence_number":1,"response":{"id":"r","status":"failed","error":{"code":"server_error","message":"provider failed"}}}`,
	} {
		for _, mode := range []string{"plain", "adapted", "secret"} {
			t.Run(envelope[:20]+"/"+mode, func(t *testing.T) {
				reply := envelope
				if mode == "secret" {
					reply = strings.Replace(reply, "provider failed", "upstream-secret", 1)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: "+reply+"\n\n")
				}))
				defer server.Close()
				a, _ := chatwire.New(outbound(t, server.URL), protocol(contract.TypeOpenAIResponses))
				var captured string
				ctx := upstreamerror.WithCapture(context.Background(), func(_ context.Context, event upstreamerror.Event) { captured = string(event.Bytes()) })
				w := &observedWriter{ResponseRecorder: httptest.NewRecorder()}
				result := a.Attempt(ctx, w, contract.NewTarget(contract.TypeOpenAIResponses, server.URL, "private-model"), contract.NewShortLivedSecret([]byte("upstream-secret"), nil), chat(t, true), contract.AttemptPolicy{HasAdaptation: mode == "adapted"})
				if result.Success || result.Committed || captured != reply || w.Body.Len() != 0 || w.headers != 1 {
					t.Fatalf("result=%+v capture=%q body=%s", result, captured, w.Body)
				}
				if mode == "plain" && (result.FailureOrigin != contract.OriginUpstreamResponse || result.ErrorDetail.Code() != "server_error" || result.ErrorDetail.Message() != "provider failed") {
					t.Fatalf("lost typed error: %+v %s", result, result.ErrorDetail.Message())
				}
				if mode != "plain" && result.ErrorDetail != (upstreamerror.Detail{}) {
					t.Fatal("sensitive detail escaped")
				}
			})
		}
	}
}

type cappedBackend struct {
	backend.Backend
	limit int64
}

func (b cappedBackend) MaxResponseBytes() int64 { return b.limit }

func TestTranslatingStreamsEnforceCumulativeBudgetWithoutSuccessTerminal(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeGoogleGemini, contract.TypeOpenAIResponses} {
		t.Run(string(kind), func(t *testing.T) {
			reply := strings.Repeat("data: "+strings.Replace(geminiJSON, `,"finishReason":"STOP"`, "", 1)+"\n\n", 50)
			if kind == contract.TypeOpenAIResponses {
				reply = responseStream() + strings.Repeat(": padding\n\n", 500)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, reply)
			}))
			defer server.Close()
			const limit = 2048
			a, _ := chatwire.New(cappedBackend{outbound(t, server.URL), limit}, protocol(kind))
			w := httptest.NewRecorder()
			result := a.Attempt(context.Background(), w, contract.NewTarget(kind, server.URL, "private-model"), contract.NewShortLivedSecret([]byte("upstream-secret"), nil), chat(t, true), contract.AttemptPolicy{})
			if result.Success || result.StreakDisposition == contract.StreakSuccess || w.Body.Len() > limit || strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatalf("result=%+v bytes=%d", result, w.Body.Len())
			}
		})
	}
}

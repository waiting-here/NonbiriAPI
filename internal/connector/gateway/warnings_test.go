package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func TestProviderWarningsAreAdvisoryForBothResponseModes(t *testing.T) {
	for _, warning := range []string{
		`[]`,
		`[{"type":"unsupported","feature":"temperature","details":"provider/private-model ignores this setting"}]`,
		`[{"type":"compatibility","feature":"system","details":"using compatibility mode"}]`,
		`[{"type":"other","message":"sk-test-credential-long"}]`,
		`[{"type":"unsupported","feature":"topP"},{"type":"other","message":"informational"}]`,
	} {
		for _, stream := range []bool{false, true} {
			t.Run(warning+map[bool]string{true: "/stream", false: "/json"}[stream], func(t *testing.T) {
				body, mime := strings.Replace(chatOK, `"warnings":[]`, `"warnings":`+warning, 1), "application/json"
				raw := prompt
				if stream {
					parts := append([]string{}, textParts...)
					parts[0] = `{"type":"stream-start","warnings":` + warning + `}`
					body, mime = sse(parts...), "text/event-stream"
					raw = strings.TrimSuffix(prompt, "}") + `,"stream":true,"stream_options":{"include_usage":true}}`
				}
				b := &fakeBackend{do: func(*http.Request) (*http.Response, error) { return response(body, mime), nil }}
				a, _ := NewAdapter(b)
				captured := 0
				ctx := upstreamerror.WithCapture(context.Background(), func(context.Context, upstreamerror.Event) { captured++ })
				w := httptest.NewRecorder()
				got := a.Attempt(ctx, w, target(), secret(), chat(t, raw), nil, "")
				if !got.Success || !got.Usage.Present || captured != 0 || !strings.Contains(w.Body.String(), "Hello") {
					t.Fatalf("result=%+v captured=%d body=%s", got, captured, w.Body)
				}
				if strings.Contains(w.Body.String(), "private-model") || strings.Contains(w.Body.String(), "sk-test") || strings.Contains(w.Body.String(), "warnings") {
					t.Fatal("provider warning leaked to caller")
				}
			})
		}
	}
}

func TestInvalidWarningsRemainProtocolFailuresWithPrivateDetails(t *testing.T) {
	for _, warning := range []string{`null`, `{}`, `"hint"`, `[null]`, `[{}]`, `[{"type":"other"}]`, `[{"type":"other","message":2}]`, `[{"type":"unsupported","feature":42}]`, `[{"type":"unsupported","feature":"x","details":false}]`, `[{"type":"future","message":"hint"}]`, `[{"type":"other","type":"unsupported","message":"hint"}]`} {
		t.Run(warning, func(t *testing.T) {
			parts := append([]string{}, textParts...)
			parts[0] = `{"type":"stream-start","warnings":` + warning + `}`
			b := &fakeBackend{do: func(*http.Request) (*http.Response, error) { return response(sse(parts...), "text/event-stream"), nil }}
			a, _ := NewAdapter(b)
			var private []byte
			count := 0
			ctx := upstreamerror.WithCapture(context.Background(), func(_ context.Context, event upstreamerror.Event) {
				count++
				private = append([]byte(nil), event.Bytes()...)
			})
			w := httptest.NewRecorder()
			result := a.Attempt(ctx, w, target(), secret(), chat(t, strings.TrimSuffix(prompt, "}")+`,"stream":true}`), nil, "")
			var detail struct{ Error, Event string }
			var record map[string]json.RawMessage
			_ = json.Unmarshal(private, &record)
			_ = json.Unmarshal(record["error"], &detail.Error)
			_ = json.Unmarshal(record["rejected_event"], &detail.Event)
			if result.Success || result.Committed || count != 1 || detail.Error != result.Diagnostic || detail.Event != parts[0] || w.Body.Len() != 0 {
				t.Fatalf("result=%+v count=%d detail=%s caller=%s", result, count, private, w.Body)
			}
			if _, _, err := translateChat([]byte(strings.Replace(chatOK, `"warnings":[]`, `"warnings":`+warning, 1)), "public", 1); err == nil {
				t.Fatal("invalid nonstream warnings accepted")
			}
		})
	}
}

func TestStreamFailureDiagnosticsDistinguishOrderingAndIncompleteOutput(t *testing.T) {
	for _, tc := range []struct{ name, body, reason, rejected string }{
		{"duplicate-start", sse(textParts[0], textParts[0]), "start was out of order", textParts[0]},
		{"late-start", sse(textParts[1], textParts[0]), "start was out of order", textParts[0]},
		{"incomplete", sse(textParts[:4]...), "ended without a finish", ""},
		{"unsupported-event", sse(`{"type":"future-event","private":"extra detail"}`), "unsupported event", `{"type":"future-event","private":"extra detail"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &fakeBackend{do: func(*http.Request) (*http.Response, error) { return response(tc.body, "text/event-stream"), nil }}
			a, _ := NewAdapter(b)
			var private []byte
			ctx := upstreamerror.WithCapture(context.Background(), func(_ context.Context, e upstreamerror.Event) { private = append([]byte(nil), e.Bytes()...) })
			w := httptest.NewRecorder()
			got := a.Attempt(ctx, w, target(), secret(), chat(t, strings.TrimSuffix(prompt, "}")+`,"stream":true}`), nil, "")
			var record struct {
				Error string `json:"error"`
				Event string `json:"rejected_event"`
			}
			if json.Unmarshal(private, &record) != nil || got.Success || !strings.Contains(record.Error, tc.reason) || record.Event != tc.rejected || strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatalf("result=%+v private=%s caller=%s", got, private, w.Body)
			}
			if strings.Contains(string(private), "Hello") || strings.Contains(w.Body.String(), "extra detail") {
				t.Fatal("successful content was captured or private failure detail exposed")
			}
		})
	}
}

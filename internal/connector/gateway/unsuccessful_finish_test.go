package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

const failedFinishUsage = `{"inputTokens":{"total":33802,"noCache":33802,"cacheRead":0},"outputTokens":{"total":4585,"text":0,"reasoning":4585}}`

func TestUnsuccessfulFinishPreservesConsumedUsage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, reason := range []string{"other", "error"} {
			raw := `{"content":[],"finishReason":{"unified":"` + reason + `","raw":"MALFORMED_RESPONSE"},"usage":` + failedFinishUsage + `}`
			kind := "application/json"
			if stream {
				raw = sse(`{"type":"stream-start","warnings":[]}`, `{"type":"finish","finishReason":{"unified":"`+reason+`","raw":"MALFORMED_RESPONSE"},"usage":`+failedFinishUsage+`}`)
				kind = "text/event-stream"
			}
			b := &fakeBackend{do: func(*http.Request) (*http.Response, error) { return response(raw, kind), nil }}
			a, _ := NewAdapter(b)
			w := httptest.NewRecorder()
			request := prompt
			if stream {
				request = strings.TrimSuffix(prompt, "}") + `,"stream":true}`
			}
			req := chat(t, request)
			res := a.Attempt(context.Background(), w, target(), secret(), req, nil, "")
			req.Clear()
			if res.Success || res.Committed || res.Failure != contract.FailureUpstream || res.ClientStatus != 0 || w.Body.Len() != 0 || !res.Usage.Present || res.Usage.UncachedInputTokens != 33802 || res.Usage.OutputTokens != 4585 || res.Usage.CacheWriteInputTokens != 0 || res.Diagnostic != unsuccessfulFinishDiagnostic || res.ErrorDetail.Message() != "MALFORMED_RESPONSE" {
				t.Fatalf("stream=%v reason=%s: %+v; detail=%s", stream, reason, res, res.ErrorDetail.Message())
			}
		}
	}
}

func TestFailedFinishMissingAndInvalidUsageRemainUnknown(t *testing.T) {
	for _, usage := range []string{"null", `{}`, `{"inputTokens":{"total":3},"outputTokens":{"total":-1}}`} {
		for _, stream := range []bool{false, true} {
			raw := `{"content":[],"finishReason":{"unified":"other","raw":"UNKNOWN"},"usage":` + usage + `}`
			kind := "application/json"
			if stream {
				raw = sse(`{"type":"finish","finishReason":{"unified":"other","raw":"UNKNOWN"},"usage":` + usage + `}`)
				kind = "text/event-stream"
			}
			b := &fakeBackend{do: func(*http.Request) (*http.Response, error) { return response(raw, kind), nil }}
			a, _ := NewAdapter(b)
			w := httptest.NewRecorder()
			request := prompt
			if stream {
				request = strings.TrimSuffix(prompt, "}") + `,"stream":true}`
			}
			req := chat(t, request)
			res := a.Attempt(context.Background(), w, target(), secret(), req, nil, "")
			req.Clear()
			if res.Success || res.Committed || res.Usage.Present || res.ClientStatus != 0 {
				t.Fatal(stream, usage, res)
			}
		}
	}
}

func TestUnsuccessfulFinishAfterOutputKeepsFailureAndSafeDetail(t *testing.T) {
	for _, rawReason := range []string{`"MALFORMED_RESPONSE"`, `"sk-test-credential-long https://upstream.example provider/private-model"`, `{"message":"bad shape"}`} {
		parts := append([]string{}, textParts[:4]...)
		parts = append(parts, `{"type":"finish","finishReason":{"unified":"other","raw":`+rawReason+`},"usage":`+failedFinishUsage+`}`)
		b := &fakeBackend{do: func(*http.Request) (*http.Response, error) { return response(sse(parts...), "text/event-stream"), nil }}
		a, _ := NewAdapter(b)
		w := httptest.NewRecorder()
		req := chat(t, strings.TrimSuffix(prompt, "}")+`,"stream":true}`)
		res := a.Attempt(context.Background(), w, target(), secret(), req, nil, "")
		req.Clear()
		if res.Success || !res.Committed || !res.Usage.Present || res.Usage.OutputTokens != 4585 || res.ClientStatus != 200 || strings.Contains(w.Body.String(), "[DONE]") || !strings.Contains(w.Body.String(), `"error"`) {
			t.Fatal(res, w.Body.String())
		}
		if strings.Contains(rawReason, "bad shape") {
			if res.Diagnostic != "upstream finish was invalid" {
				t.Fatal(res)
			}
		} else if res.Diagnostic != unsuccessfulFinishDiagnostic {
			t.Fatal(res)
		}
		for _, secret := range []string{"sk-test-credential-long", "upstream.example", "provider/private-model"} {
			if strings.Contains(res.ErrorDetail.Message(), secret) || strings.Contains(w.Body.String(), secret) {
				t.Fatal("source escaped", res.ErrorDetail.Message(), w.Body.String())
			}
		}
	}
}

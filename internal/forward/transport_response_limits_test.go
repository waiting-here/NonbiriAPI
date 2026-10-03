package forward

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
)

func transportLimitCompletion(t *testing.T, content string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"id": "c1", "object": "chat.completion", "created": 1, "model": "m",
		"choices": []any{map[string]any{
			"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 3, "total_tokens": 23},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func assertTransportFrameBudgets(t *testing.T, frames [][]byte) {
	t.Helper()
	total := int64(0)
	for _, frame := range frames {
		total += int64(len(frame))
		if !utf8.Valid(frame) || len(frame) > openai.DefaultMaxSSEEventBytes {
			t.Fatalf("invalid UTF-8 or oversized event: %d bytes", len(frame))
		}
		for _, line := range bytes.Split(frame, []byte("\n")) {
			if len(line) > openai.DefaultMaxSSELineBytes {
				t.Fatalf("oversized SSE line: %d bytes", len(line))
			}
		}
	}
	if total > openai.DefaultMaxStreamBytes {
		t.Fatalf("stream exceeded cumulative limit: %d", total)
	}
}

func canonicalTransportJSON(t *testing.T, raw []byte) string {
	t.Helper()
	value, err := readWireObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestTransportLargeCompletionFragmentsPreserveChoicesAndFields(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal(transportLimitCompletion(t, ""), &root); err != nil {
		t.Fatal(err)
	}
	logprobs := make([]any, 6000)
	for i := range logprobs {
		logprobs[i] = map[string]any{"token": strings.Repeat("x", 64), "logprob": -1, "bytes": []any{120}}
	}
	root["choices"] = []any{
		map[string]any{
			"index": 0,
			"message": map[string]any{
				"role":              "assistant",
				"content":           strings.Repeat("abcdef", 350000) + strings.Repeat("🙂<&\n\"", 50000),
				"reasoning_content": strings.Repeat("思考", 50000),
				"refusal":           strings.Repeat("拒绝", 50000),
				"tool_calls": []any{
					map[string]any{"id": "tool_1", "type": "function", "function": map[string]any{"name": "lookup", "arguments": `{"value":"` + strings.Repeat("x", 800000) + `"}`}},
					map[string]any{"id": "tool_2", "type": "function", "function": map[string]any{"name": "second", "arguments": `{"n":9007199254740993}`}},
				},
			},
			"finish_reason": "tool_calls", "logprobs": map[string]any{"content": logprobs},
		},
		map[string]any{
			"index": 1,
			"message": map[string]any{
				"role": "assistant", "content": nil,
				"reasoning":     strings.Repeat("reason", 100000),
				"function_call": map[string]any{"name": "legacy", "arguments": `{"value":"` + strings.Repeat("y", 300000) + `"}`},
			},
			"finish_reason": "function_call",
		},
	}
	raw, err := json.Marshal(root)
	if err != nil || int64(len(raw)) > openai.DefaultMaxJSONResponseBytes {
		t.Fatalf("fixture is not within JSON budget: %d %v", len(raw), err)
	}
	frames, err := completionToStream(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) < 10 {
		t.Fatal("large response was not fragmented")
	}
	assertTransportFrameBudgets(t, frames)
	if string(frames[len(frames)-1]) != "data: [DONE]\n\n" {
		t.Fatal("missing final DONE")
	}
	usageFrame, err := readWireObject(bytes.TrimSpace(bytes.TrimPrefix(frames[len(frames)-2], []byte("data: "))))
	if err != nil || len(usageFrame["choices"].([]any)) != 0 || usageFrame["usage"] == nil {
		t.Fatal("usage must follow finish events and precede DONE")
	}
	body, err := streamToCompletion(bytes.Join(frames, nil))
	if err != nil {
		t.Fatal(err)
	}
	if canonicalTransportJSON(t, raw) != canonicalTransportJSON(t, body) {
		t.Fatal("fragmented roundtrip changed choices, tools, reasoning, refusal, logprobs or usage")
	}
}

func TestTransportCompletionHonorsJSONAndSSELimits(t *testing.T) {
	raw := transportLimitCompletion(t, strings.Repeat("x", 2<<20))
	for _, includeUsage := range []bool{false, true} {
		t.Run(fmt.Sprintf("include_usage=%t", includeUsage), func(t *testing.T) {
			frames, err := completionToStream(raw, includeUsage)
			if err != nil {
				t.Fatal(err)
			}
			assertTransportFrameBudgets(t, frames)
			body, err := streamToCompletion(bytes.Join(frames, nil))
			if err != nil {
				t.Fatal(err)
			}
			root, err := readWireObject(body)
			if err != nil {
				t.Fatal(err)
			}
			if (root["usage"] != nil) != includeUsage {
				t.Fatal("caller usage choice changed")
			}
		})
	}
	nearLimit := transportLimitCompletion(t, "small")
	nearLimit = append(nearLimit, bytes.Repeat([]byte(" "), int(openai.DefaultMaxJSONResponseBytes)-len(nearLimit))...)
	if _, err := completionToStream(nearLimit, false); err != nil {
		t.Fatalf("exact JSON limit was rejected: %v", err)
	}
	if frames, err := completionToStream(append(nearLimit, ' '), false); err == nil || len(frames) != 0 {
		t.Fatal("over-limit JSON conversion succeeded")
	}
	root, err := readWireObject(transportLimitCompletion(t, "small"))
	if err != nil {
		t.Fatal(err)
	}
	root["unrepresentable_metadata"] = strings.Repeat("x", openai.DefaultMaxSSELineBytes)
	metadata, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if frames, err := completionToStream(metadata, false); err == nil || len(frames) != 0 {
		t.Fatal("unsplittable oversized metadata was emitted")
	}
}

func transportLimitStream(t *testing.T, contentBytes int) []byte {
	t.Helper()
	var stream bytes.Buffer
	for contentBytes > 0 {
		size := min(contentBytes, 32<<10)
		body, err := json.Marshal(map[string]any{
			"id": "c1", "object": "chat.completion.chunk", "created": 1, "model": "m",
			"choices": []any{map[string]any{
				"index": 0, "delta": map[string]any{"content": strings.Repeat("x", size)}, "finish_reason": nil,
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		stream.WriteString("data: ")
		stream.Write(body)
		stream.WriteString("\n\n")
		contentBytes -= size
	}
	stream.WriteString("data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	return stream.Bytes()
}

func confirmedTransportResult() contract.AttemptResult {
	return contract.ProtocolSucceeded(contract.AttemptResult{
		Success: true, Committed: true, UpstreamStatus: 200, ClientStatus: 200,
		Usage: contract.Usage{Present: true, UncachedInputTokens: 20, OutputTokens: 3},
	})
}

func TestTransportFinalJSONLimitKeepsConfirmedUpstreamAccounting(t *testing.T) {
	buffer := newTransportResponse(true)
	defer buffer.clear()
	stream := transportLimitStream(t, int(openai.DefaultMaxJSONResponseBytes))
	if int64(len(stream)) > openai.DefaultMaxStreamBytes {
		t.Fatal("fixture exceeds upstream stream limit")
	}
	if _, err := buffer.Write(stream); err != nil {
		t.Fatal(err)
	}
	sink := httptest.NewRecorder()
	result := buffer.complete(context.Background(), sink, false, confirmedTransportResult())
	if result.Success || result.Committed || result.ClientStatus != http.StatusBadGateway || sink.Body.Len() != 0 {
		t.Fatalf("oversized JSON reached caller: %+v bytes=%d", result, sink.Body.Len())
	}
	outcome := attemptOutcome(result)
	if !outcome.ProtocolSuccess || outcome.StreakDisposition != contract.StreakSuccess || outcome.FailureOrigin != contract.OriginNone || outcome.Usage.OutputTokens != 3 || !outcome.Usage.Present {
		t.Fatalf("local conversion failure lost confirmed upstream facts: %+v", outcome)
	}
}

func TestTransportContextFailureSeparatesDeliveryFromUpstreamOutcome(t *testing.T) {
	for _, upstreamStream := range []bool{false, true} {
		for _, expired := range []bool{false, true} {
			t.Run(fmt.Sprintf("upstream_stream=%t/deadline=%t", upstreamStream, expired), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				if expired {
					cancel()
					ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				} else {
					cancel()
				}
				defer cancel()
				buffer := newTransportResponse(upstreamStream)
				defer buffer.clear()
				if upstreamStream {
					_, _ = buffer.Write(transportLimitStream(t, 1))
				} else {
					_, _ = buffer.Write(transportLimitCompletion(t, "small"))
				}
				sink := httptest.NewRecorder()
				result := buffer.complete(ctx, sink, false, confirmedTransportResult())
				if sink.Body.Len() != 0 || result.Committed || result.Success {
					t.Fatalf("ended request reached caller: %+v", result)
				}
				if expired {
					if result.Failure != contract.FailureUpstream || result.ClientStatus != 504 || !isTimeoutResult(result) {
						t.Fatalf("execution deadline was classified as disconnect: %+v", result)
					}
				} else if result.Failure != contract.FailureCanceled || result.ClientStatus != 0 {
					t.Fatalf("caller cancellation was classified as timeout: %+v", result)
				}
				outcome := attemptOutcome(result)
				if !outcome.ProtocolSuccess || outcome.StreakDisposition != contract.StreakSuccess || outcome.FailureOrigin != contract.OriginNone || outcome.Usage.OutputTokens != 3 {
					t.Fatalf("delivery interruption changed upstream result: %+v", outcome)
				}
			})
		}
	}
}

type transportDeadlineSink struct {
	*httptest.ResponseRecorder
	ctx       context.Context
	deadlines []time.Time
	waitFirst bool
}

func (sink *transportDeadlineSink) SetWriteDeadline(deadline time.Time) error {
	sink.deadlines = append(sink.deadlines, deadline)
	return nil
}

func (sink *transportDeadlineSink) Write(body []byte) (int, error) {
	n, err := sink.ResponseRecorder.Write(body)
	if sink.waitFirst {
		sink.waitFirst = false
		<-sink.ctx.Done()
	}
	return n, err
}

func TestTransportDeliveryHonorsRemainingExecutionBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	buffer := newTransportResponse(true)
	defer buffer.clear()
	_, _ = buffer.Write(transportLimitStream(t, 1))
	sink := &transportDeadlineSink{ResponseRecorder: httptest.NewRecorder(), ctx: ctx}
	result := buffer.complete(ctx, sink, false, confirmedTransportResult())
	if !result.Success || len(sink.deadlines) != 2 || !sink.deadlines[0].Equal(deadline) || !sink.deadlines[1].IsZero() {
		t.Fatalf("caller write outlived execution budget: result=%+v deadlines=%v", result, sink.deadlines)
	}
}

func TestTransportDeadlineDuringSSEDeliveryStopsBeforeDONE(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	buffer := newTransportResponse(false)
	defer buffer.clear()
	_, _ = buffer.Write(transportLimitCompletion(t, strings.Repeat("x", 300<<10)))
	sink := &transportDeadlineSink{ResponseRecorder: httptest.NewRecorder(), ctx: ctx, waitFirst: true}
	result := buffer.complete(ctx, sink, true, confirmedTransportResult())
	if result.Success || !result.Committed || result.Failure != contract.FailureUpstream || result.ClientStatus != 504 || strings.Contains(sink.Body.String(), "[DONE]") {
		t.Fatalf("deadline did not stop SSE delivery: %+v", result)
	}
	if !attemptOutcome(result).ProtocolSuccess || result.Usage.OutputTokens != 3 {
		t.Fatal("partial caller delivery discarded confirmed upstream usage")
	}
}

func TestTransportExecutionDeadlineWrites504AndKeepsCharityCharge(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.service.timeout = time.Second
	f.charity.preflight.TransportRule = transportpolicy.ForceStream
	f.charity.snapshot.TransportRule = transportpolicy.ForceStream
	f.addDispatch(f.charity.snapshot.Candidates[0])
	stream := transportLimitStream(t, 1)
	f.openAI.attempt = func(ctx context.Context, input connector.AttemptInput) contract.AttemptResult {
		if !input.Ingress.Stream {
			t.Fatal("physical request was not streaming")
		}
		if _, err := input.Sink.Write(stream); err != nil {
			t.Fatal(err)
		}
		// The upstream has terminated successfully, but the remaining execution
		// budget expires before forwarding receives that confirmed result.
		<-ctx.Done()
		return confirmedTransportResult()
	}
	sink := httptest.NewRecorder()
	f.service.Chat(context.Background(), sink, 1, decodeChatForTest(t, `{"model":"[公益]care/model","messages":[]}`), nil, "application/json", "en")
	if sink.Code != http.StatusGatewayTimeout || !strings.HasPrefix(sink.Header().Get("Content-Type"), "application/json") || sink.Body.Len() == 0 || strings.Contains(sink.Body.String(), "data:") {
		t.Fatalf("deadline did not emit JSON HTTP 504: status=%d body=%q", sink.Code, sink.Body.String())
	}
	if len(f.claims.outcomes) != 1 || !f.claims.outcomes[0].ResponseStarted || !f.claims.outcomes[0].ProtocolSuccess || f.claims.outcomes[0].Usage.OutputTokens != 3 {
		t.Fatalf("upstream accounting lost: %+v", f.claims.outcomes)
	}
	terminal := f.claims.requestResults[0]
	if terminal.Caller.Class != claim.ResultFailed || terminal.Caller.Status != 504 || terminal.Disposition != claim.AccountingCommit || terminal.ActualChargeMilli != 10 || f.charges.calls != 1 || f.openAI.calls != 1 {
		t.Fatalf("deadline outcome or settlement incorrect: %+v", terminal)
	}
}

package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func gatewayProjectionRequest(t *testing.T, stream bool) *InferenceRequest {
	t.Helper()
	body := `{"model":"public","input":"hello"}`
	if stream {
		body = `{"model":"public","input":"hello","stream":true}`
	}
	request := inferenceRequestForTest(t, contract.OperationResponses, body)
	t.Cleanup(request.Clear)
	return request
}

func projectionEvents(t *testing.T, wire string) []map[string]json.RawMessage {
	t.Helper()
	var out []map[string]json.RawMessage
	for _, frame := range strings.Split(wire, "\n\n") {
		for _, line := range strings.Split(frame, "\n") {
			if strings.HasPrefix(line, "data: ") {
				var event map[string]json.RawMessage
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
					t.Fatal(err)
				}
				out = append(out, event)
			}
		}
	}
	return out
}

func TestGatewayResponsesProjectionReasoningIsExplicit(t *testing.T) {
	request := gatewayProjectionRequest(t, false)
	raw := []byte(`{"id":"c","created":1,"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"answer","reasoning_content":"actual reasoning","tool_calls":[{"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`)
	if _, _, err := projectChatAsResponse(raw, request); err == nil {
		t.Fatal("ordinary Chat bridge accepted reasoning extension")
	}
	writer := httptest.NewRecorder()
	projection, err := NewGatewayResponsesChatProjection(context.Background(), writer, request, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer projection.Clear()
	if _, err = projection.Write(raw); err != nil {
		t.Fatal(err)
	}
	if !projection.Complete() || !projection.Committed() {
		t.Fatal("JSON projection did not complete")
	}
	var root struct {
		Output []map[string]json.RawMessage
		Usage  struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
		}
	}
	if json.Unmarshal(writer.Body.Bytes(), &root) != nil || len(root.Output) != 3 || root.Usage.Input != 2 || root.Usage.Output != 3 {
		t.Fatal(writer.Body.String())
	}
	if string(root.Output[0]["type"]) != `"reasoning"` || string(root.Output[0]["summary"]) != "[]" || string(root.Output[0]["content"]) != `[{"text":"actual reasoning","type":"reasoning_text"}]` || string(root.Output[1]["type"]) != `"message"` || string(root.Output[2]["type"]) != `"function_call"` {
		t.Fatal(writer.Body.String())
	}
	ordinary := responseChatState{model: "public"}
	defer ordinary.clear()
	delta := strings.TrimSuffix(strings.TrimPrefix(chatResponseChunk(`{"reasoning_content":"thought"}`, "null", "null"), "data: "), "\n\n")
	if ordinary.consume([]byte(delta), true) == nil {
		t.Fatal("ordinary streaming bridge accepted reasoning extension")
	}
}

func TestGatewayResponsesProjectionOrdersReasoningTextAndTools(t *testing.T) {
	for _, reasoningFirst := range []bool{false, true} {
		request := gatewayProjectionRequest(t, true)
		writer := &projectionCheckpoint{ResponseRecorder: httptest.NewRecorder()}
		p, err := NewGatewayResponsesChatProjection(context.Background(), writer, request, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Clear()
		channels := []string{`{"content":"answer"}`, `{"reasoning_content":"real "}`}
		if reasoningFirst {
			channels[0], channels[1] = channels[1], channels[0]
		}
		for _, delta := range append(channels, `{"reasoning_content":"thought"}`, `{"tool_calls":[{"index":0,"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"}}]}`) {
			if _, err = p.Write([]byte(chatResponseChunk(delta, "null", "null"))); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = p.Write([]byte(chatResponseChunk("{}", `"tool_calls"`, "null"))); err != nil {
			t.Fatal(err)
		}
		if p.Complete() {
			t.Fatal("finish without terminal accepted")
		}
		if _, err = p.Write([]byte("data: [DONE]\n\n")); err != nil {
			t.Fatal(err)
		}
		if err = p.FlushError(); err != nil {
			t.Fatal(err)
		}
		events := projectionEvents(t, writer.Body.String())
		added := []string{}
		done := map[string]int{}
		deltas := ""
		reasonIndex := 1
		if reasoningFirst {
			reasonIndex = 0
		}
		for sequence, event := range events {
			var gotSequence int
			_ = json.Unmarshal(event["sequence_number"], &gotSequence)
			if gotSequence != sequence {
				t.Fatalf("sequence %d != %d", gotSequence, sequence)
			}
			kind := string(event["type"])
			switch kind {
			case `"response.output_item.added"`:
				var item map[string]json.RawMessage
				_ = json.Unmarshal(event["item"], &item)
				var index int
				_ = json.Unmarshal(event["output_index"], &index)
				if index != len(added) {
					t.Fatal("noncontiguous output index")
				}
				added = append(added, string(item["type"]))
			case `"response.reasoning_text.delta"`, `"response.reasoning_text.done"`:
				if string(event["output_index"]) != string(rune('0'+reasonIndex)) || string(event["content_index"]) != "0" || event["summary_index"] != nil {
					t.Fatalf("%s", writer.Body.Bytes())
				}
				var value string
				if kind == `"response.reasoning_text.delta"` {
					_ = json.Unmarshal(event["delta"], &value)
					deltas += value
				} else {
					_ = json.Unmarshal(event["text"], &value)
					if value != "real thought" {
						t.Fatal(value)
					}
				}
			case `"response.output_item.done"`:
				var item map[string]json.RawMessage
				_ = json.Unmarshal(event["item"], &item)
				done[string(item["type"])]++
			case `"response.completed"`:
				var response struct{ Output []map[string]json.RawMessage }
				_ = json.Unmarshal(event["response"], &response)
				if len(response.Output) != 3 {
					t.Fatal("final output lost item")
				}
				for i, item := range response.Output {
					if string(item["type"]) != added[i] {
						t.Fatal("final order differs from events")
					}
				}
			}
		}
		if len(added) != 3 || added[reasonIndex] != `"reasoning"` || added[2] != `"function_call"` || deltas != "real thought" || len(done) != 3 || done[`"reasoning"`] != 1 || writer.started != 1 || !p.Complete() {
			t.Fatal(writer.Body.String())
		}
		if strings.Contains(writer.Body.String(), "summary_text") {
			t.Fatal("raw reasoning relabeled as summary")
		}
	}
}

type projectionCheckpoint struct {
	*httptest.ResponseRecorder
	started, observed, flushes, deadlines int
	checkpointError                       bool
	cancel                                context.CancelFunc
}

func (w *projectionCheckpoint) MarkResponseStarted() error {
	w.started++
	if w.checkpointError {
		return errors.New("checkpoint unavailable")
	}
	return nil
}
func (w *projectionCheckpoint) ObserveUpstreamResponse(int, bool, http.Header) error {
	w.observed++
	return nil
}
func (w *projectionCheckpoint) Flush()                           { w.flushes++; w.ResponseRecorder.Flush() }
func (w *projectionCheckpoint) SetWriteDeadline(time.Time) error { w.deadlines++; return nil }
func (w *projectionCheckpoint) Write(data []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(data)
	if w.cancel != nil {
		w.cancel()
	}
	return n, err
}

func TestGatewayResponsesProjectionBoundaries(t *testing.T) {
	request := gatewayProjectionRequest(t, true)
	t.Run("wrappers", func(t *testing.T) {
		writer := &projectionCheckpoint{ResponseRecorder: httptest.NewRecorder()}
		p, _ := NewGatewayResponsesChatProjection(context.Background(), writer, request, 1<<20)
		defer p.Clear()
		p.Flush()
		if err := contract.MarkResponseStarted(p); err != nil {
			t.Fatal(err)
		}
		if err := contract.ObserveUpstreamResponse(p, 200, true, http.Header{}); err != nil {
			t.Fatal(err)
		}
		if err := http.NewResponseController(p).SetWriteDeadline(time.Now()); err != nil {
			t.Fatal(err)
		}
		if writer.started != 0 || writer.flushes != 0 || writer.observed != 1 || writer.deadlines != 1 {
			t.Fatalf("%+v", writer)
		}
		if _, err := p.Write([]byte("data: [DONE]\n\n")); err == nil || p.Complete() || writer.Body.Len() != 0 {
			t.Fatal("terminal without finish committed")
		}
	})
	t.Run("limit", func(t *testing.T) {
		limit := int64(httperr.MaxSSEErrorFrameBytes + 8192)
		writer := &projectionCheckpoint{ResponseRecorder: httptest.NewRecorder()}
		p, _ := NewGatewayResponsesChatProjection(context.Background(), writer, request, limit)
		defer p.Clear()
		if _, err := p.Write([]byte(chatResponseChunk(`{"content":"a"}`, "null", "null"))); err != nil {
			t.Fatal(err)
		}
		_, err := p.Write([]byte(chatResponseChunk(`{"content":"`+strings.Repeat("x", 9000)+`"}`, "null", "null")))
		if err == nil || !p.Rejected() || p.Complete() || int64(writer.Body.Len()) > limit || !strings.Contains(writer.Body.String(), `"error"`) {
			t.Fatal("bounded failure was lost")
		}
	})
	t.Run("cancel-between-events", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		writer := &projectionCheckpoint{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
		p, _ := NewGatewayResponsesChatProjection(ctx, writer, request, 1<<20)
		defer p.Clear()
		_, err := p.Write([]byte(chatResponseChunk(`{"content":"hello"}`, "null", "null")))
		if !errors.Is(err, context.Canceled) || !p.Committed() || p.Complete() || strings.Contains(writer.Body.String(), "response.in_progress") {
			t.Fatal("projection continued after cancellation")
		}
	})
	t.Run("checkpoint", func(t *testing.T) {
		writer := &projectionCheckpoint{ResponseRecorder: httptest.NewRecorder(), checkpointError: true}
		p, _ := NewGatewayResponsesChatProjection(context.Background(), writer, request, 1<<20)
		defer p.Clear()
		_, err := p.Write([]byte(chatResponseChunk(`{"content":"hello"}`, "null", "null")))
		if err == nil || !p.Rejected() || p.Committed() || writer.Body.Len() != 0 {
			t.Fatal("checkpoint error crossed response boundary")
		}
	})
	t.Run("upstream-error", func(t *testing.T) {
		writer := httptest.NewRecorder()
		p, _ := NewGatewayResponsesChatProjection(context.Background(), writer, request, 1<<20)
		defer p.Clear()
		if _, err := p.Write([]byte(chatResponseChunk(`{"content":"hello"}`, "null", "null"))); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(p, "data: {\"error\":{\"message\":\"opaque-secret\"}}\n\n"); err != nil {
			t.Fatal(err)
		}
		if p.Rejected() || p.Complete() || strings.Contains(writer.Body.String(), "opaque-secret") || strings.Contains(writer.Body.String(), "response.completed") {
			t.Fatal("upstream error was reclassified or completed")
		}
	})
}

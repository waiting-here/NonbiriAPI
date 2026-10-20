package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

const decisionBody = `{"model":"public/model","input":"The screen is broken.","questions":[{"type":"predicate","name":"damaged","instructions":"Is it damaged?"}]}`
const decisionReply = `{"model":"private-model","answers":[{"type":"predicate","name":"damaged","probability":0.95}],"usage":{"input_tokens":42,"input_tokens_details":{"cached_tokens":10,"cache_write_tokens":2},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":2},"total_tokens":45}}`

func decodeDecision(t *testing.T, body string) *DecisionRequest {
	t.Helper()
	r, err := DecodeDecisionRequest(strings.NewReader(body), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Clear)
	return r
}

func TestDecisionRequestBoundariesAndToolHistory(t *testing.T) {
	question := `{"type":"predicate","instructions":""}`
	for _, count := range []int{1, 200} {
		body := `{"model":"m","input":"","questions":[` + strings.TrimSuffix(strings.Repeat(question+",", count), ",") + `]}`
		decodeDecision(t, body)
	}
	valid := []string{
		`{"model":"m","input":"","stream":false,"questions":[{"type":"choice","instructions":"","choices":[{"value":true},{"value":"true"}]}]}`,
		`{"model":"m","input":[{"type":"function_call","call_id":"c","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"c","output":[{"type":"input_text","text":"answer","prompt_cache_breakpoint":{"mode":"explicit"}}]}],"questions":[` + question + `]}`,
		`{"model":"m","input":[{"role":"system","content":[{"type":"input_text","text":"rules"}]},{"role":"assistant","phase":"final_answer","content":[{"type":"refusal","refusal":"declined"}]},{"type":"custom_tool_call","call_id":"c","name":"lookup","input":"x"},{"type":"custom_tool_call_output","call_id":"c","output":"answer"}],"questions":[{"type":"score","instructions":"","levels":[{"label":"low"},{"label":"high"}]}],"prompt_cache_key":"key","prompt_cache_options":{"mode":"explicit","ttl":"30m"}}`,
	}
	for _, body := range valid {
		decodeDecision(t, body)
	}
	invalid := []string{
		strings.Replace(decisionBody, `"input":`, `"input":null,"input":`, 1),
		strings.Replace(decisionBody, `"input":"The screen is broken."`, `"input":null`, 1),
		strings.Replace(decisionBody, `"questions":[`, `"stream":true,"questions":[`, 1),
		strings.Replace(decisionBody, `"questions":[`, `"stream":null,"questions":[`, 1),
		strings.Replace(decisionBody, `"questions":[`, `"metadata":{},"questions":[`, 1),
		`{"model":"m","input":"x","questions":[]}`,
		`{"model":"m","input":"x","questions":[` + strings.TrimSuffix(strings.Repeat(question+",", 201), ",") + `]}`,
		`{"model":"m","input":"x","questions":[{"type":"choice","instructions":"x","choices":[{"value":"yes"},{"value":"\u0079es"}]}]}`,
		`{"model":"m","input":[{"type":"function_call_output","call_id":"missing","output":"x"}],"questions":[` + question + `]}`,
		`{"model":"m","input":[{"role":"system","content":[{"type":"input_image","image_url":"https://example.com/a.png"}]}],"questions":[` + question + `]}`,
		`{"model":"m","input":[{"role":"assistant","content":[{"type":"input_text","text":"x"}]}],"questions":[` + question + `]}`,
		`{"model":"m","input":[{"type":"item_reference","id":"x"}],"questions":[` + question + `]}`,
	}
	for i, body := range invalid {
		if r, err := DecodeDecisionRequest(strings.NewReader(body), 0); err == nil {
			r.Clear()
			t.Errorf("case %d accepted", i)
		}
	}
	image := `{"type":"input_image","image_url":"https://example.com/a.png"}`
	for _, count := range []int{128, 129} {
		body := `{"model":"m","input":[{"role":"user","content":[` + strings.TrimSuffix(strings.Repeat(image+",", count), ",") + `]}],"questions":[` + question + `]}`
		r, err := DecodeDecisionRequest(strings.NewReader(body), 0)
		if (err == nil) != (count == 128) {
			t.Fatalf("image count %d: %v", count, err)
		}
		if r != nil {
			r.Clear()
		}
	}
}

func TestDecisionResponseProjectionAndUnknownUsage(t *testing.T) {
	r := decodeDecision(t, decisionBody)
	body, usage, err := projectDecisionResponse([]byte(decisionReply), r)
	if err != nil || !usage.Present || usage.UncachedInputTokens != 30 || usage.CacheReadInputTokens != 10 || usage.CacheWriteInputTokens != 2 || usage.OutputTokens != 3 {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
	if bytes.Contains(body, []byte("private-model")) || !bytes.Contains(body, []byte("public/model")) {
		t.Fatalf("source identity projection failed: %s", body)
	}
	for _, replacement := range []string{`"input_tokens":null`, `"input_tokens":-1`, `"input_tokens":"42"`, `"input_tokens":1`} {
		body, usage, err = projectDecisionResponse([]byte(strings.Replace(decisionReply, `"input_tokens":42`, replacement, 1)), r)
		if err != nil || usage.Present || bytes.Contains(body, []byte(`"usage"`)) {
			t.Fatalf("malformed usage not unknown: %s %+v %v", body, usage, err)
		}
	}
	for _, reply := range []string{
		strings.Replace(decisionReply, `"damaged"`, `null`, 1),
		strings.Replace(decisionReply, `0.95`, `null`, 1),
		strings.Replace(decisionReply, `0.95`, `1.1`, 1),
		strings.Replace(decisionReply, `"predicate"`, `"choice"`, 1),
	} {
		if _, _, err := projectDecisionResponse([]byte(reply), r); err == nil {
			t.Errorf("accepted invalid answer %s", reply)
		}
	}
	refusal := `{"model":"private","answers":[{"type":"refusal","name":"damaged"}]}`
	if _, usage, err := projectDecisionResponse([]byte(refusal), r); err != nil || usage.Present {
		t.Fatalf("refusal: %v %+v", err, usage)
	}
}

func TestDecisionTypedAnswersAndQuestionOrder(t *testing.T) {
	r := decodeDecision(t, `{"model":"m","input":"x","questions":[{"type":"choice","instructions":"","choices":[{"value":true},{"value":"true"}]},{"type":"score","name":"s","instructions":"","levels":[{"label":"low"},{"label":"high"}]}]}`)
	reply := `{"model":"private","answers":[{"type":"choice","name":null,"choice":true,"confidence":0.6,"probabilities":[{"value":"true","probability":0.4},{"value":true,"probability":0.6}]},{"type":"score","name":"s","score":0.8,"confidence":0.8,"probabilities":[{"value":0,"label":"low","probability":0.2},{"value":1,"label":"high","probability":0.8}]}]}`
	if _, _, err := projectDecisionResponse([]byte(reply), r); err != nil {
		t.Fatal(err)
	}
	for _, change := range [][2]string{{`"choice":true`, `"choice":false`}, {`"value":"true"`, `"value":true`}, {`"label":"low"`, `"label":"wrong"`}, {`"value":1`, `"value":3`}, {`"score":0.8`, `"score":2`}} {
		if _, _, err := projectDecisionResponse([]byte(strings.Replace(reply, change[0], change[1], 1)), r); err == nil {
			t.Errorf("accepted %v", change)
		}
	}
}

type decisionCheckpoint struct {
	*httptest.ResponseRecorder
	observed, started int
	status            int
	stream            bool
}

func (w *decisionCheckpoint) ObserveUpstreamResponse(status int, stream bool, _ http.Header) error {
	w.observed++
	w.status = status
	w.stream = stream
	return nil
}
func (w *decisionCheckpoint) MarkResponseStarted() error { w.started++; return nil }

func TestDecisionAdapterNativeWireCheckpointAndSecretGuard(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(fmt.Sprint(broken), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/decisions" || r.Header.Get("Authorization") != "Bearer upstream-secret" || r.Header.Get("X-Project") != "configured" {
					t.Error("incorrect native request")
				}
				body, _ := io.ReadAll(r.Body)
				var object map[string]any
				if json.Unmarshal(body, &object) != nil || object["model"] != "private-model" || object["safety_identifier"] != "consumer-id" || object["stream"] != nil {
					t.Error("native projection failed")
				}
				w.Header().Set("Content-Type", "application/json")
				if broken {
					_, _ = io.WriteString(w, `{"model":"private-model","answers":[]}`)
				} else {
					_, _ = io.WriteString(w, decisionReply)
				}
			}))
			defer server.Close()
			adapter := adapterForServer(t, server.URL, nil, nil)
			writer := &decisionCheckpoint{ResponseRecorder: httptest.NewRecorder()}
			r := decodeDecision(t, strings.Replace(decisionBody, `"input":`, `"stream":false,"input":`, 1))
			result := adapter.AttemptDecision(context.Background(), writer, NewTarget(server.URL+"/v1", "private-model", NewCredential([]byte("upstream-secret"), nil)), r, contract.AttemptPolicy{SafetyIdentifier: "consumer-id", AdditionalHeaders: http.Header{"X-Project": []string{"configured"}}})
			if result.Success == broken || writer.observed != 1 || writer.status != 200 || writer.stream || writer.started != map[bool]int{false: 1, true: 0}[broken] {
				t.Fatalf("result=%+v checkpoints=%+v", result, writer)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, strings.Replace(decisionReply, `"damaged"`, `"upstream-secret"`, 1))
	}))
	defer server.Close()
	r := decodeDecision(t, strings.Replace(decisionBody, `"damaged"`, `"upstream-secret"`, 1))
	writer := httptest.NewRecorder()
	result := adapterForServer(t, server.URL, nil, nil).AttemptDecision(context.Background(), writer, NewTarget(server.URL, "private-model", NewCredential([]byte("upstream-secret"), nil)), r, contract.AttemptPolicy{SafetyIdentifier: "consumer-id"})
	if result.Success || result.Committed || writer.Body.Len() != 0 {
		t.Fatalf("secret reflection escaped: %+v", result)
	}
}

func TestDecisionCloneOwnsMutableState(t *testing.T) {
	r := decodeDecision(t, `{"model":"m","input":"source","prompt_cache_key":"cache","questions":[{"type":"choice","name":"choice","instructions":"","choices":[{"value":"yes"},{"value":true}]},{"type":"score","name":"score","instructions":"","levels":[{"label":"low"},{"label":"high"}]}]}`)
	if err := r.ExcludeFields([]string{"prompt_cache_key"}); err != nil {
		t.Fatal(err)
	}
	before, _ := r.LogicalBody()
	clone := r.CloneForAttempt()
	if clone == nil || clone.RequestBodyLimit() != r.RequestBodyLimit() {
		t.Fatal("clone unavailable")
	}
	clone.Model = "changed"
	clone.fields[0].value[0] = '!'
	clone.questions[0].name[0] = '!'
	clone.questions[0].values[0][0] = '!'
	clone.questions[1].labels[0] = "changed"
	clone.excluded[0] = "changed"
	clone.Clear()
	after, _ := r.LogicalBody()
	if !bytes.Equal(before, after) || r.Model != "m" || string(r.questions[0].name) != `"choice"` || string(r.questions[0].values[0]) != `"yes"` || r.questions[1].labels[0] != "low" || r.excluded[0] != "prompt_cache_key" {
		t.Fatal("mutating or clearing an attempt changed the original")
	}
	clone = r.CloneForAttempt()
	defer clone.Clear()
	r.Clear()
	retained, _ := clone.LogicalBody()
	if !bytes.Equal(before, retained) || string(clone.questions[0].name) != `"choice"` || string(clone.questions[0].values[0]) != `"yes"` {
		t.Fatal("clearing source changed clone")
	}
}

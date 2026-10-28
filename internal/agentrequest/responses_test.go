package agentrequest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func completedResponse(output string) string {
	return `event: response.completed` + "\n" + `data: {"type":"response.completed","response":{"status":"completed","output":` + output + `,"usage":{"input_tokens":20,"output_tokens":8,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":3}}}}` + "\n\n"
}

const responseText = `[{"type":"message","id":"message-1","role":"assistant","content":[{"type":"output_text","text":"finished"}]}]`

func TestDriverCatalogPreservesAccountOrderAndAllowsDiscoveryBeforeSelection(t *testing.T) {
	models, err := decodeDriverModels([]byte(`{"models":[{"slug":"z","display_name":"First","visibility":"list"},{"slug":"private","visibility":"hidden"},{"slug":"a","display_name":"Second","visibility":"list"}]}`), ProviderChatGPT)
	if err != nil || len(models) != 2 || models[0].ID != "z" || models[1].DisplayName != "Second" {
		t.Fatal(models, err)
	}
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Error(r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"available"}]}`)
	})
	p := responsesProfile(t, f, "")
	models, err = f.runtime.DiscoverDriverModels(context.Background(), p.ID)
	if err != nil || len(models) != 1 || models[0].ID != "available" {
		t.Fatal(models, err)
	}
	bindProfile(t, f, "default", 0, p.ID)
	if _, err = f.runtime.Submit(context.Background(), input("unselected")); !errors.Is(err, ErrUnconfigured) {
		t.Fatal("unselected model dispatched", err)
	}
}

func TestResponsesNativeReplayToolsAndStrictFormat(t *testing.T) {
	s := Settings{Provider: ProviderChatGPT, DriverID: 9, ConnectionRevision: 2, BaseURL: "https://api.openai.com/v1", Model: "fixture", ContextWindow: 32768, OutputReserveTokens: 4096, ExtraParameters: json.RawMessage(`{}`)}
	output := `[{"type":"reasoning","id":"reason-1","summary":[],"encrypted_content":"encrypted-reasoning"},{"type":"message","id":"message-1","role":"assistant","content":[{"type":"output_text","text":"checking"}]},{"type":"reasoning","id":"reason-2","summary":[],"encrypted_content":"second-reasoning"},{"type":"function_call","id":"function-1","call_id":"call-1","namespace":"nonbiri","name":"inspect","arguments":"{\"id\":1}"}]`
	result, complete, code, err := parseResponsesResult(4, []byte(completedResponse(output)), s)
	if err != nil || !complete || code != "" || result.FinishReason != "tool_calls" || result.Usage.InputTokens != 20 || result.Usage.ReasoningTokens != 3 {
		t.Fatal(result, complete, code, err)
	}
	messages := `[{"role":"system","content":"guide"},` + string(result.Message) + `,{"role":"tool","tool_call_id":"call-1","content":"ok"}]`
	in := Input{Messages: json.RawMessage(messages), Tools: json.RawMessage(`[{"type":"function","function":{"name":"inspect","parameters":{"type":"object","properties":{"id":{"type":"integer"}}}}}]`), ToolChoice: json.RawMessage(`{"type":"function","function":{"name":"inspect"}}`), ResponseFormat: json.RawMessage(`{"type":"json_schema","json_schema":{"name":"approval","strict":true,"schema":{"type":"object","properties":{"allow":{"type":"boolean"}},"required":["allow"],"additionalProperties":false}}}`)}
	raw, err := requestBody(in, s)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	_ = json.Unmarshal(raw, &body)
	for _, field := range []string{"max_output_tokens", "temperature", "top_p", "previous_response_id", "reasoning", "service_tier", "metadata", "safety_identifier"} {
		if _, ok := body[field]; ok {
			t.Fatalf("default was overridden: %s", field)
		}
	}
	if string(body["store"]) != "false" || string(body["stream"]) != "true" {
		t.Fatal(string(raw))
	}
	var items []map[string]json.RawMessage
	_ = json.Unmarshal(body["input"], &items)
	if len(items) != 6 || string(items[0]["role"]) != `"developer"` || string(items[1]["encrypted_content"]) != `"encrypted-reasoning"` || string(items[3]["encrypted_content"]) != `"second-reasoning"` || string(items[5]["type"]) != `"function_call_output"` {
		t.Fatal(string(body["input"]))
	}
	if !strings.Contains(string(body["tools"]), `"type":"namespace"`) || !strings.Contains(string(body["tools"]), `"strict":false`) || !strings.Contains(string(body["text"]), `"strict":true`) {
		t.Fatal(string(raw))
	}
	other := s
	other.DriverID++
	cross, err := requestBody(in, other)
	if err != nil || strings.Contains(string(cross), "encrypted-reasoning") {
		t.Fatal("foreign native reasoning replayed", err)
	}
	reconnected := s
	reconnected.ConnectionRevision++
	newAccount, err := requestBody(in, reconnected)
	if err != nil || strings.Contains(string(newAccount), "encrypted-reasoning") || !strings.Contains(string(newAccount), "checking") {
		t.Fatal("account change did not project compatible history", err)
	}
	refreshed := s
	refreshed.Revision++ // Profile settings can change without changing the connection.
	sameAccount, err := requestBody(in, refreshed)
	if err != nil || !strings.Contains(string(sameAccount), "encrypted-reasoning") {
		t.Fatal("same connection lost opaque history", err)
	}
	chat := s
	chat.Provider = ProviderChat
	chat.MaxOutputTokens = 100
	chat.OutputTokenParameter = "max_tokens"
	chatRaw, err := requestBody(in, chat)
	if err != nil || strings.Contains(string(chatRaw), "responses_output") || strings.Contains(string(chatRaw), "responses_provider") {
		t.Fatal("native metadata escaped into Chat", err)
	}
}

func TestResponsesRequiresProtocolCompletionAndKeepsFailureUsage(t *testing.T) {
	s := Settings{Provider: ProviderResponses, Model: "fixture"}
	cases := []struct{ name, raw, code string }{
		{"clean-eof", `data: {"type":"response.output_text.delta","delta":"partial"}` + "\n\n", "protocol"},
		{"done-only", "data: [DONE]\n\n", "protocol"},
		{"incomplete", `data: {"type":"response.incomplete","response":{"status":"incomplete","usage":{"input_tokens":2,"output_tokens":1}}}` + "\n\n", "upstream_incomplete"},
		{"quota", `data: {"type":"response.failed","response":{"status":"failed","error":{"code":"subscription_sharing_usage_limit_exceeded"},"usage":{"input_tokens":2,"output_tokens":1}}}` + "\n\n", "subscription_sharing_usage_limit_exceeded"},
		{"after-completed", completedResponse(responseText) + "data: invalid\n\n", "protocol"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, done, code, err := parseResponsesResult(1, []byte(tc.raw), s)
			if !errors.Is(err, ErrProtocol) || done || code != tc.code {
				t.Fatalf("%+v %v %s %v", out, done, code, err)
			}
			if (tc.name == "quota" || tc.name == "incomplete") && (out.Usage.Unknown || out.Usage.OutputTokens != 1) {
				t.Fatal(out.Usage)
			}
		})
	}
	if _, done, _, err := parseResponsesResult(1, []byte(completedResponse(responseText)+"data: [DONE]\n\n"), s); err != nil || !done {
		t.Fatal(done, err)
	}
}

func responsesProfile(t *testing.T, f *fixture, model string) DriverProfile {
	t.Helper()
	s, err := f.runtime.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s.Provider = ProviderResponses
	s.Model = model
	s.MaxOutputTokens = 0
	s.OutputReserveTokens = 2048
	key := fixtureKey
	name := model
	if name == "" {
		name = "unselected"
	}
	p, err := f.runtime.PutDriver(context.Background(), 0, DriverUpdate{Name: name, Settings: s, APIKey: &key})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func bindProfile(t *testing.T, f *fixture, scope string, actor, id int64) DriverBinding {
	t.Helper()
	v, err := f.runtime.PutBinding(context.Background(), BindingUpdate{Scope: scope, ActorID: actor, DriverID: &id})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDriverInheritanceAuthorizationAndDispatchSnapshot(t *testing.T) {
	started := make(chan string, 1)
	release := make(chan struct{})
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Error(r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		started <- string(raw)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, completedResponse(responseText))
	})
	global := responsesProfile(t, f, "global")
	purpose := responsesProfile(t, f, "steward")
	personal := responsesProfile(t, f, "personal")
	bindProfile(t, f, "default", 0, global.ID)
	binding := bindProfile(t, f, "steward_chat", 0, purpose.ID)
	bindProfile(t, f, "steward_chat", 8, personal.ID)
	for _, tc := range []struct {
		source      string
		actor, want int64
	}{{"admin_chat", 0, global.ID}, {"auto_review", 0, global.ID}, {"steward_chat", 7, purpose.ID}, {"steward_chat", 8, personal.ID}} {
		s, err := f.runtime.ResolveSettings(context.Background(), Attribution{Source: tc.source, ActorID: tc.actor})
		if err != nil || s.DriverID != tc.want || s.OutputReserveTokens != 2048 {
			t.Fatal(tc, s, err)
		}
	}
	if err := f.runtime.DeleteDriver(context.Background(), purpose.ID, purpose.Settings.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("bound profile deleted", err)
	}
	if _, err := f.runtime.PutBinding(context.Background(), BindingUpdate{Scope: binding.Scope, ExpectedRevision: binding.Revision}); err != nil {
		t.Fatal(err)
	}
	s, err := f.runtime.ResolveSettings(context.Background(), Attribution{Source: "steward_chat", ActorID: 7})
	if err != nil || s.DriverID != global.ID {
		t.Fatal(s, err)
	}
	request := submit(t, f.runtime, "snapshot")
	if err = f.runtime.Start(); err != nil {
		t.Fatal(err)
	}
	wire := await(t, started)
	if !strings.Contains(wire, `"model":"global"`) {
		t.Fatal(wire)
	}
	updated := global.Settings
	updated.Model = "changed"
	if _, err = f.runtime.PutDriver(context.Background(), global.ID, DriverUpdate{ExpectedRevision: global.Settings.Revision, Name: "renamed", Settings: updated}); err != nil {
		t.Fatal(err)
	}
	close(release)
	result := waitResult(t, f.runtime, request.ID)
	if result.FinishReason != "stop" {
		t.Fatal(result)
	}
	detail, err := f.runtime.Detail(context.Background(), request.ID)
	if err != nil || detail.Provider != ProviderResponses || detail.DriverID != global.ID || detail.Model != "global" || !strings.Contains(string(detail.DriverSettings), `"model":"global"`) {
		t.Fatal(detail, err)
	}
	if strings.Contains(string(detail.DriverSettings), fixtureKey) || strings.Contains(detail.RequestBody, fixtureKey) || strings.Contains(detail.ResponseBody, fixtureKey) {
		t.Fatal("secret in request history")
	}
	f.runtime.adminAuthorize = func(context.Context, *sql.Tx) error { return errors.New("forbidden") }
	if _, err = f.runtime.Drivers(context.Background()); err == nil {
		t.Fatal("unauthorized drivers")
	}
	if _, err = f.runtime.PutDriver(context.Background(), 0, DriverUpdate{Name: "x", Settings: global.Settings}); err == nil {
		t.Fatal("unauthorized write")
	}
}

func TestResponsesRuntimeCancellationAndRedaction(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			started := make(chan string, 1)
			f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				started <- "started"
				if cancelled {
					<-r.Context().Done()
					return
				}
				_, _ = io.WriteString(w, completedResponse(strings.ReplaceAll(responseText, "finished", fixtureKey)))
			})
			p := responsesProfile(t, f, "response")
			bindProfile(t, f, "default", 0, p.ID)
			request := submit(t, f.runtime, "case")
			if err := f.runtime.Start(); err != nil {
				t.Fatal(err)
			}
			await(t, started)
			if cancelled {
				if err := f.runtime.Cancel(context.Background(), request.ID); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := f.runtime.Wait(ctx, request.ID)
			if cancelled && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled request did not terminate: %v", err)
			}
			detail, e := f.runtime.Detail(ctx, request.ID)
			if e != nil {
				t.Fatal(e)
			}
			if cancelled && detail.State != "cancelled" {
				t.Fatal(detail.State)
			}
			if !cancelled && (!detail.ContentRedacted || strings.Contains(detail.ResponseBody, fixtureKey)) {
				t.Fatal("echoed secret retained")
			}
		})
	}
}

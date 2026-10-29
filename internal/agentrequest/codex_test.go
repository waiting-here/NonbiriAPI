package agentrequest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/codexauth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func attachCodex(t *testing.T, f *fixture) DriverProfile {
	t.Helper()
	// The production schema is installed by the separately owned explicit
	// upgrade. This empty fixture mirrors the new provider constraint.
	if _, err := f.database.Exec("DROP TABLE agent_request_drivers"); err != nil {
		t.Fatal(err)
	}
	// Recreate the empty snapshot table without changing other source tables.
	if _, err := f.database.Exec(`CREATE TABLE agent_request_drivers (
 request_id INTEGER PRIMARY KEY REFERENCES agent_requests(id) ON DELETE CASCADE,
 driver_id INTEGER NOT NULL CHECK(driver_id>=0),
 provider TEXT NOT NULL CHECK(provider IN ('chat_completions','responses','chatgpt','codex')),
 settings_json TEXT NOT NULL CHECK(json_valid(settings_json))
) STRICT;` + db.CodexSessionSQL); err != nil {
		t.Fatal(err)
	}
	vault := f.runtime.secrets.(secret.CodexCodec)
	raw, _ := json.Marshal(codexauth.Credentials{AccessToken: "codex-access-secret", RefreshToken: "codex-refresh-secret", IDToken: "unused", AccountID: "workspace", Subject: "subject", ExpiresAt: f.clock.Load()/1000 + 3600})
	encrypted, err := vault.SealCodex(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.database.Exec("INSERT INTO agent_codex_session(id,revision,encrypted_credentials,account_id,expires_at,updated_at) VALUES(1,1,?,'workspace',?,0)", encrypted, f.clock.Load()/1000+3600); err != nil {
		t.Fatal(err)
	}
	m, err := codexauth.New(codexauth.Config{DB: f.runtime.db, ReadDB: f.runtime.readDB, Stack: f.runtime.stack, Secrets: vault, AdminAuthorize: f.runtime.adminAuthorize, Now: f.runtime.now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	f.runtime.codex = m
	s, _ := f.runtime.Settings(context.Background())
	s.Provider = ProviderCodex
	s.BaseURL = ""
	s.MaxOutputTokens = 0
	p, err := f.runtime.PutDriver(context.Background(), 0, DriverUpdate{Name: "Codex", Settings: s})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestCodexExplicitBindingsAndQueueDisconnect(t *testing.T) {
	f := newFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected upstream") })
	p := attachCodex(t, f)
	ctx := context.Background()
	for _, scope := range []string{"default", "auto_review", "steward_chat"} {
		id := p.ID
		if _, err := f.runtime.PutBinding(ctx, BindingUpdate{Scope: scope, DriverID: &id}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s inherited codex: %v", scope, err)
		}
	}
	bindProfile(t, f, "admin_chat", 0, p.ID)
	bindProfile(t, f, "steward_chat", 7, p.ID)
	s, err := f.runtime.ResolveSettings(ctx, Attribution{Source: "steward_chat", ActorID: 7, ActorRole: "level5"})
	if err != nil || s.Provider != ProviderCodex || !s.HasKey {
		t.Fatalf("explicit authorization: %+v %v", s, err)
	}
	s, err = f.runtime.ResolveSettings(ctx, Attribution{Source: "steward_chat", ActorID: 8, ActorRole: "level6"})
	if err != nil || s.Provider == ProviderCodex {
		t.Fatalf("unbound steward inherited: %+v %v", s, err)
	}
	request, err := f.runtime.Submit(ctx, input("queued-codex"))
	if err != nil {
		t.Fatal(err)
	}
	detail, err := f.runtime.Detail(ctx, request.ID)
	if err != nil || detail.Provider != ProviderCodex {
		t.Fatalf("queue snapshot: %+v %v", detail, err)
	}
	if strings.Contains(string(detail.DriverSettings), "codex-access-secret") {
		t.Fatal("credential in snapshot")
	}
	if _, err = f.runtime.DisconnectCodex(ctx, 1); err != nil {
		t.Fatal(err)
	}
	detail, err = f.runtime.Detail(ctx, request.ID)
	if err != nil || detail.State != "cancelled" {
		t.Fatalf("queue not cancelled: %+v %v", detail, err)
	}
	if err = f.runtime.checkQueuedCodex(ctx, request.ID, Settings{Provider: ProviderCodex, ConnectionRevision: 3}); !errors.Is(err, codexauth.ErrDisconnected) {
		t.Fatalf("old queue can use new account: %v", err)
	}
}
func codexStream() string {
	return "data: " + `{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"reasoning-1","encrypted_content":"opaque","summary":[]}}` + "\n\n" +
		"data: " + `{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"call-1","name":"inspect","arguments":"{}"}}` + "\n\n" +
		"data: " + `{"type":"response.completed","response":{"id":"response-1","usage":{"input_tokens":11,"output_tokens":4,"output_tokens_details":{"reasoning_tokens":2}}}}` + "\n\n"
}
func TestCodexSparseTerminalAndNativeToolRoundTrip(t *testing.T) {
	s := Settings{Provider: ProviderCodex, BaseURL: codexauth.Resource, Model: "model", DriverID: 2, ConnectionRevision: 3}
	result, done, _, err := parseCodexResult(1, []byte(codexStream()), s)
	if err != nil || !done || result.FinishReason != "tool_calls" || result.Usage.ReasoningTokens != 2 {
		t.Fatalf("sparse terminal: %+v %v", result, err)
	}
	in := Input{Messages: json.RawMessage(`[{"role":"system","content":"Operate carefully"},{"role":"user","content":"inspect"},` + string(result.Message) + `,{"role":"tool","tool_call_id":"call-1","content":"result"}]`), Tools: json.RawMessage(`[{"type":"function","function":{"name":"inspect","parameters":{"type":"object"}}}]`)}
	raw, err := requestBody(in, s)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	_ = json.Unmarshal(raw, &body)
	if string(body["instructions"]) != `"Operate carefully"` || string(body["store"]) != "false" || string(body["stream"]) != "true" || !strings.Contains(string(body["input"]), "opaque") || !strings.Contains(string(body["input"]), "function_call_output") || strings.Contains(string(body["tools"]), "namespace") {
		t.Fatal(string(raw))
	}
	for _, broken := range []string{"data: [DONE]\n\n", strings.Split(codexStream(), `data: {"type":"response.completed"`)[0], codexStream() + "data: {}\n\n"} {
		if _, ok, _, err := parseCodexResult(1, []byte(broken), s); err == nil || ok {
			t.Fatal("invalid stream accepted")
		}
	}
}
func TestCodexHTTPToolsAndText(t *testing.T) {
	var turn atomic.Int64
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		current := turn.Add(1)
		if r.URL.Path != "/responses" || r.Header.Get("ChatGPT-Account-ID") != "workspace" || r.Header.Get("Authorization") != "Bearer codex-access-secret" {
			t.Error("incorrect Codex routing")
		}
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), `"instructions":"Operate carefully"`) || strings.Contains(string(raw), "max_output_tokens") {
			t.Error(string(raw))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if current == 1 {
			_, _ = io.WriteString(w, codexStream())
			return
		}
		if !strings.Contains(string(raw), "opaque") || !strings.Contains(string(raw), "function_call_output") {
			t.Error("native tool history missing")
		}
		_, _ = io.WriteString(w, "data: "+`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done"}]}}`+"\n\n"+"data: "+`{"type":"response.completed","response":{"usage":{"input_tokens":8,"output_tokens":1}}}`+"\n\n")
	})
	p := attachCodex(t, f)
	original, _ := f.runtime.Settings(context.Background())
	// Only this local fixture replaces the otherwise fixed service origin.
	p.Settings.BaseURL = original.BaseURL
	raw, _ := json.Marshal(p.Settings)
	if _, err := f.database.Exec("UPDATE agent_driver_profiles SET settings_json=? WHERE id=?", string(raw), p.ID); err != nil {
		t.Fatal(err)
	}
	bindProfile(t, f, "admin_chat", 0, p.ID)
	if err := f.runtime.Start(); err != nil {
		t.Fatal(err)
	}
	first := input("codex-first")
	first.Messages = json.RawMessage(`[{"role":"system","content":"Operate carefully"},{"role":"user","content":"inspect"}]`)
	result, err := f.runtime.Run(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	next := input("codex-second")
	next.Messages = json.RawMessage(`[{"role":"system","content":"Operate carefully"},{"role":"user","content":"inspect"},` + string(result.Message) + `,{"role":"tool","tool_call_id":"call-1","content":"result"}]`)
	result, err = f.runtime.Run(context.Background(), next)
	if err != nil || !strings.Contains(string(result.Message), "Done") || result.Usage.OutputTokens != 1 {
		t.Fatalf("second turn: %+v %v", result, err)
	}
}
func TestCodexRejectsConflictingTerminalOutputAndIdentity(t *testing.T) {
	s := Settings{Provider: ProviderCodex}
	done := "data: " + `{"type":"response.output_item.done","output_index":0,"item":{"id":"item-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"original"}]}}` + "\n\n"
	conflict := done + "data: " + `{"type":"response.completed","response":{"status":"completed","output":[{"id":"item-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"changed"}]}]}}` + "\n\n"
	identity := "data: " + `{"type":"response.created","response":{"id":"first"}}` + "\n\n" + done + "data: " + `{"type":"response.completed","response":{"id":"different"}}` + "\n\n"
	for _, raw := range []string{conflict, identity} {
		if _, complete, _, err := parseCodexResult(1, []byte(raw), s); err == nil || complete {
			t.Fatal("conflicting output accepted")
		}
	}
}

func TestCodexDoneItemsWithoutOutputIndex(t *testing.T) {
	raw := strings.NewReplacer("\"output_index\":0,", "", "\"output_index\":1,", "").Replace(codexStream())
	result, complete, _, err := parseCodexResult(1, []byte(raw), Settings{Provider: ProviderCodex})
	if err != nil || !complete || result.FinishReason != "tool_calls" || !strings.Contains(string(result.Message), "opaque") {
		t.Fatalf("unindexed Codex items: %+v %v", result, err)
	}
}

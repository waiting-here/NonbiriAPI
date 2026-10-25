package agentrequest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	_ "modernc.org/sqlite"
)

type fixtureResolver struct{}

func (fixtureResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}

const fixtureKey = "private-agent-key-for-http-fixture"

type fixture struct {
	runtime  *Runtime
	database *sql.DB
	flow     *flowcontrol.Controller
	clock    atomic.Int64
}

func newFixture(t *testing.T, handler http.HandlerFunc) *fixture {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	path := filepath.ToSlash(filepath.Join(t.TempDir(), "agent.sqlite"))
	writer, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	writer.SetMaxOpenConns(1)
	t.Cleanup(func() { writer.Close() })
	if _, err = writer.Exec(db.AgentRequestsSQL); err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	reader.SetMaxOpenConns(2)
	t.Cleanup(func() { reader.Close() })
	vault, err := secret.New([]byte(strings.Repeat("v", 32)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vault.Close() })
	stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{server.URL}, Resolver: fixtureResolver{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = stack.AddSelfOrigins(context.Background(), &config.Config{SiteBaseURL: "https://agent-self.example.invalid", UserHost: "agent-self.example.invalid", AdminHost: "agent-admin.example.invalid", ListenAddr: "127.0.0.1:41999"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.CloseIdleConnections)
	flow, err := flowcontrol.New(flowcontrol.Config{RPM: ratelimit.RPMConfig{GlobalLimit: 100, PerUserLimit: 1, Window: time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { flow.Close() })
	f := &fixture{database: writer, flow: flow}
	f.clock.Store(1800000000000)
	r, err := New(Config{AdminAuthorize: func(context.Context, *sql.Tx) error { return nil }, DB: writer, ReadDB: reader, Stack: stack, Flow: flow, Secrets: vault, Now: func() time.Time { return time.UnixMilli(f.clock.Load()) }, Authorize: func(context.Context, Attribution) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	f.runtime = r
	t.Cleanup(func() { r.Close() })
	putDriver(t, r, server.URL, 1)
	return f
}
func putDriver(t *testing.T, r *Runtime, base string, limit int) {
	t.Helper()
	current, err := r.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	key := fixtureKey
	_, err = r.PutSettings(context.Background(), SettingsUpdate{ExpectedRevision: current.Revision, BaseURL: base, APIKey: &key, Model: "driver-model", ContextWindow: 32768, MaxOutputTokens: 1024, Concurrency: limit, TimeoutSeconds: 10, ExtraParameters: json.RawMessage("{}")})
	if err != nil {
		t.Fatal(err)
	}
}
func input(id string) Input {
	messages, _ := json.Marshal([]map[string]string{{"role": "user", "content": id}})
	return Input{Attribution: Attribution{ClientRequestID: id, Source: "admin_chat", Kind: "model", ActorRole: "admin"}, Messages: messages}
}
func submit(t *testing.T, r *Runtime, id string) Request {
	t.Helper()
	out, err := r.Submit(context.Background(), input(id))
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func await(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("request did not arrive")
		return ""
	}
}
func waitResult(t *testing.T, r *Runtime, id int64) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := r.Wait(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func answer(w http.ResponseWriter, text string) {
	body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": text}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 3, "prompt_tokens_details": map[string]int{"cached_tokens": 4}}})
	_, _ = w.Write(body)
}
func TestFIFOInterleaveCancelAndBodyLifetime(t *testing.T) {
	started := make(chan string, 5)
	release := make(chan struct{}, 5)
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Messages []struct{ Content string } }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		started <- body.Messages[0].Content
		select {
		case <-release:
			answer(w, "done")
		case <-r.Context().Done():
		}
	})
	a := submit(t, f.runtime, "a")
	b := submit(t, f.runtime, "b")
	c := submit(t, f.runtime, "c")
	if err := f.runtime.Start(); err != nil {
		t.Fatal(err)
	}
	if got := await(t, started); got != "a" {
		t.Fatal(got)
	}
	current, _, _ := f.runtime.stack.SampleGlobalConcurrency()
	if current != 1 {
		t.Fatalf("body did not retain egress slot: %d", current)
	}
	queue, err := f.runtime.Queue(context.Background(), c.ID)
	if err != nil || queue.Position != 2 || queue.Length != 2 {
		t.Fatal(queue, err)
	}
	if err = f.runtime.Cancel(context.Background(), b.ID); err != nil {
		t.Fatal(err)
	}
	release <- struct{}{}
	waitResult(t, f.runtime, a.ID)
	if got := await(t, started); got != "c" {
		t.Fatal(got)
	}
	d := submit(t, f.runtime, "a-next-loop")
	select {
	case got := <-started:
		t.Fatalf("body slot released early: %s", got)
	case <-time.After(35 * time.Millisecond):
	}
	release <- struct{}{}
	waitResult(t, f.runtime, c.ID)
	if got := await(t, started); got != "a-next-loop" {
		t.Fatal(got)
	}
	release <- struct{}{}
	waitResult(t, f.runtime, d.ID)
	if _, err = f.runtime.Wait(context.Background(), b.ID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestGlobalRPMWaitDoesNotHoldWriterOrHTTP(t *testing.T) {
	called := make(chan string, 1)
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { called <- "called"; answer(w, "ok") })
	if err := f.flow.SetLimits(ratelimit.RPMLimits{GlobalLimit: 1, PerUserLimit: 1}); err != nil {
		t.Fatal(err)
	}
	occupied, _, err := f.flow.Admit(context.Background(), 11)
	if err != nil {
		t.Fatal(err)
	}
	q := submit(t, f.runtime, "queued")
	if err = f.runtime.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err = f.database.ExecContext(ctx, "UPDATE agent_request_settings SET updated_at=updated_at+1"); err != nil {
		t.Fatal("queue holds writer", err)
	}
	select {
	case <-called:
		t.Fatal("exceeded global RPM")
	case <-time.After(40 * time.Millisecond):
	}
	active, _, _ := f.runtime.stack.SampleGlobalConcurrency()
	if active != 0 {
		t.Fatal("RPM wait occupies HTTP slot", active)
	}
	occupied.Release()
	if got := await(t, called); got != "called" {
		t.Fatal(got)
	}
	waitResult(t, f.runtime, q.ID)
	rpm, _, _ := f.flow.SampleGlobalRPM()
	if rpm != 1 {
		t.Fatal("Agent used a different global counter", rpm)
	}
}

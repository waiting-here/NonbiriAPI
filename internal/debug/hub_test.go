package debug

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/forward"
)

func testHub(t *testing.T) *Hub {
	t.Helper()
	hub, err := NewHub(Config{SessionBindingValidator: func(context.Context, int64, string) (bool, error) { return true, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	return hub
}

func attachTestSubscriber(t *testing.T, hub *Hub, userID int64, binding string) *Subscription {
	t.Helper()
	sub, err := hub.Subscribe(userID, binding, 0, false)
	if err != nil {
		t.Fatalf("subscribe user=%d binding=%q: %v", userID, binding, err)
	}
	// Keep the synthetic browser connection alive without allowing its bounded
	// event queue to turn a long live-request test into an accidental detach.
	drained := make(chan struct{})
	go func() {
		for envelope := range sub.Events() {
			envelope.Release()
		}
		close(drained)
	}()
	t.Cleanup(func() {
		sub.Close()
		<-drained
	})
	return sub
}

func TestRedactionPreservesOrderAndNeverRetainsSecrets(t *testing.T) {
	raw := []byte(`{"model":"p/m","Authorization":"Bearer top-secret","nested":{"CALLER_KEY":"nbk_sensitive","value":false},"zero":0,"empty":[]}`)
	projection := RedactJSON(raw, MaxRawRequestBytes)
	if projection.ParseCategory != "valid" || projection.Truncated {
		t.Fatalf("projection=%+v", projection)
	}
	if strings.Contains(string(projection.Data), "top-secret") || strings.Contains(string(projection.Data), "nbk_sensitive") {
		t.Fatalf("redacted data retained a secret: %s", projection.Data)
	}
	if bytes.Index(projection.Data, []byte(`"model"`)) > bytes.Index(projection.Data, []byte(`"nested"`)) {
		t.Fatalf("object order changed: %s", projection.Data)
	}
	var decoded any
	if err := json.Unmarshal(projection.Data, &decoded); err != nil {
		t.Fatalf("redacted JSON is invalid: %v", err)
	}
	invalid := RedactJSON([]byte(`{"x":`), MaxRawRequestBytes)
	if invalid.ParseCategory == "valid" || bytes.Contains(invalid.Data, []byte(`"x"`)) {
		t.Fatalf("invalid projection retained source: %+v", invalid)
	}
	oversized := RedactJSON(bytes.Repeat([]byte{'x'}, MaxRawRequestBytes+1), MaxRawRequestBytes)
	if !oversized.Truncated || bytes.Contains(oversized.Data, []byte{'x'}) {
		t.Fatalf("oversized projection leaked source: %+v", oversized)
	}
}

func TestRedactionCoversNestedStringKeysSSEAndArrayNodeBudget(t *testing.T) {
	projection := RedactJSON([]byte(`{"note":"api_key=sk-nested","nested":{"detail":"safety-identifier=hmac-value"}}`), MaxRawRequestBytes)
	if projection.ParseCategory != "valid" {
		t.Fatalf("nested string projection=%+v", projection)
	}
	if strings.Contains(string(projection.Data), "sk-nested") || strings.Contains(string(projection.Data), "hmac-value") {
		t.Fatalf("nested string secret escaped: %s", projection.Data)
	}
	sse := RedactText([]byte("event: message\ndata: {\"secret\":\"sse-secret\",\"note\":\"api_key=sk-sse\"}\n\ndata: [DONE]\n"), MaxResponseBytes)
	if sse.ParseCategory != "text" || strings.Contains(string(sse.Data), "sse-secret") || strings.Contains(string(sse.Data), "sk-sse") || !strings.Contains(string(sse.Data), "data: [DONE]") {
		t.Fatalf("SSE redaction=%+v data=%q", sse, sse.Data)
	}
	array := []byte("[" + strings.TrimSuffix(strings.Repeat(`"x",`, maxJSONFields+2), ",") + "]")
	if projection := RedactJSON(array, MaxRawRequestBytes); projection.ParseCategory != "field_limit" {
		t.Fatalf("array node budget projection=%+v", projection)
	}
	effective := buildTracePayload(TraceInput{Mode: ModeLive, Effective: []byte(`{"origin":{"state":"not_selected_not_evaluated"},"safety_identifier":{"state":"applied","value_hidden":true},"note":"origin=https://secret.example"}`)})
	encoded, _ := json.Marshal(effective["effective"])
	if !bytes.Contains(encoded, []byte(`"origin":{"state":"not_selected_not_evaluated"}`)) || !bytes.Contains(encoded, []byte(`"safety_identifier":{"state":"applied","value_hidden":true}`)) || bytes.Contains(encoded, []byte("secret.example")) {
		t.Fatalf("effective state projection was not safely preserved/redacted: %s", encoded)
	}
	unsafe := buildTracePayload(TraceInput{Mode: ModeLive, Effective: []byte(`{"origin":{"state":"evaluated","url":"https://secret.example"},"safety_identifier":{"state":"applied","value":"hmac-secret"}}`)})
	unsafeEncoded, _ := json.Marshal(unsafe["effective"])
	if bytes.Contains(unsafeEncoded, []byte("secret.example")) || bytes.Contains(unsafeEncoded, []byte("hmac-secret")) || !bytes.Contains(unsafeEncoded, []byte(`[REDACTED]`)) {
		t.Fatalf("unsafe effective state was not fail-closed: %s", unsafeEncoded)
	}
	unsafeHidden := buildTracePayload(TraceInput{Mode: ModeLive, Effective: []byte(`{"safety_identifier":{"state":"applied","value_hidden":false}}`)})
	unsafeHiddenEncoded, _ := json.Marshal(unsafeHidden["effective"])
	if !bytes.Contains(unsafeHiddenEncoded, []byte(`[REDACTED]`)) {
		t.Fatalf("false value_hidden was accepted: %s", unsafeHiddenEncoded)
	}
	unsafePair := buildTracePayload(TraceInput{Mode: ModeLive, Effective: []byte(`{"store":{"caller_value":true,"effective":{"state":"evaluated","value":false,"extra":"nope"}}}`)})
	unsafePairEncoded, _ := json.Marshal(unsafePair["effective"])
	if !bytes.Contains(unsafePairEncoded, []byte(`"store":"[REDACTED]"`)) {
		t.Fatalf("unknown caller/effective pair was not hidden: %s", unsafePairEncoded)
	}
	pair := buildTracePayload(TraceInput{Mode: ModeLive, ReceivedAt: 1700000000, Effective: []byte(`{"store":{"caller_value":true,"effective":{"state":"applied","value":false}},"flatten_tool_calls":{"caller_value":null,"effective":{"state":"unchanged"}},"safety_identifier":{"caller_value":null,"effective":{"state":"applied","value_hidden":true}}}`)})
	pairEncoded, _ := json.Marshal(pair)
	if !bytes.Contains(pairEncoded, []byte(`"received_at":1700000000`)) ||
		!bytes.Contains(pairEncoded, []byte(`"caller_value"`)) ||
		!bytes.Contains(pairEncoded, []byte(`"effective":{"state":"applied"`)) ||
		bytes.Contains(pairEncoded, []byte(`[REDACTED]`)) {
		t.Fatalf("caller/effective state pair was not preserved safely: %s", pairEncoded)
	}
}

func TestTraceResponseSectionCarriesTruncationAccounting(t *testing.T) {
	input := TraceInput{
		Mode: ModeLive, Model: "public/model", Response: []byte(strings.Repeat("x", MaxResponseBytes)),
		ResponseOriginalBytes: MaxResponseBytes + 17, ResponseCapturedBytes: MaxResponseBytes, ResponseTruncated: true,
	}
	payload := buildTracePayload(input)
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var projection map[string]any
	if err := json.Unmarshal(encoded, &projection); err != nil {
		t.Fatal(err)
	}
	response, ok := projection["response"].(map[string]any)
	if !ok || response["truncated"] != true || response["original_bytes"] != float64(MaxResponseBytes+17) || response["captured_bytes"] != float64(MaxResponseBytes) {
		t.Fatalf("response truncation projection=%s", encoded)
	}
}

func TestHubChallengeIsSingleUseAndBoundToGeneration(t *testing.T) {
	hub := testHub(t)
	metadata, err := hub.Start(7, "binding-a")
	if err != nil || metadata.Mode != ModeDry || metadata.Generation != 1 {
		t.Fatalf("start metadata=%+v err=%v", metadata, err)
	}
	confirmation, err := hub.IssueChallenge(7, "binding-a")
	if err != nil || confirmation == "" {
		t.Fatalf("challenge=%q err=%v", confirmation, err)
	}
	if _, err := hub.SetMode(7, "binding-b", ModeLive, confirmation); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong binding err=%v", err)
	}
	attachTestSubscriber(t, hub, 7, "binding-a")
	metadata, err = hub.SetMode(7, "binding-a", ModeLive, confirmation)
	if err != nil || metadata.Mode != ModeLive {
		t.Fatalf("live metadata=%+v err=%v", metadata, err)
	}
	if _, err := hub.SetMode(7, "binding-a", ModeLive, confirmation); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("confirmation replay err=%v", err)
	}
	metadata, err = hub.SetMode(7, "binding-a", ModeDry, "")
	if err != nil || metadata.Mode != ModeDry {
		t.Fatalf("dry metadata=%+v err=%v", metadata, err)
	}
	newMetadata, err := hub.Start(7, "binding-a")
	if err != nil || newMetadata.Generation != 2 || newMetadata.Mode != ModeDry {
		t.Fatalf("replacement metadata=%+v err=%v", newMetadata, err)
	}
	if _, err := hub.SetMode(7, "binding-a", ModeLive, confirmation); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("old-generation confirmation err=%v", err)
	}
}

func TestHubLiveTransitionRequiresCurrentSubscriberAndDetachForcesDry(t *testing.T) {
	hub := testHub(t)
	if _, err := hub.Start(701, "live-order"); err != nil {
		t.Fatal(err)
	}
	confirmation, err := hub.IssueChallenge(701, "live-order")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.SetMode(701, "live-order", ModeLive, confirmation); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("live without subscriber err=%v", err)
	}
	// A failed no-subscriber attempt consumes the presented challenge; a new
	// browser confirmation is required after the stream is attached.
	sub := attachTestSubscriber(t, hub, 701, "live-order")
	if _, err := hub.SetMode(701, "live-order", ModeLive, confirmation); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("consumed disconnected challenge err=%v", err)
	}
	confirmation, err = hub.IssueChallenge(701, "live-order")
	if err != nil {
		t.Fatal(err)
	}
	if metadata, err := hub.SetMode(701, "live-order", ModeLive, confirmation); err != nil || metadata.Mode != ModeLive {
		t.Fatalf("live with subscriber metadata=%+v err=%v", metadata, err)
	}

	// The detach and mode operations use the same Hub mutex.  When detach wins,
	// the live transition is rejected; when live wins, detach immediately
	// publishes the authoritative dry state.
	sub.Close()
	confirmation, err = hub.IssueChallenge(701, "live-order")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.SetMode(701, "live-order", ModeLive, confirmation); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("detach-before-live err=%v", err)
	}
	sub = attachTestSubscriber(t, hub, 701, "live-order")
	confirmation, err = hub.IssueChallenge(701, "live-order")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.SetMode(701, "live-order", ModeLive, confirmation); err != nil {
		t.Fatal(err)
	}
	sub.Close()
	metadata, ok := hub.Metadata(701, "live-order")
	if !ok || metadata.Mode != ModeDry || metadata.Connected {
		t.Fatalf("detach-after-live metadata=%+v ok=%v", metadata, ok)
	}
}

func TestHubGenerationIsProcessMonotonicAndConfirmationSlotIsBounded(t *testing.T) {
	hub := testHub(t)
	first, err := hub.Start(101, "binding-a")
	if err != nil || first.Generation != 1 {
		t.Fatalf("first generation=%+v err=%v", first, err)
	}
	second, err := hub.Start(102, "binding-b")
	if err != nil || second.Generation != 2 {
		t.Fatalf("second generation=%+v err=%v", second, err)
	}
	third, err := hub.Start(101, "binding-a")
	if err != nil || third.Generation != 3 {
		t.Fatalf("replacement generation=%+v err=%v", third, err)
	}
	for i := 0; i < 20; i++ {
		if _, err := hub.IssueChallenge(101, "binding-a"); err != nil {
			t.Fatal(err)
		}
	}
	hub.mu.Lock()
	confirmationCount := 0
	for _, item := range hub.confirmations {
		if item.userID == 101 {
			confirmationCount++
		}
	}
	hub.mu.Unlock()
	if confirmationCount != 1 {
		t.Fatalf("confirmation slots for one session=%d, want 1", confirmationCount)
	}
}

func TestHubMetadataAdvertisesEffectiveCompleteLimits(t *testing.T) {
	const (
		configuredSessions = 3
		configuredHubBytes = 8 << 20
		configuredSession  = 1 << 20
	)
	hub, err := NewHub(Config{
		MaxSessions: configuredSessions, MaxHubBytes: configuredHubBytes, MaxSessionBytes: configuredSession,
		SessionBindingValidator: func(context.Context, int64, string) (bool, error) { return true, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	metadata, err := hub.Start(109, "limits-binding")
	if err != nil {
		t.Fatal(err)
	}
	want := Limits{
		MaxSessions: configuredSessions, HubBytes: configuredHubBytes, SessionBytes: configuredSession,
		MaxTraces: MaxTraces, MaxEvents: MaxRetainedEvents, EventBytes: MaxEventBytes,
		SubscriberQueue: MaxSubscriberQueue, MaxSubscribers: MaxSubscribers,
		RawRequestBytes: MaxRawRequestBytes, MessagesToolsBytes: MaxMessagesToolsBytes,
		ParametersBytes: MaxParametersBytes, EffectiveSummaryBytes: MaxEffectiveSummaryBytes,
		ResponseBytes: MaxResponseBytes, TraceBytes: MaxTraceBytes,
		FirstAttachSeconds: int64(FirstAttachTimeout / time.Second), ReconnectSeconds: int64(ReconnectGrace / time.Second),
		IdleSeconds: int64(IdleTimeout / time.Second), AbsoluteSeconds: int64(AbsoluteLifetime / time.Second),
		HeartbeatSeconds: int64(HeartbeatInterval / time.Second), WriteDeadlineSeconds: int64(WriteDeadline / time.Second),
		ConfirmationSeconds: int64(ConfirmationLifetime / time.Second),
	}
	if metadata.Limits != want {
		t.Fatalf("metadata limits=%+v, want %+v", metadata.Limits, want)
	}
	hub.mu.Lock()
	if len(hub.sessions[109].events) == 0 {
		hub.mu.Unlock()
		t.Fatal("initial snapshot was not retained")
	}
	snapshot := hub.sessions[109].events[0].envelope
	hub.mu.Unlock()
	if snapshot.Type != EventSessionSnapshot {
		t.Fatalf("initial event type=%s, want snapshot", snapshot.Type)
	}
	if snapshotMetadata, ok := snapshot.Payload["metadata"].(SessionMetadata); !ok || snapshotMetadata.LastEventID != snapshot.Seq {
		t.Fatalf("snapshot metadata cursor=%+v seq=%d", snapshot.Payload["metadata"], snapshot.Seq)
	}
	wire, err := json.Marshal(metadata.Limits)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"max_sessions", "hub_bytes", "session_bytes", "event_bytes", "effective_summary_bytes", "heartbeat_seconds", "write_deadline_seconds", "confirmation_seconds"} {
		if !bytes.Contains(wire, []byte(`"`+field+`"`)) {
			t.Fatalf("limits wire omitted %q: %s", field, wire)
		}
	}
}

func TestHubStartPreflightsSnapshotAndPreservesOldOnCapacityFailure(t *testing.T) {
	hub, err := NewHub(Config{
		MaxHubBytes:             1024,
		SessionBindingValidator: func(context.Context, int64, string) (bool, error) { return true, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	old, err := hub.Start(106, "old-binding")
	if err != nil {
		t.Fatalf("initial start err=%v", err)
	}
	if _, err := hub.Start(106, "new-binding"); !errors.Is(err, ErrCapacity) {
		t.Fatalf("replacement err=%v, want capacity", err)
	}
	metadata, ok := hub.Metadata(106, "old-binding")
	if !ok || metadata.ID != old.ID || metadata.Generation != old.Generation {
		t.Fatalf("old session was not preserved metadata=%+v ok=%v old=%+v", metadata, ok, old)
	}
	if _, ok := hub.Metadata(106, "new-binding"); ok {
		t.Fatal("failed replacement became active")
	}

	small, err := NewHub(Config{MaxSessionBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer small.Close()
	if _, err := small.Start(107, "tiny"); !errors.Is(err, ErrCapacity) {
		t.Fatalf("tiny snapshot start err=%v, want capacity", err)
	}
	if _, ok := small.Metadata(107, "tiny"); ok {
		t.Fatal("capacity-failed tiny session became active")
	}
}

func TestUnknownSessionEndReasonFailsClosed(t *testing.T) {
	hub := testHub(t)
	if _, err := hub.Start(114, "reason-binding"); err != nil {
		t.Fatal(err)
	}
	sub, err := hub.Subscribe(114, "reason-binding", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	initial, open := receiveDebugEvent(t, sub)
	if !open {
		t.Fatal("session snapshot closed before delivery")
	}
	initial.Release()
	hub.ForgetUserReason(114, SessionEndReason("untrusted-reason"))
	envelope, open := receiveDebugEvent(t, sub)
	if !open || envelope.Type != EventSessionEnd || envelope.Payload["reason"] != string(EndSessionInvalid) {
		t.Fatalf("unknown reason envelope=%+v open=%v", envelope, open)
	}
	envelope.Release()
}

func TestBoundLoginValidationRevokesLiveAndFailsClosedToDry(t *testing.T) {
	var state atomic.Int32
	state.Store(1)
	hub, err := NewHub(Config{SessionBindingValidator: func(context.Context, int64, string) (bool, error) {
		switch state.Load() {
		case 1:
			return true, nil
		case 2:
			return false, errors.New("session store unavailable")
		default:
			return false, nil
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	if _, err := hub.Start(8, "login-hash"); err != nil {
		t.Fatal(err)
	}
	confirmation, err := hub.IssueChallenge(8, "login-hash")
	if err != nil {
		t.Fatal(err)
	}
	attachTestSubscriber(t, hub, 8, "login-hash")
	if _, err := hub.SetMode(8, "login-hash", ModeLive, confirmation); err != nil {
		t.Fatal(err)
	}
	if mode, _, _, active := hub.ModeForCallerContext(context.Background(), 8); !active || mode != ModeLive {
		t.Fatalf("valid binding mode=%s active=%v", mode, active)
	}
	state.Store(2)
	if mode, _, _, active := hub.ModeForCallerContext(context.Background(), 8); !active || mode != ModeDry {
		t.Fatalf("uncertain binding mode=%s active=%v", mode, active)
	}
	if metadata, ok := hub.Metadata(8, "login-hash"); !ok || metadata.Mode != ModeDry {
		t.Fatalf("uncertain metadata=%+v ok=%v", metadata, ok)
	}
	state.Store(0)
	if mode, _, _, active := hub.ModeForCallerContext(context.Background(), 8); active || mode != ModeDry {
		t.Fatalf("revoked binding mode=%s active=%v", mode, active)
	}
	if _, ok := hub.Metadata(8, "login-hash"); ok {
		t.Fatal("revoked binding retained session")
	}
}

func TestSweepClosesExpiredReconnectGraceWithoutTimer(t *testing.T) {
	clock := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	hub, err := NewHub(Config{
		Now:                     func() time.Time { return clock },
		SessionBindingValidator: func(context.Context, int64, string) (bool, error) { return true, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	if _, err := hub.Start(86, "reconnect-clock"); err != nil {
		t.Fatal(err)
	}
	sub, err := hub.Subscribe(86, "reconnect-clock", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	initial, open := receiveDebugEvent(t, sub)
	if !open || initial.Type != EventSessionSnapshot {
		t.Fatalf("initial event=%+v open=%v", initial, open)
	}
	initial.Release()
	sub.Close()
	hub.mu.Lock()
	session := hub.sessions[86]
	if session == nil || session.reconnectExpires.IsZero() {
		hub.mu.Unlock()
		t.Fatal("detached session did not retain reconnect deadline")
	}
	expires := session.reconnectExpires
	if session.reconnectTimer != nil {
		session.reconnectTimer.Stop()
		session.reconnectTimer = nil
	}
	hub.mu.Unlock()
	// Sweep is intentionally the only expiry mechanism exercised here; the
	// runtime timer is stopped to prove the synchronous deadline check.
	hub.Sweep(expires)
	if _, ok := hub.Metadata(86, "reconnect-clock"); ok {
		t.Fatal("expired reconnect grace remained active after Sweep")
	}
}

func TestHubSubscriberReplayAndReplacementTerminal(t *testing.T) {
	hub := testHub(t)
	if _, err := hub.Start(1, "a"); err != nil {
		t.Fatal(err)
	}
	sub, err := hub.Subscribe(1, "a", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	first, open := receiveDebugEvent(t, sub)
	if !open {
		t.Fatal("subscriber closed before initial snapshot")
	}
	if first.Type != EventSessionSnapshot || first.Seq == 0 {
		t.Fatalf("first event=%+v", first)
	}
	if !hub.PublishTrace(1, "a", Trace{ID: "trace-1", Revision: 1, Payload: map[string]any{"secret": "must-not-appear", "ok": "value"}}) {
		t.Fatal("trace was not accepted")
	}
	trace, open := receiveDebugEvent(t, sub)
	if !open {
		t.Fatal("subscriber closed before trace projection")
	}
	if trace.Type != EventTraceUpsert || trace.Revision != 1 || trace.Payload["trace"] == nil {
		t.Fatalf("trace event=%+v", trace)
	}
	encoded, _ := json.Marshal(trace)
	if strings.Contains(string(encoded), "must-not-appear") {
		t.Fatalf("trace event retained secret: %s", encoded)
	}
	if _, err := hub.Start(1, "a"); err != nil {
		t.Fatal(err)
	}
	terminal, open := receiveDebugEvent(t, sub)
	if !open || terminal.Type != EventSessionEnd || terminal.Payload["reason"] != string(EndReplaced) {
		t.Fatalf("replacement terminal=%+v open=%v", terminal, open)
	}
	if _, open := receiveDebugEvent(t, sub); open {
		t.Fatal("replacement subscriber remained open")
	}
	sub.Close()
}

func TestHubReplayGapDoesNotReemitEvictedRingAndConnectedSnapshot(t *testing.T) {
	hub := testHub(t)
	if _, err := hub.Start(13, "replay-binding"); err != nil {
		t.Fatal(err)
	}
	sub, err := hub.Subscribe(13, "replay-binding", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	first, open := receiveDebugEvent(t, sub)
	if !open {
		t.Fatal("subscriber closed before initial snapshot")
	}
	firstWire, _ := json.Marshal(first)
	if !strings.Contains(string(firstWire), `"connected":true`) {
		t.Fatalf("initial snapshot did not advertise connected=true: %s", firstWire)
	}
	sub.Close()
	for index := 0; index < MaxRetainedEvents+8; index++ {
		if !hub.PublishTrace(13, "replay-binding", Trace{ID: "replay-" + strconv.Itoa(index), Revision: 1, Terminal: true, Payload: map[string]any{"index": index}}) {
			t.Fatalf("trace %d was not accepted", index)
		}
	}
	resumed, err := hub.Subscribe(13, "replay-binding", 1, true)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	var replay []EventEnvelope
	for {
		select {
		case event, open := <-resumed.Events():
			if !open {
				goto done
			}
			replay = append(replay, event)
		default:
			goto done
		}
	}
done:
	if len(replay) != 2 || replay[0].Type != EventGap || replay[1].Type != EventSessionSnapshot {
		t.Fatalf("stale replay=%+v, want only gap+snapshot", replay)
	}
	if wire, _ := json.Marshal(replay[1]); !strings.Contains(string(wire), `"connected":true`) {
		t.Fatalf("reconnect snapshot did not advertise connected=true: %s", wire)
	}
}

func TestHubQueuedEventCopiesAreBoundedAndReleasedOnSlowClose(t *testing.T) {
	hub := testHub(t)
	if _, err := hub.Start(103, "slow-binding"); err != nil {
		t.Fatal(err)
	}
	sub, err := hub.Subscribe(103, "slow-binding", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	initial, open := receiveDebugEvent(t, sub)
	if !open {
		t.Fatal("slow subscriber closed before initial snapshot")
	}
	initial.Release()
	large := strings.Repeat("x", 4096)
	for revision := 1; revision <= MaxRetainedEvents+32; revision++ {
		accepted := hub.PublishTrace(103, "slow-binding", Trace{
			ID: "slow-trace", Revision: uint64(revision), Payload: map[string]any{
				"revision": revision, "body": large,
			},
		})
		if !accepted {
			hub.mu.Lock()
			state := hub.sessions[103]
			t.Fatalf("revision %d was unexpectedly rejected session=%+v maxSession=%d maxHub=%d total=%d", revision, state, hub.maxSessionBytes, hub.maxHubBytes, hub.totalBytes)
		}
	}
	hub.mu.Lock()
	session := hub.sessions[103]
	if session == nil || session.bytes > hub.maxSessionBytes || hub.totalBytes > hub.maxHubBytes {
		hub.mu.Unlock()
		t.Fatalf("slow queue exceeded memory cap: session=%v total=%d", session, hub.totalBytes)
	}
	hub.mu.Unlock()
	// The bounded queue is intentionally never allowed to silently skip a
	// copy. Once it fills, the subscriber receives one explicit gap and EOF;
	// this is the ordinary (non-fragment) slow-consumer path.
	gap, open := receiveDebugEvent(t, sub)
	if !open || gap.Type != EventGap {
		t.Fatalf("slow ordinary subscriber did not receive gap: %+v open=%v", gap, open)
	}
	gap.Release()
	if _, open := receiveDebugEvent(t, sub); open {
		t.Fatal("slow ordinary subscriber remained open after copy drop")
	}
	sub.Close()
	_ = hub.Close()
	hub.mu.Lock()
	remaining := hub.totalBytes
	hub.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("close retained event/trace bytes=%d", remaining)
	}
}

func TestHubLargeTraceAndSnapshotFragmentsAreBoundedAndReconstructable(t *testing.T) {
	hub := testHub(t)
	if _, err := hub.Start(105, "fragment-binding"); err != nil {
		t.Fatal(err)
	}
	sub, err := hub.Subscribe(105, "fragment-binding", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	initial, open := receiveDebugEvent(t, sub)
	if !open {
		t.Fatal("fragment subscriber closed before initial snapshot")
	}
	initial.Release()
	large := strings.Repeat("z", MaxFragmentBytes*2)
	if !hub.PublishTrace(105, "fragment-binding", Trace{ID: "fragment-trace", Revision: 1, Payload: map[string]any{"body": large}}) {
		t.Fatal("large trace was rejected")
	}
	var fragments []EventEnvelope
	firstFragment, open := receiveDebugEvent(t, sub)
	if !open {
		t.Fatal("fragment subscriber closed before first fragment")
	}
	fragmentValue, ok := firstFragment.Payload["fragment"].(map[string]any)
	if !ok {
		t.Fatalf("first trace event was not a fragment: %+v", firstFragment.Payload)
	}
	fragmentCount, ok := fragmentValue["count"].(int)
	if !ok || fragmentCount < 1 || fragmentCount > MaxRetainedEvents {
		t.Fatalf("invalid fragment count=%T/%v", fragmentValue["count"], fragmentValue["count"])
	}
	for index := 0; index < fragmentCount; index++ {
		event := firstFragment
		if index != 0 {
			event, open = receiveDebugEvent(t, sub)
			if !open {
				t.Fatalf("fragment subscriber closed at index %d", index)
			}
		}
		if event.Type != EventTraceUpsert {
			t.Fatalf("trace fragment event=%+v", event)
		}
		wire, _ := json.Marshal(event)
		if len(wire) > MaxEventBytes {
			t.Fatalf("trace fragment exceeded event cap=%d", len(wire))
		}
		if _, ok := event.Payload["fragment"].(map[string]any); !ok {
			t.Fatalf("trace event was not explicit fragment=%+v", event.Payload)
		}
		fragments = append(fragments, event)
	}
	var assembled []byte
	var digest string
	var total int
	for _, event := range fragments {
		fragment := event.Payload["fragment"].(map[string]any)
		if fragment["kind"] != "trace" {
			t.Fatalf("fragment kind=%v", fragment["kind"])
		}
		if digest == "" {
			digest = fragment["sha256"].(string)
			switch value := fragment["total_bytes"].(type) {
			case int:
				total = value
			case float64:
				total = int(value)
			default:
				t.Fatalf("fragment total_bytes type=%T", fragment["total_bytes"])
			}
		}
		data, ok := fragment["data"].(fragmentData)
		if !ok {
			t.Fatalf("fragment data type=%T", fragment["data"])
		}
		chunk := append([]byte(nil), data...)
		assembled = append(assembled, chunk...)
	}
	hash := sha256.Sum256(assembled)
	if len(assembled) != total || hex.EncodeToString(hash[:]) != digest {
		t.Fatalf("fragment reconstruction bytes=%d total=%d digest=%s", len(assembled), total, digest)
	}
	for _, event := range fragments {
		event.Release()
	}
	sub.Close()
	hub.mu.Lock()
	snapshotOK := hub.emitSnapshotLocked(hub.sessions[105])
	snapshotSession := hub.sessions[105]
	snapshotEvents := len(snapshotSession.events)
	snapshotBytes := snapshotSession.bytes
	snapshotDropped := snapshotSession.dropped
	var snapshots []EventEnvelope
	for _, stored := range snapshotSession.events {
		if stored != nil && stored.envelope.Type == EventSessionSnapshot {
			snapshots = append(snapshots, stored.envelope)
		}
	}
	hub.mu.Unlock()
	if !snapshotOK {
		t.Fatalf("snapshot fragments rejected events=%d bytes=%d dropped=%d", snapshotEvents, snapshotBytes, snapshotDropped)
	}
	if len(snapshots) < 3 {
		t.Fatalf("snapshot fragments retained=%d events=%d bytes=%d", len(snapshots), snapshotEvents, snapshotBytes)
	}
	for _, event := range snapshots[len(snapshots)-3:] {
		if event.Type != EventSessionSnapshot {
			t.Fatalf("snapshot fragment event=%+v", event)
		}
		wire, _ := json.Marshal(event)
		if len(wire) > MaxEventBytes {
			t.Fatalf("snapshot fragment exceeded event cap=%d", len(wire))
		}
		event.Release()
	}
}

func TestHubFragmentGroupBudgetFailureIsAtomicAndRecovers(t *testing.T) {
	hub, err := NewHub(Config{MaxSessionBytes: 900 << 10, MaxHubBytes: 2 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	if _, err := hub.Start(112, "fragment-cap"); err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("q", MaxFragmentBytes*2)
	if hub.PublishTrace(112, "fragment-cap", Trace{ID: "atomic-fragment", Revision: 1, Payload: map[string]any{"body": large}}) {
		t.Fatal("fragment group unexpectedly exceeded the session budget")
	}
	hub.mu.Lock()
	session := hub.sessions[112]
	if session == nil {
		hub.mu.Unlock()
		t.Fatal("fragment failure removed the session")
	}
	if _, ok := session.traces["atomic-fragment"]; ok {
		hub.mu.Unlock()
		t.Fatal("failed fragment trace remained in the authoritative projection")
	}
	for _, event := range session.events {
		if event == nil || event.envelope.TraceID != "atomic-fragment" {
			continue
		}
		if event.envelope.Type == EventTraceUpsert {
			hub.mu.Unlock()
			t.Fatalf("partial trace fragment remained seq=%d", event.envelope.Seq)
		}
	}
	if session.bytes > hub.maxSessionBytes || hub.totalBytes > hub.maxHubBytes {
		bytes, total := session.bytes, hub.totalBytes
		hub.mu.Unlock()
		t.Fatalf("fragment failure leaked budget session=%d total=%d", bytes, total)
	}
	var hasGap, hasSnapshot bool
	for _, event := range session.events {
		if event == nil {
			continue
		}
		hasGap = hasGap || event.envelope.Type == EventGap
		hasSnapshot = hasSnapshot || event.envelope.Type == EventSessionSnapshot
	}
	hub.mu.Unlock()
	if !hasGap || !hasSnapshot {
		t.Fatalf("fragment failure lacked gap+snapshot recovery gap=%v snapshot=%v", hasGap, hasSnapshot)
	}
}

func TestHubLargeSnapshotCompactsSupersededEventsForRecovery(t *testing.T) {
	hub := testHub(t)
	if _, err := hub.Start(113, "snapshot-compact"); err != nil {
		t.Fatal(err)
	}
	first, err := hub.Subscribe(113, "snapshot-compact", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	initial, open := receiveDebugEvent(t, first)
	if !open {
		t.Fatal("first subscriber closed before initial snapshot")
	}
	initial.Release()
	second, err := hub.Subscribe(113, "snapshot-compact", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	initial, open = receiveDebugEvent(t, second)
	if !open {
		t.Fatal("second subscriber closed before initial snapshot")
	}
	initial.Release()
	large := strings.Repeat("s", MaxTraceBytes-128)
	if !hub.PublishTrace(113, "snapshot-compact", Trace{ID: "large-terminal", Revision: 1, Terminal: true, Payload: map[string]any{"body": large}}) {
		t.Fatal("large terminal trace was rejected")
	}
	// Release the two original trace fragment deliveries so the test models
	// slow consumers only through the ring, then ask for a current snapshot.
	connectedToTrace := make([]bool, 0, 2)
	for _, sub := range []*Subscription{first, second} {
		event, open := receiveDebugEvent(t, sub)
		if !open {
			connectedToTrace = append(connectedToTrace, false)
			continue
		}
		if event.Type == EventGap {
			// Near the 4 MiB session cap a slow subscriber may be dropped as
			// soon as the complete fragment group is committed. It must see an
			// explicit gap and EOF, never silently continue with a later patch.
			event.Release()
			if _, open := receiveDebugEvent(t, sub); open {
				t.Fatal("dropped subscriber remained open after gap")
			}
			connectedToTrace = append(connectedToTrace, false)
			continue
		}
		fragment, ok := event.Payload["fragment"].(map[string]any)
		if !ok {
			t.Fatalf("large trace event=%+v", event.Payload)
		}
		count, ok := fragment["count"].(int)
		if !ok || count < 1 || count > MaxRetainedEvents {
			t.Fatalf("large trace count=%T/%v", fragment["count"], fragment["count"])
		}
		event.Release()
		for index := 1; index < count; index++ {
			event, open = receiveDebugEvent(t, sub)
			if !open {
				t.Fatalf("large trace subscriber closed at fragment %d", index)
			}
			event.Release()
		}
		connectedToTrace = append(connectedToTrace, true)
	}
	hub.mu.Lock()
	session := hub.sessions[113]
	if session == nil || !hub.emitSnapshotLocked(session) {
		hub.mu.Unlock()
		t.Fatal("large snapshot could not compact superseded history")
	}
	if session.bytes > hub.maxSessionBytes || hub.totalBytes+hub.requestCopyBytes > hub.maxHubBytes {
		bytes, total := session.bytes, hub.totalBytes
		hub.mu.Unlock()
		t.Fatalf("snapshot compaction exceeded cap session=%d total=%d", bytes, total)
	}
	var snapshotCount, gapCount int
	for _, event := range session.events {
		if event == nil {
			continue
		}
		snapshotCount += boolInt(event.envelope.Type == EventSessionSnapshot)
		gapCount += boolInt(event.envelope.Type == EventGap)
	}
	hub.mu.Unlock()
	if snapshotCount == 0 || gapCount == 0 {
		t.Fatalf("compaction recovery events snapshot=%d gap=%d", snapshotCount, gapCount)
	}
	for index, sub := range []*Subscription{first, second} {
		if !connectedToTrace[index] {
			continue
		}
		envelope, open := receiveDebugEvent(t, sub)
		if !open || envelope.Type != EventGap {
			t.Fatalf("compacted subscriber %d did not receive deterministic gap envelope=%+v open=%v", index, envelope, open)
		}
		envelope.Release()
		if _, open := receiveDebugEvent(t, sub); open {
			t.Fatalf("compacted subscriber %d remained open after gap", index)
		}
	}
	// A fragmented recovery snapshot must carry the same connected=true
	// attach-local metadata as a plain snapshot; the bit is inside the
	// base64url-json fragment payload, not only in the outer transport state.
	reconnect, err := hub.Subscribe(113, "snapshot-compact", 1, true)
	if err != nil {
		t.Fatalf("fragmented reconnect failed: %v", err)
	}
	defer reconnect.Close()
	var snapshotParts map[int]fragmentData
	fragmentCount := 0
	for snapshotParts == nil || len(snapshotParts) < fragmentCount {
		event, open := receiveDebugEvent(t, reconnect)
		if !open {
			t.Fatal("fragmented reconnect closed before snapshot")
		}
		if event.Type != EventSessionSnapshot {
			event.Release()
			continue
		}
		fragment, ok := event.Payload["fragment"].(map[string]any)
		if !ok {
			event.Release()
			t.Fatalf("reconnect snapshot was not fragmented")
		}
		index, okIndex := fragment["index"].(int)
		count, okCount := fragment["count"].(int)
		data, okData := fragment["data"].(fragmentData)
		if !okIndex || !okCount || !okData || count < 1 || count > MaxRetainedEvents || index < 0 || index >= count {
			event.Release()
			t.Fatalf("invalid reconnect fragment=%v", fragment)
		}
		if snapshotParts == nil {
			snapshotParts = make(map[int]fragmentData, count)
			fragmentCount = count
		}
		snapshotParts[index] = append(fragmentData(nil), data...)
		event.Release()
	}
	assembled := make([]byte, 0)
	for index := 0; index < fragmentCount; index++ {
		assembled = append(assembled, snapshotParts[index]...)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(assembled, &snapshot); err != nil {
		t.Fatalf("fragmented reconnect snapshot JSON: %v", err)
	}
	metadata, ok := snapshot["metadata"].(map[string]any)
	if !ok || metadata["connected"] != true {
		t.Fatalf("fragmented reconnect metadata=%v", snapshot["metadata"])
	}
	first.Close()
	second.Close()
}

func TestMarkIncompleteReplacesTinyReceivedProjection(t *testing.T) {
	hub := testHub(t)
	if _, err := hub.Start(116, "incomplete-marker"); err != nil {
		t.Fatal(err)
	}
	hub.mu.Lock()
	session := hub.sessions[116]
	wire := []byte(`{"terminal":"received"}`)
	record := &traceRecord{id: "tiny", revision: 1, wire: append([]byte(nil), wire...)}
	session.traces[record.id] = record
	session.traceOrder = append(session.traceOrder, record.id)
	session.bytes += int64(len(record.wire))
	hub.totalBytes += int64(len(record.wire))
	hub.markIncompleteTraceLocked(session, record)
	actual := append([]byte(nil), record.wire...)
	hub.mu.Unlock()
	var projection map[string]any
	if err := json.Unmarshal(actual, &projection); err != nil {
		t.Fatal(err)
	}
	if projection["terminal"] != "incomplete" || !record.terminal {
		t.Fatalf("tiny marker projection=%s terminal=%v", actual, record.terminal)
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func receiveDebugEvent(t *testing.T, sub *Subscription) (EventEnvelope, bool) {
	t.Helper()
	if sub == nil {
		return EventEnvelope{}, false
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case envelope, open := <-sub.Events():
		return envelope, open
	case <-timer.C:
		t.Fatal("timed out waiting for debug event")
		return EventEnvelope{}, false
	}
}

func TestHubFragmentMemoryIsCountedAndClearedAfterClose(t *testing.T) {
	hub := testHub(t)
	if _, err := hub.Start(109, "fragment-memory"); err != nil {
		t.Fatal(err)
	}
	sub, err := hub.Subscribe(109, "fragment-memory", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	initial, open := receiveDebugEvent(t, sub)
	if !open {
		t.Fatal("fragment-memory subscriber closed before initial snapshot")
	}
	held := []EventEnvelope{initial}
	large := strings.Repeat("m", MaxFragmentBytes*2)
	if !hub.PublishTrace(109, "fragment-memory", Trace{ID: "large", Payload: map[string]any{"body": large}}) {
		t.Fatal("large trace was rejected")
	}
	firstFragment, open := receiveDebugEvent(t, sub)
	if !open {
		t.Fatal("fragment-memory subscriber closed before first fragment")
	}
	held = append(held, firstFragment)
	fragment, ok := firstFragment.Payload["fragment"].(map[string]any)
	if !ok {
		t.Fatalf("fragment-memory event=%+v", firstFragment.Payload)
	}
	count, ok := fragment["count"].(int)
	if !ok || count < 1 || count > MaxRetainedEvents {
		t.Fatalf("fragment-memory count=%T/%v", fragment["count"], fragment["count"])
	}
	for index := 1; index < count; index++ {
		event, open := receiveDebugEvent(t, sub)
		if !open {
			t.Fatalf("fragment-memory subscriber closed at index %d", index)
		}
		held = append(held, event)
	}
	hub.mu.Lock()
	session := hub.sessions[109]
	if session == nil || session.bytes > hub.maxSessionBytes || hub.totalBytes > hub.maxHubBytes {
		sessionBytes := int64(0)
		if session != nil {
			sessionBytes = session.bytes
		}
		hub.mu.Unlock()
		t.Fatalf("fragment memory exceeded cap session=%d total=%d", sessionBytes, hub.totalBytes)
	}
	hub.mu.Unlock()
	if err := hub.Close(); err != nil {
		t.Fatal(err)
	}
	hub.mu.Lock()
	remaining := hub.totalBytes
	hub.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("close retained fragment bytes=%d", remaining)
	}
	for _, envelope := range held {
		if fragment, ok := envelope.Payload["fragment"].(map[string]any); ok {
			data, ok := fragment["data"].(fragmentData)
			if !ok {
				continue
			}
			before := append([]byte(nil), data...)
			envelope.Release()
			if len(before) != 0 && !bytes.Equal(data, make([]byte, len(before))) {
				t.Fatal("fragment delivery bytes were not cleared on release")
			}
		} else {
			envelope.Release()
		}
	}
}

func TestTraceFragmentRevisionsAdvancePastFinalFragmentForActualPublisher(t *testing.T) {
	hub := testHub(t)
	if _, err := hub.Start(110, "revision-binding"); err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("r", MaxFragmentBytes*2)
	if !hub.PublishTrace(110, "revision-binding", Trace{ID: "revisioned", Revision: 1, Payload: map[string]any{"body": large}}) {
		t.Fatal("first large trace was rejected")
	}
	hub.mu.Lock()
	firstFinal := hub.sessions[110].traces["revisioned"].revision
	firstSeq := hub.sessions[110].nextSeq
	hub.mu.Unlock()
	if firstFinal < 3 {
		t.Fatalf("first fragment final revision=%d, want at least 3", firstFinal)
	}
	if !hub.PublishTrace(110, "revision-binding", Trace{ID: "revisioned", Revision: 2, Payload: map[string]any{"body": large}}) {
		t.Fatal("second large trace was rejected")
	}
	hub.mu.Lock()
	secondFinal := hub.sessions[110].traces["revisioned"].revision
	var secondRevisions []uint64
	for _, event := range hub.sessions[110].events {
		if event != nil && event.seq > firstSeq && event.envelope.Type == EventTraceUpsert && event.envelope.TraceID == "revisioned" {
			secondRevisions = append(secondRevisions, event.envelope.Revision)
		}
	}
	hub.mu.Unlock()
	if secondFinal <= firstFinal || len(secondRevisions) < 3 {
		t.Fatalf("revisions first=%d second=%d events=%v", firstFinal, secondFinal, secondRevisions)
	}
	for _, revision := range secondRevisions {
		if revision <= firstFinal {
			t.Fatalf("second fragment revision=%d regressed below first final=%d", revision, firstFinal)
		}
	}
}

func TestLateObserverFillsOnlySafeTerminalMetadata(t *testing.T) {
	hub := testHub(t)
	if _, err := hub.Start(104, "late-binding"); err != nil {
		t.Fatal(err)
	}
	accepted := hub.PublishTrace(104, "late-binding", Trace{ID: "late-trace", Revision: 1, Payload: map[string]any{
		"terminal": "completed", "status": 200, "response": "caller response", "error": nil,
	}, Terminal: true})
	if !accepted {
		hub.mu.Lock()
		state := hub.sessions[104]
		t.Fatalf("terminal projection was not accepted session=%+v total=%d", state, hub.totalBytes)
	}
	if !hub.PublishTrace(104, "late-binding", Trace{ID: "late-trace", Revision: 2, Merge: true, Payload: map[string]any{
		"effective":     map[string]any{"connector": map[string]any{"name": "openai-compatible"}},
		"attempt_index": 3, "commit_stage": "after_commit", "usage": map[string]any{"input": 1},
		"attempt_state": "failed", "error": "upstream",
	}}) {
		t.Fatal("late observer projection was not accepted")
	}
	hub.mu.Lock()
	wire := append([]byte(nil), hub.sessions[104].traces["late-trace"].wire...)
	hub.mu.Unlock()
	var projection map[string]any
	if err := json.Unmarshal(wire, &projection); err != nil {
		t.Fatal(err)
	}
	if projection["terminal"] != "completed" || projection["status"] != float64(200) || projection["response"] != "caller response" || projection["error"] != nil {
		t.Fatalf("late observer changed logical terminal fields: %s", wire)
	}
	if projection["effective"] == nil || projection["attempt_index"] != float64(3) || projection["usage"] == nil {
		t.Fatalf("late observer safe metadata was lost: %s", wire)
	}
}

func TestLateObserverFillsFailureCategoryOnlyFromNewestAttempt(t *testing.T) {
	hub := testHub(t)
	if _, err := hub.Start(115, "late-failure"); err != nil {
		t.Fatal(err)
	}
	if !hub.PublishTrace(115, "late-failure", Trace{ID: "failed-trace", Revision: 1, Terminal: true, Payload: map[string]any{
		"terminal": "failed", "status": 502, "response": "caller response", "attempt_index": 1,
	}}) {
		t.Fatal("failed terminal projection was not accepted")
	}
	if !hub.PublishTrace(115, "late-failure", Trace{ID: "failed-trace", Revision: 2, Merge: true, Payload: map[string]any{
		"attempt_index": 0, "attempt_state": "failed", "error": "internal",
	}}) {
		t.Fatal("older late observer projection was not accepted")
	}
	if !hub.PublishTrace(115, "late-failure", Trace{ID: "failed-trace", Revision: 3, Merge: true, Payload: map[string]any{
		"attempt_index": 2, "attempt_state": "failed", "error": "upstream",
	}}) {
		t.Fatal("newest late observer projection was not accepted")
	}
	hub.mu.Lock()
	wire := append([]byte(nil), hub.sessions[115].traces["failed-trace"].wire...)
	hub.mu.Unlock()
	var projection map[string]any
	if err := json.Unmarshal(wire, &projection); err != nil {
		t.Fatal(err)
	}
	if projection["terminal"] != "failed" || projection["error"] != "upstream" || projection["attempt_index"] != float64(2) {
		t.Fatalf("late failure merge=%s", wire)
	}
}

func TestHubMergeRetainsCompleteProjectionAndLateObserverCannotRegressTerminal(t *testing.T) {
	hub := testHub(t)
	if _, err := hub.Start(14, "merge-binding"); err != nil {
		t.Fatal(err)
	}
	if !hub.PublishTrace(14, "merge-binding", Trace{ID: "merge-trace", Payload: map[string]any{
		"request_id": "request-1", "raw_request": map[string]any{"messages": []any{"hello"}},
	}, Revision: 1}) {
		t.Fatal("initial projection was not accepted")
	}
	if !hub.PublishTrace(14, "merge-binding", Trace{ID: "merge-trace", Merge: true, Payload: map[string]any{
		"effective": map[string]any{"connector": "openai-compatible"}, "attempt_index": 1,
	}, Revision: 2}) {
		t.Fatal("observer projection was not accepted")
	}
	if !hub.PublishTrace(14, "merge-binding", Trace{ID: "merge-trace", Merge: true, Terminal: true, Payload: map[string]any{
		"terminal": "failed", "response": "upstream response",
	}, Revision: 3}) {
		t.Fatal("final projection was not accepted")
	}
	if !hub.PublishTrace(14, "merge-binding", Trace{ID: "merge-trace", Merge: true, Payload: map[string]any{
		"terminal": "completed", "attempt_index": 2,
	}, Revision: 4}) {
		t.Fatal("late observer projection was not accepted")
	}
	hub.mu.Lock()
	record := hub.sessions[14].traces["merge-trace"]
	hub.mu.Unlock()
	if record == nil || !record.terminal {
		t.Fatal("logical terminal trace became evictable")
	}
	var projection map[string]any
	if err := json.Unmarshal(record.wire, &projection); err != nil {
		t.Fatal(err)
	}
	if projection["raw_request"] == nil || projection["effective"] == nil || projection["response"] == nil || projection["terminal"] != "failed" {
		t.Fatalf("merge lost complete projection or regressed terminal: %s", record.wire)
	}
}

func TestRedactionBoundsPlainResponseModelParametersAndAggregateMessagesTools(t *testing.T) {
	payload := buildTracePayload(TraceInput{
		Mode: ModeDry, Model: "[公益]donor/charity", Response: []byte("data: hello\n\n"),
		RawRequest: []byte(`{"model":"[公益]donor/charity","temperature":-0,"top_p":0e0}`),
	})
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"model":"[公益]donor/charity"`) || !strings.Contains(string(encoded), `data: hello\n\n`) {
		t.Fatalf("model/plain response projection=%s", encoded)
	}
	parameters, ok := payload["parameters"].(json.RawMessage)
	if !ok {
		t.Fatalf("parameters type=%T", payload["parameters"])
	}
	var items []map[string]any
	if err := json.Unmarshal(parameters, &items); err != nil {
		t.Fatal(err)
	}
	presence := make(map[string]string)
	for _, item := range items {
		presence[item["name"].(string)] = item["presence"].(string)
	}
	if presence["temperature"] != "zero" || presence["top_p"] != "zero" || presence["stream"] != "absent" {
		t.Fatalf("parameter presence=%v", presence)
	}
	secret := RedactJSON([]byte(`{"safety_identifier":"hmac-secret","note":"Bearer top-secret nbk_caller"}`), MaxRawRequestBytes)
	if strings.Contains(string(secret.Data), "hmac-secret") || strings.Contains(string(secret.Data), "top-secret") || strings.Contains(string(secret.Data), "nbk_caller") {
		t.Fatalf("secret-shaped string escaped redaction: %s", secret.Data)
	}
	messages := bytes.Repeat([]byte{'m'}, MaxMessagesToolsBytes/2)
	tools := bytes.Repeat([]byte{'t'}, MaxMessagesToolsBytes/2+1)
	bounded := map[string]any{}
	setMessagesTools(bounded, messages, tools)
	if !strings.Contains(string(bounded["tools"].(json.RawMessage)), "size_limit") {
		t.Fatalf("messages/tools aggregate budget was not enforced: %s", bounded["tools"])
	}
}

func TestDryTraceCapturesSimulatedCallerResponseAndCharityLogicalValidator(t *testing.T) {
	var personal, charity atomic.Int32
	hub, err := NewHub(Config{
		SessionBindingValidator: func(context.Context, int64, string) (bool, error) { return true, nil },
		DryRunValidator: func(_ context.Context, _ int64, request *openai.ChatRequest) (DryRunResult, error) {
			personal.Add(1)
			return DryRunResult{Model: request.Model, Personal: true}, nil
		},
		CharityDryRunValidator: func(_ context.Context, _ int64, request *openai.ChatRequest) (DryRunResult, error) {
			charity.Add(1)
			return DryRunResult{Model: request.Model, Charity: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	if _, err := hub.Start(15, "dry-binding"); err != nil {
		t.Fatal(err)
	}
	wrapper := hub.WrapCallerWithIdentity(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("dry called real handler") }), func(*http.Request) (int64, error) { return 15, nil })
	record := httptest.NewRecorder()
	wrapper.ServeHTTP(record, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"[公益]donor/charity","messages":[]}`)))
	if record.Code != http.StatusOK || personal.Load() != 0 || charity.Load() != 1 {
		t.Fatalf("charity dry route code=%d personal=%d charity=%d", record.Code, personal.Load(), charity.Load())
	}
	hub.mu.Lock()
	var traceWire []byte
	for _, trace := range hub.sessions[15].traces {
		traceWire = append([]byte(nil), trace.wire...)
	}
	hub.mu.Unlock()
	if !bytes.Contains(traceWire, []byte(`"status":200`)) || !bytes.Contains(traceWire, []byte(`"response"`)) {
		t.Fatalf("dry trace missing caller response projection: %s", traceWire)
	}
}

func TestHubTraceCapDropsNewCopyUntilOldestTerminalIsAvailable(t *testing.T) {
	hub := testHub(t)
	if _, err := hub.Start(2, "trace-binding"); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < MaxTraces; index++ {
		if !hub.PublishTrace(2, "trace-binding", Trace{ID: "active-" + strconv.Itoa(index), Revision: 1, Payload: map[string]any{"index": index}}) {
			t.Fatalf("active trace %d was dropped", index)
		}
	}
	if hub.PublishTrace(2, "trace-binding", Trace{ID: "new-active", Revision: 1, Payload: map[string]any{"active": true}}) {
		t.Fatal("new active trace should be dropped when no terminal trace is evictable")
	}
	if !hub.PublishTrace(2, "trace-binding", Trace{ID: "active-0", Revision: 2, Terminal: true, Payload: map[string]any{"done": true}}) {
		t.Fatal("terminal update was dropped")
	}
	if !hub.PublishTrace(2, "trace-binding", Trace{ID: "new-terminal", Revision: 1, Terminal: true, Payload: map[string]any{"done": true}}) {
		t.Fatal("new trace should evict the oldest terminal trace")
	}
	if _, ok := hub.Metadata(2, "trace-binding"); !ok {
		t.Fatal("trace session disappeared")
	}
}

func TestControlHandlerFixedSurfaceAndLiveChallenge(t *testing.T) {
	hub := testHub(t)
	identity := func(*http.Request) (int64, string, error) { return 42, "session-binding", nil }
	handler := NewHandler(hub, HandlerConfig{Identity: identity})
	request := func(method, path, body string) *httptest.ResponseRecorder {
		record := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		handler.ServeHTTP(record, req)
		return record
	}
	if record := request(http.MethodGet, "/api/debug/session", ""); record.Code != http.StatusOK || !strings.Contains(record.Body.String(), `"active":false`) {
		t.Fatalf("inactive session=%d %s", record.Code, record.Body.String())
	}
	if record := request(http.MethodPost, "/api/debug/session", `{}`); record.Code != http.StatusBadRequest {
		t.Fatalf("bodyful start status=%d body=%s", record.Code, record.Body.String())
	}
	start := request(http.MethodPost, "/api/debug/session", "")
	if start.Code != http.StatusCreated || start.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("start=%d headers=%v body=%s", start.Code, start.Header(), start.Body.String())
	}
	attachTestSubscriber(t, hub, 42, "session-binding")
	if record := request(http.MethodPut, "/api/debug/session/mode", `{"mode":"live"}`); record.Code != http.StatusBadRequest {
		t.Fatalf("live without challenge status=%d", record.Code)
	}
	for _, body := range []string{
		`{"mode":"dry","confirmation_id":null}`,
		`{"mode":"dry","confirmation_id":""}`,
		`{"mode":"dry","confirmation_id":"unexpected"}`,
		`{"mode":"live","confirmation_id":null}`,
		`{"mode":"live","confirmation_id":""}`,
		`{"mode":"live","confirmation_id":"a","confirmation_id":"b"}`,
	} {
		if record := request(http.MethodPut, "/api/debug/session/mode", body); record.Code != http.StatusBadRequest {
			t.Fatalf("invalid mode body=%s status=%d response=%s", body, record.Code, record.Body.String())
		}
	}
	challenge := request(http.MethodPost, "/api/debug/session/live-challenge", "")
	if challenge.Code != http.StatusOK {
		t.Fatalf("challenge=%d %s", challenge.Code, challenge.Body.String())
	}
	var challengeBody struct {
		ConfirmationID string `json:"confirmation_id"`
	}
	if err := json.Unmarshal(challenge.Body.Bytes(), &challengeBody); err != nil || challengeBody.ConfirmationID == "" {
		t.Fatalf("challenge body=%s err=%v", challenge.Body.String(), err)
	}
	liveBody := `{"mode":"live","confirmation_id":"` + challengeBody.ConfirmationID + `"}`
	if record := request(http.MethodPut, "/api/debug/session/mode", liveBody); record.Code != http.StatusOK || !strings.Contains(record.Body.String(), `"mode":"live"`) {
		t.Fatalf("live=%d %s", record.Code, record.Body.String())
	}
	if record := request(http.MethodGet, "/api/debug/events?session_id=forbidden", ""); record.Code != http.StatusBadRequest {
		t.Fatalf("query session id status=%d", record.Code)
	}
}

func TestDryWrapperDoesNotCallUnderlyingAndLiveTeePreservesResponse(t *testing.T) {
	hub, err := NewHub(Config{SessionBindingValidator: func(context.Context, int64, string) (bool, error) { return true, nil }, DryRunValidator: func(_ context.Context, _ int64, request *openai.ChatRequest) (DryRunResult, error) {
		return DryRunResult{Model: request.Model, Personal: true}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	if _, err := hub.Start(9, "binding"); err != nil {
		t.Fatal(err)
	}
	var underlying atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		underlying.Add(1)
		w.Header().Set("X-Upstream", "unchanged")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("upstream-secret"))
	})
	wrapped := hub.WrapCallerWithIdentity(next, func(*http.Request) (int64, error) { return 9, nil })
	dryRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"public/model","messages":[]}`))
	dryRecord := httptest.NewRecorder()
	wrapped.ServeHTTP(dryRecord, dryRequest)
	if underlying.Load() != 0 || dryRecord.Code != http.StatusOK || dryRecord.Header().Get("X-Nonbiri-Debug-Mode") != "dry-run" || strings.Contains(dryRecord.Body.String(), "upstream-secret") {
		t.Fatalf("dry underlying=%d code=%d headers=%v body=%s", underlying.Load(), dryRecord.Code, dryRecord.Header(), dryRecord.Body.String())
	}
	confirmation, err := hub.IssueChallenge(9, "binding")
	if err != nil {
		t.Fatal(err)
	}
	attachTestSubscriber(t, hub, 9, "binding")
	if _, err := hub.SetMode(9, "binding", ModeLive, confirmation); err != nil {
		t.Fatal(err)
	}
	liveRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"public/model","messages":[]}`))
	liveRecord := httptest.NewRecorder()
	wrapped.ServeHTTP(liveRecord, liveRequest)
	if underlying.Load() != 1 || liveRecord.Code != http.StatusAccepted || liveRecord.Header().Get("X-Upstream") != "unchanged" || liveRecord.Body.String() != "upstream-secret" {
		t.Fatalf("live underlying=%d code=%d headers=%v body=%q", underlying.Load(), liveRecord.Code, liveRecord.Header(), liveRecord.Body.String())
	}
	if strings.Contains(string(mustMarshal(liveRecord.Header())), "secret") {
		t.Fatal("unexpected secret in response headers")
	}
}

func TestDryWrapperFailsClosedWithoutValidatorAndReusesIngressGate(t *testing.T) {
	hub, err := NewHub(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	if _, err := hub.Start(10, "binding"); err != nil {
		t.Fatal(err)
	}
	var underlying atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		underlying.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	})
	wrapped := hub.WrapCallerWithIdentity(next, func(*http.Request) (int64, error) { return 10, nil })
	contentType := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"public/model"}`))
	contentType.Header.Set("Content-Type", "text/plain")
	contentRecord := httptest.NewRecorder()
	wrapped.ServeHTTP(contentRecord, contentType)
	if contentRecord.Code != http.StatusBadRequest || underlying.Load() != 1 || contentRecord.Header().Get("X-Nonbiri-Debug-Mode") != "" {
		t.Fatalf("ingress mismatch code=%d underlying=%d headers=%v", contentRecord.Code, underlying.Load(), contentRecord.Header())
	}

	valid := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"public/model"}`))
	validRecord := httptest.NewRecorder()
	wrapped.ServeHTTP(validRecord, valid)
	if validRecord.Code != http.StatusInternalServerError || underlying.Load() != 1 || validRecord.Header().Get("X-Nonbiri-Debug-Mode") != "" {
		t.Fatalf("missing validator was not fail-closed code=%d underlying=%d body=%s", validRecord.Code, underlying.Load(), validRecord.Body.String())
	}
}

func TestActiveIngressRejectionPreservesWireAndCreatesDryLiveTrace(t *testing.T) {
	hub := testHub(t)
	if _, err := hub.Start(108, "ingress-binding"); err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Ingress", "rejected")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid request"}`)
	})
	ordinary := func(contentEncoding bool) *httptest.ResponseRecorder {
		record := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"public/model"}`))
		request.Header.Set("Content-Type", "text/plain")
		if contentEncoding {
			request.Header.Set("Content-Encoding", "gzip")
		}
		next.ServeHTTP(record, request)
		return record
	}
	wrapper := hub.WrapCallerWithIdentity(next, func(*http.Request) (int64, error) { return 108, nil })
	for _, encoded := range []bool{false, true} {
		expected := ordinary(encoded)
		actual := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"public/model"}`))
		request.Header.Set("Content-Type", "text/plain")
		if encoded {
			request.Header.Set("Content-Encoding", "gzip")
		}
		wrapper.ServeHTTP(actual, request)
		if actual.Code != expected.Code || actual.Body.String() != expected.Body.String() || !headerEqual(actual.Header(), expected.Header()) {
			t.Fatalf("dry ingress encoded=%v actual=(%d,%q,%v) expected=(%d,%q,%v)", encoded, actual.Code, actual.Body.String(), actual.Header(), expected.Code, expected.Body.String(), expected.Header())
		}
	}
	hub.mu.Lock()
	dryTraceCount := len(hub.sessions[108].traces)
	hub.mu.Unlock()
	if dryTraceCount != 2 {
		t.Fatalf("dry ingress trace count=%d, want 2", dryTraceCount)
	}
	confirmation, err := hub.IssueChallenge(108, "ingress-binding")
	if err != nil {
		t.Fatal(err)
	}
	attachTestSubscriber(t, hub, 108, "ingress-binding")
	if _, err := hub.SetMode(108, "ingress-binding", ModeLive, confirmation); err != nil {
		t.Fatal(err)
	}
	for _, encoded := range []bool{false, true} {
		expected := ordinary(encoded)
		actual := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"public/model"}`))
		request.Header.Set("Content-Type", "text/plain")
		if encoded {
			request.Header.Set("Content-Encoding", "gzip")
		}
		wrapper.ServeHTTP(actual, request)
		if actual.Code != expected.Code || actual.Body.String() != expected.Body.String() || !headerEqual(actual.Header(), expected.Header()) {
			t.Fatalf("live ingress encoded=%v actual=(%d,%q,%v) expected=(%d,%q,%v)", encoded, actual.Code, actual.Body.String(), actual.Header(), expected.Code, expected.Body.String(), expected.Header())
		}
	}
	hub.mu.Lock()
	var liveFound bool
	for _, trace := range hub.sessions[108].traces {
		if bytes.Contains(trace.wire, []byte(`"mode":"live"`)) && bytes.Contains(trace.wire, []byte(`"status":400`)) {
			liveFound = true
		}
	}
	hub.mu.Unlock()
	if !liveFound {
		t.Fatal("live ingress rejection did not create a status trace")
	}
}

func TestActualLiveWrapperLargeProjectionUsesMonotonicFragmentRevisions(t *testing.T) {
	hub := testHub(t)
	if _, err := hub.Start(111, "large-live"); err != nil {
		t.Fatal(err)
	}
	confirmation, err := hub.IssueChallenge(111, "large-live")
	if err != nil {
		t.Fatal(err)
	}
	attachTestSubscriber(t, hub, 111, "large-live")
	if _, err := hub.SetMode(111, "large-live", ModeLive, confirmation); err != nil {
		t.Fatal(err)
	}
	response := strings.Repeat("r", 350<<10)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if state := forward.ProtocolStateFromContext(r.Context()); state != nil {
			state.Mark(connectorcontract.AttemptResult{Success: true})
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, response)
	})
	requestBody := `{"model":"public/model","messages":[{"role":"user","content":"` + strings.Repeat("m", 100<<10) + `"}]}`
	record := httptest.NewRecorder()
	hub.WrapCallerWithIdentity(next, func(*http.Request) (int64, error) { return 111, nil }).ServeHTTP(record,
		httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(requestBody)))
	if record.Code != http.StatusOK || len(record.Body.Bytes()) != len(response) {
		t.Fatalf("live caller wire code=%d body=%d want=%d", record.Code, len(record.Body.Bytes()), len(response))
	}
	hub.mu.Lock()
	var traceID string
	var revisions []uint64
	var finalRevision uint64
	for _, event := range hub.sessions[111].events {
		if event == nil || event.envelope.Type != EventTraceUpsert {
			continue
		}
		if traceID == "" {
			traceID = event.envelope.TraceID
		}
		if event.envelope.TraceID != traceID {
			continue
		}
		if event.envelope.Revision > 1 {
			revisions = append(revisions, event.envelope.Revision)
		}
	}
	if trace := hub.sessions[111].traces[traceID]; trace != nil {
		finalRevision = trace.revision
	}
	hub.mu.Unlock()
	if len(revisions) < 1 || finalRevision == 0 {
		t.Fatalf("actual live large trace revisions=%v final=%d trace=%q", revisions, finalRevision, traceID)
	}
	for index := 1; index < len(revisions); index++ {
		if revisions[index] <= revisions[index-1] {
			t.Fatalf("non-monotonic actual fragment revisions=%v", revisions)
		}
	}
	if finalRevision != revisions[len(revisions)-1] {
		t.Fatalf("record final revision=%d last wire revision=%d", finalRevision, revisions[len(revisions)-1])
	}
}

func headerEqual(left, right http.Header) bool {
	if len(left) != len(right) {
		return false
	}
	for key, values := range left {
		if strings.Join(values, "\x00") != strings.Join(right.Values(key), "\x00") {
			return false
		}
	}
	return true
}

type failingBody struct {
	read bool
}

func (b *failingBody) Read(p []byte) (int, error) {
	if b.read {
		return 0, errors.New("body read failed")
	}
	b.read = true
	copy(p, []byte(`{"model":"public/model"}`))
	return len([]byte(`{"model":"public/model"}`)), errors.New("body read failed")
}

func (*failingBody) Close() error { return nil }

func TestDryWrapperReadFailureNeverDelegatesToRealSend(t *testing.T) {
	hub := testHub(t)
	if _, err := hub.Start(11, "binding"); err != nil {
		t.Fatal(err)
	}
	var underlying atomic.Int32
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { underlying.Add(1) })
	wrapped := hub.WrapCallerWithIdentity(next, func(*http.Request) (int64, error) { return 11, nil })
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.Body = &failingBody{}
	record := httptest.NewRecorder()
	wrapped.ServeHTTP(record, request)
	if underlying.Load() != 0 || record.Code != http.StatusBadRequest {
		t.Fatalf("read failure delegated code=%d underlying=%d", record.Code, underlying.Load())
	}
}

type trackedReadErrorBody struct {
	closed atomic.Int32
	read   bool
}

func (b *trackedReadErrorBody) Read(p []byte) (int, error) {
	if b.read {
		return 0, errors.New("body read failed")
	}
	b.read = true
	data := []byte(`{"model":"public/model"}`)
	n := copy(p, data)
	return n, errors.New("body read failed")
}

func (b *trackedReadErrorBody) Close() error {
	b.closed.Add(1)
	return nil
}

func TestLiveBodyReplayPreservesReadErrorAndClosesOriginal(t *testing.T) {
	hub, err := NewHub(Config{SessionBindingValidator: func(context.Context, int64, string) (bool, error) { return true, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	if _, err := hub.Start(16, "body-binding"); err != nil {
		t.Fatal(err)
	}
	confirmation, err := hub.IssueChallenge(16, "body-binding")
	if err != nil {
		t.Fatal(err)
	}
	attachTestSubscriber(t, hub, 16, "body-binding")
	if _, err := hub.SetMode(16, "body-binding", ModeLive, confirmation); err != nil {
		t.Fatal(err)
	}
	original := &trackedReadErrorBody{}
	var underlying atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		underlying.Add(1)
		data, readErr := io.ReadAll(r.Body)
		if readErr == nil || string(data) != `{"model":"public/model"}` {
			t.Errorf("replayed body data=%q err=%v", data, readErr)
		}
		w.WriteHeader(http.StatusAccepted)
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.Body = original
	record := httptest.NewRecorder()
	hub.WrapCallerWithIdentity(next, func(*http.Request) (int64, error) { return 16, nil }).ServeHTTP(record, request)
	if underlying.Load() != 1 || original.closed.Load() != 1 || record.Code != http.StatusAccepted {
		t.Fatalf("live replay underlying=%d closed=%d code=%d", underlying.Load(), original.closed.Load(), record.Code)
	}
}

type readCountBody struct {
	reads  atomic.Int32
	closed atomic.Int32
}

type shortResponseWriter struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

type flushErrorResponseWriter struct {
	header   http.Header
	body     bytes.Buffer
	flushErr error
	flushes  atomic.Int32
}

func (w *flushErrorResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *flushErrorResponseWriter) WriteHeader(code int) {
	if w.Header().Get("X-Status") == "" {
		w.Header().Set("X-Status", strconv.Itoa(code))
	}
}

func (w *flushErrorResponseWriter) Write(data []byte) (int, error) {
	return w.body.Write(data)
}

func (w *flushErrorResponseWriter) Flush() {}

func (w *flushErrorResponseWriter) FlushError() error {
	w.flushes.Add(1)
	return w.flushErr
}

type sseDeadlineWriter struct {
	header          http.Header
	body            bytes.Buffer
	deadline        atomic.Int64
	flushErr        error
	flushes         atomic.Int32
	sawDeadline     atomic.Bool
	clearedDeadline atomic.Bool
}

func (w *sseDeadlineWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *sseDeadlineWriter) WriteHeader(int) {}

func (w *sseDeadlineWriter) Write(data []byte) (int, error) {
	if w.deadline.Load() != 0 {
		w.sawDeadline.Store(true)
	}
	return w.body.Write(data)
}

func (w *sseDeadlineWriter) Flush() {}

func (w *sseDeadlineWriter) FlushError() error {
	w.flushes.Add(1)
	return w.flushErr
}

func (w *sseDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	if deadline.IsZero() {
		w.deadline.Store(0)
		w.clearedDeadline.Store(true)
		return nil
	}
	w.deadline.Store(deadline.UnixNano())
	return nil
}

func (w *shortResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *shortResponseWriter) WriteHeader(code int) { w.code = code }

func (w *shortResponseWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	_, _ = w.body.Write(data[:len(data)-1])
	return len(data) - 1, nil
}

func (w *shortResponseWriter) Flush() {}

func (b *readCountBody) Read([]byte) (int, error) {
	b.reads.Add(1)
	return 0, io.EOF
}

func (b *readCountBody) Close() error {
	b.closed.Add(1)
	return nil
}

func TestRequestCopyLeaseBoundsTemporaryBodyAndFailsClosed(t *testing.T) {
	const hubBudget = int64(MaxRequestCopyBytes) + 1<<20
	hub, err := NewHub(Config{
		MaxHubBytes:     hubBudget,
		MaxSessionBytes: MaxSessionBytes,
		SessionBindingValidator: func(context.Context, int64, string) (bool, error) {
			return true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	metadata, err := hub.Start(17, "lease")
	if err != nil {
		t.Fatal(err)
	}
	lease, ok := hub.acquireRequestCopyLease(17, metadata.ID, metadata.Generation)
	if !ok {
		hub.mu.Lock()
		session := hub.sessions[17]
		total, maxCopy, maxSession, maxHub := hub.totalBytes, hub.maxRequestCopyBytes, hub.maxSessionBytes, hub.maxHubBytes
		hub.mu.Unlock()
		t.Fatalf("first request-copy lease was rejected copyconst=%d session=%+v total=%d maxcopy=%d maxsession=%d maxhub=%d", MaxRequestCopyBytes, session, total, maxCopy, maxSession, maxHub)
	}
	if _, second := hub.acquireRequestCopyLease(17, metadata.ID, metadata.Generation); second {
		t.Fatal("per-session copy lease exceeded its bounded budget")
	}
	hub.mu.Lock()
	if hub.requestCopyBytes != int64(MaxRequestCopyBytes) || hub.sessions[17].requestCopyBytes != int64(MaxRequestCopyBytes) {
		hub.mu.Unlock()
		t.Fatalf("lease accounting=%d session=%d", hub.requestCopyBytes, hub.sessions[17].requestCopyBytes)
	}
	hub.mu.Unlock()
	lease.Release()
	hub.mu.Lock()
	if hub.requestCopyBytes != 0 || hub.sessions[17].requestCopyBytes != 0 {
		hub.mu.Unlock()
		t.Fatalf("lease was not released hub=%d session=%d", hub.requestCopyBytes, hub.sessions[17].requestCopyBytes)
	}
	hub.mu.Unlock()

	// Occupy the only temporary-copy slot. Dry mode must not read or delegate
	// the real request when admission cannot reserve its bounded copy.
	lease, ok = hub.acquireRequestCopyLease(17, metadata.ID, metadata.Generation)
	if !ok {
		t.Fatal("second lease was rejected")
	}
	body := &readCountBody{}
	var called atomic.Int32
	wrapped := hub.WrapCallerWithIdentity(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Add(1) }), func(*http.Request) (int64, error) { return 17, nil })
	record := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	wrapped.ServeHTTP(record, request)
	if called.Load() != 0 || body.reads.Load() != 0 || record.Code != http.StatusServiceUnavailable {
		t.Fatalf("dry no-lease path called=%d reads=%d status=%d", called.Load(), body.reads.Load(), record.Code)
	}
	lease.Release()

	confirmation, err := hub.IssueChallenge(17, "lease")
	if err != nil {
		t.Fatal(err)
	}
	attachTestSubscriber(t, hub, 17, "lease")
	if _, err := hub.SetMode(17, "lease", ModeLive, confirmation); err != nil {
		t.Fatal(err)
	}
	lease, ok = hub.acquireRequestCopyLease(17, metadata.ID, metadata.Generation)
	if !ok {
		t.Fatal("live lease was rejected")
	}
	body = &readCountBody{}
	called.Store(0)
	record = httptest.NewRecorder()
	wrapped.ServeHTTP(record, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body))
	if called.Load() != 0 || body.reads.Load() != 0 || record.Code != http.StatusServiceUnavailable {
		t.Fatalf("live no-lease path was not fail-closed called=%d reads=%d status=%d", called.Load(), body.reads.Load(), record.Code)
	}
	lease.Release()
}

func TestLiveObserverAdmissionFailureUsesSameTerminalTraceAndNeverCallsNext(t *testing.T) {
	hub := testHub(t)
	_, err := hub.Start(117, "observer-capacity")
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := hub.IssueChallenge(117, "observer-capacity")
	if err != nil {
		t.Fatal(err)
	}
	attachTestSubscriber(t, hub, 117, "observer-capacity")
	if _, err := hub.SetMode(117, "observer-capacity", ModeLive, confirmation); err != nil {
		t.Fatal(err)
	}
	hub.mu.Lock()
	hub.maxObservers = 0
	hub.mu.Unlock()
	var calls atomic.Int32
	wrapped := hub.WrapCallerWithIdentity(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}), func(*http.Request) (int64, error) { return 117, nil })
	for index := 0; index < MaxTraces+8; index++ {
		record := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"public/model"}`))
		request.Header.Set("Content-Type", "application/json")
		wrapped.ServeHTTP(record, request)
		if record.Code != http.StatusServiceUnavailable {
			t.Fatalf("request %d status=%d body=%s", index, record.Code, record.Body.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("observer capacity failure reached real handler calls=%d", calls.Load())
	}
	hub.mu.Lock()
	session := hub.sessions[117]
	if session == nil || len(session.traces) > MaxTraces {
		hub.mu.Unlock()
		t.Fatalf("observer capacity grew traces session=%v", session)
	}
	for id, trace := range session.traces {
		if trace == nil || !trace.terminal {
			hub.mu.Unlock()
			t.Fatalf("trace %q remained active after admission failure: %+v", id, trace)
		}
	}
	hub.mu.Unlock()
}

func TestResponseCaptureSnapshotsContentTypeAtCommit(t *testing.T) {
	record := httptest.NewRecorder()
	capture := &responseCapture{orig: record, maxBytes: MaxResponseBytes}
	capture.Header().Set("Content-Type", "application/json")
	_, _ = capture.Write([]byte(`{"ok":true}`))
	capture.Header().Set("Content-Type", "text/plain")
	if got := capture.ContentType(); got != "application/json" {
		t.Fatalf("content type changed after commit: %q", got)
	}
	for _, body := range []string{`{"ok":true}`, "plain text"} {
		ordinary := httptest.NewRecorder()
		_, _ = ordinary.Write([]byte(body))
		capturedRecorder := httptest.NewRecorder()
		captured := &responseCapture{orig: capturedRecorder, maxBytes: MaxResponseBytes}
		_, _ = captured.Write([]byte(body))
		if capturedRecorder.Header().Get("Content-Type") != ordinary.Header().Get("Content-Type") || captured.ContentType() != ordinary.Header().Get("Content-Type") {
			t.Fatalf("implicit content type body=%q captured=%q ordinary=%q", body, capturedRecorder.Header().Get("Content-Type"), ordinary.Header().Get("Content-Type"))
		}
	}
}

func TestResponseCaptureResponseControllerFlushAndUnwrap(t *testing.T) {
	flushErr := errors.New("flush failed")
	original := &flushErrorResponseWriter{flushErr: flushErr}
	capture := &responseCapture{orig: original, maxBytes: MaxResponseBytes}
	if capture.Unwrap() != original {
		t.Fatal("response capture did not preserve Unwrap identity")
	}
	if _, err := capture.Write([]byte("first-byte")); err != nil {
		t.Fatal(err)
	}
	if got := http.NewResponseController(capture).Flush(); !errors.Is(got, flushErr) {
		t.Fatalf("ResponseController.Flush error=%v, want %v", got, flushErr)
	}
	if !capture.wroteHead || original.flushes.Load() != 1 {
		t.Fatalf("flush did not commit/call underlying: wroteHead=%v flushes=%d", capture.wroteHead, original.flushes.Load())
	}
}

func TestSSEWriteHelpersEnforceDeadlineAndPropagateFlushErrors(t *testing.T) {
	flushErr := errors.New("sse flush failed")
	writer := &sseDeadlineWriter{flushErr: flushErr}
	event := EventEnvelope{Version: 1, Seq: 7, Type: EventGap, SessionID: "dbg", Payload: map[string]any{"after": uint64(6), "reason": "resume_gap"}}
	if err := writeEvent(writer, writer, event); !errors.Is(err, flushErr) {
		t.Fatalf("writeEvent error=%v, want %v", err, flushErr)
	}
	if writer.flushes.Load() != 1 || !writer.sawDeadline.Load() || !writer.clearedDeadline.Load() {
		t.Fatalf("writeEvent deadline/flush state flushes=%d saw=%v cleared=%v", writer.flushes.Load(), writer.sawDeadline.Load(), writer.clearedDeadline.Load())
	}
	writer.flushErr = nil
	writer.sawDeadline.Store(false)
	writer.clearedDeadline.Store(false)
	if err := writeHeartbeat(writer, writer); err != nil {
		t.Fatal(err)
	}
	if writer.flushes.Load() != 2 || !writer.sawDeadline.Load() || !writer.clearedDeadline.Load() {
		t.Fatalf("writeHeartbeat deadline/flush state flushes=%d saw=%v cleared=%v", writer.flushes.Load(), writer.sawDeadline.Load(), writer.clearedDeadline.Load())
	}
}

func TestDryWritersRejectShortWritesAndTraceIncomplete(t *testing.T) {
	short := &shortResponseWriter{}
	if err := writeDryJSON(context.Background(), short, "public/model"); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("dry JSON short write err=%v", err)
	}
	short = &shortResponseWriter{}
	if err := writeDryStream(context.Background(), short, "public/model"); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("dry stream short write err=%v", err)
	}

	hub, err := NewHub(Config{DryRunValidator: func(_ context.Context, _ int64, request *openai.ChatRequest) (DryRunResult, error) {
		return DryRunResult{Model: request.Model, Personal: true}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	metadata, err := hub.Start(20, "short")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"public/model"}`))
	body := []byte(`{"model":"public/model"}`)
	short = &shortResponseWriter{}
	hub.serveDry(short, request, 20, metadata.ID, metadata.Generation, body, false)
	hub.mu.Lock()
	var sawIncomplete bool
	for _, trace := range hub.sessions[20].traces {
		if bytes.Contains(trace.wire, []byte(`"terminal":"incomplete"`)) && bytes.Contains(trace.wire, []byte(`"error":"sink"`)) {
			sawIncomplete = true
		}
	}
	hub.mu.Unlock()
	if !sawIncomplete {
		traces := make([]string, 0)
		for _, trace := range hub.sessions[20].traces {
			traces = append(traces, string(trace.wire))
		}
		t.Fatalf("short dry sink did not retain an incomplete terminal trace: %v", traces)
	}
}

func TestObserverAdmissionIsBoundedPerSessionAndProcess(t *testing.T) {
	hub := testHub(t)
	hub.maxSessions = 2
	hub.maxObservers = 2 * MaxTraces
	first, err := hub.Start(18, "observer-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := hub.Start(19, "observer-b")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		userID     int64
		id         string
		generation uint64
	}{
		{18, first.ID, first.Generation}, {19, second.ID, second.Generation},
	} {
		confirmation, challengeErr := hub.IssueChallenge(item.userID, map[int64]string{18: "observer-a", 19: "observer-b"}[item.userID])
		if challengeErr != nil {
			t.Fatal(challengeErr)
		}
		attachTestSubscriber(t, hub, item.userID, map[int64]string{18: "observer-a", 19: "observer-b"}[item.userID])
		if _, modeErr := hub.SetMode(item.userID, map[int64]string{18: "observer-a", 19: "observer-b"}[item.userID], ModeLive, confirmation); modeErr != nil {
			t.Fatal(modeErr)
		}
		if !hub.PublishTrace(item.userID, map[int64]string{18: "observer-a", 19: "observer-b"}[item.userID], Trace{ID: "observer-trace", Payload: map[string]any{"terminal": "dispatching"}}) {
			t.Fatal("initial observer trace was not retained")
		}
	}
	observers := make([]*connector.SafeObserver, 0, 2*MaxTraces)
	for _, item := range []struct {
		userID     int64
		id         string
		generation uint64
	}{
		{18, first.ID, first.Generation}, {19, second.ID, second.Generation},
	} {
		for i := 0; i < MaxTraces; i++ {
			observer, observerErr := hub.NewRequestObserverForTrace(item.userID, item.id, item.generation, "observer-trace")
			if observerErr != nil {
				t.Fatalf("observer %d user %d err=%v", i, item.userID, observerErr)
			}
			observers = append(observers, observer)
		}
	}
	if _, observerErr := hub.NewRequestObserverForTrace(18, first.ID, first.Generation, "observer-trace"); !errors.Is(observerErr, ErrCapacity) {
		t.Fatalf("process observer cap err=%v", observerErr)
	}
	for _, observer := range observers {
		_ = observer.Close()
	}
	if observer, observerErr := hub.NewRequestObserverForTrace(18, first.ID, first.Generation, "observer-trace"); observerErr != nil {
		t.Fatalf("released observer slot remained occupied: %v", observerErr)
	} else {
		_ = observer.Close()
	}
}

func TestObserverLeaseSurvivesSessionReplacementUntilSidecarClose(t *testing.T) {
	hub := testHub(t)
	hub.maxObservers = 1
	first, err := hub.Start(87, "observer-replace")
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := hub.IssueChallenge(87, "observer-replace")
	if err != nil {
		t.Fatal(err)
	}
	attachTestSubscriber(t, hub, 87, "observer-replace")
	if _, err := hub.SetMode(87, "observer-replace", ModeLive, confirmation); err != nil {
		t.Fatal(err)
	}
	if !hub.PublishTrace(87, "observer-replace", Trace{ID: "replace-trace", Payload: map[string]any{"state": "dispatching"}}) {
		t.Fatal("initial trace was not retained")
	}
	observer, err := hub.NewRequestObserverForTrace(87, first.ID, first.Generation, "replace-trace")
	if err != nil {
		t.Fatal(err)
	}
	hub.mu.Lock()
	if hub.observerCount != 1 {
		hub.mu.Unlock()
		t.Fatalf("observer count after admission=%d", hub.observerCount)
	}
	hub.mu.Unlock()
	second, err := hub.Start(87, "observer-replace")
	if err != nil {
		t.Fatal(err)
	}
	// Replacing the session detaches its logical owner, but the sidecar still
	// owns the process-wide slot until its worker has drained and Close runs
	// the lifecycle hook.
	hub.mu.Lock()
	count := hub.observerCount
	hub.mu.Unlock()
	if count != 1 {
		t.Fatalf("replacement released a live sidecar early: count=%d", count)
	}
	if _, err := hub.NewRequestObserverForTrace(87, second.ID, second.Generation, "replace-trace"); !errors.Is(err, ErrCapacity) {
		t.Fatalf("replacement bypassed observer cap: err=%v", err)
	}
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
	hub.mu.Lock()
	count = hub.observerCount
	hub.mu.Unlock()
	if count != 0 {
		t.Fatalf("sidecar close did not release observer slot: count=%d", count)
	}
	confirmation, err = hub.IssueChallenge(87, "observer-replace")
	if err != nil {
		t.Fatal(err)
	}
	attachTestSubscriber(t, hub, 87, "observer-replace")
	if _, err := hub.SetMode(87, "observer-replace", ModeLive, confirmation); err != nil {
		t.Fatal(err)
	}
	if !hub.PublishTrace(87, "observer-replace", Trace{ID: "replace-trace-2", Payload: map[string]any{"state": "dispatching"}}) {
		t.Fatal("replacement trace was not retained")
	}
	newObserver, err := hub.NewRequestObserverForTrace(87, second.ID, second.Generation, "replace-trace-2")
	if err != nil {
		t.Fatal(err)
	}
	_ = newObserver.Close()
}

func mustMarshal(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

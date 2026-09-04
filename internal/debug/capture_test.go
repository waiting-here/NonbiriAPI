package debug

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func TestDryCaptureIsTerminalZeroExecutionAndGolden(t *testing.T) {
	clock := newDebugTestClock(1_000)
	verifier := &debugTestVerifier{state: IdentityActive}
	hub, _ := newDebugTestHub(t, clock, verifier)
	metadata := mustStartDebug(t, hub, 7, "binding-7")
	if metadata.Mode != ModeDry || metadata.Revision != "1" {
		t.Fatalf("initial metadata = %+v", metadata)
	}

	executionCalls := 0
	decision, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
		UserID: 7, RouteKind: RouteOpenAIChat, Model: "provider/model", Stream: true,
		MediaType: "application/json", Body: []byte(`{"messages":[null,false,0,""]}`),
		IdentityCertain: true, Language: "en",
	})
	if err != nil {
		t.Fatalf("DecideAfterAdmission: %v", err)
	}
	if !decision.DryIntercepted() || decision.Trace == nil || decision.ClaimPurpose != "" {
		t.Fatalf("dry decision = %+v", decision)
	}
	if !decision.DryIntercepted() {
		executionCalls++ // candidate/Vault/claim/egress/accounting/log/health branch
	}
	if executionCalls != 0 {
		t.Fatalf("dry reached execution dependencies %d times", executionCalls)
	}

	subscription, err := hub.Subscribe(context.Background(), 7, "binding-7", "")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = subscription.Close() })
	event := mustNextDebug(t, subscription)
	if event.Kind != EventSnapshot {
		t.Fatalf("initial event kind = %q", event.Kind)
	}
	snapshot := decodeDebugData[SnapshotData](t, event)
	if len(snapshot.Traces) != 1 || snapshot.Traces[0].State != TraceTerminal ||
		snapshot.Traces[0].CallerResult == nil || snapshot.Traces[0].UpstreamResult != nil {
		t.Fatalf("dry snapshot = %+v", snapshot)
	}
	assertDebugGolden(t, "dry_snapshot.golden", marshalDebugGolden(t, event))
}

func TestLiveCaptureFreezesModePurposeAndSafeProjection(t *testing.T) {
	clock := newDebugTestClock(2_000)
	verifier := &debugTestVerifier{state: IdentityActive}
	hub, _ := newDebugTestHub(t, clock, verifier)
	mustStartDebug(t, hub, 8, "binding-8")
	if _, err := hub.ChangeMode(8, "1", ModeLive, true); err != nil {
		t.Fatalf("ChangeMode: %v", err)
	}
	decision, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
		UserID: 8, RouteKind: RouteOpenAIChat, Model: strings.Repeat("界", 512),
		MediaType: "application/json", Body: []byte(`{"prompt":"owner-visible"}`),
		IdentityCertain: true, Language: "en",
	})
	if err != nil || !decision.Active || decision.Mode != ModeLive ||
		decision.ClaimPurpose != claim.PurposeDebugLive || decision.Trace == nil {
		t.Fatalf("live decision = (%+v,%v)", decision, err)
	}
	if err := decision.Trace.CompleteLiveCaptured("en"); !errors.Is(err, ErrConflict) {
		t.Fatalf("pre-dispatch live terminal = %v", err)
	}
	status := 429
	code, diag := "rate_limit_exceeded", "connector-safe diagnostic"
	upstream := DebugUpstreamResult{
		ResultKind: ResultResponse, StatusCode: &status, UpstreamCode: &code, Diag: &diag,
		Usage: LogUsage{UncachedInputTokens: "3", CacheWriteInputTokens: "2", CacheReadInputTokens: "1",
			OutputTokens: "4", TotalTokens: "10", Charge: "1.25"}, CompletedAt: 2_001,
	}
	if err := decision.Trace.RecordUpstream(upstream); !errors.Is(err, ErrConflict) {
		t.Fatalf("pre-dispatch upstream result = %v", err)
	}
	if err := decision.Trace.MarkDispatched(); err != nil {
		t.Fatalf("MarkDispatched: %v", err)
	}
	if err := decision.Trace.CompleteCaller(DebugCallerResult{
		HTTPStatus: 502, ErrorCode: stringPointer(httperr.CodeUpstream), Source: SourceUpstream,
		Message: "normalized upstream failure", CompletedAt: 2_001,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("generic upstream caller result = %v", err)
	}
	if err := decision.Trace.CompleteCaller(DebugCallerResult{
		HTTPStatus: 500, ErrorCode: stringPointer(httperr.CodeInternal), Source: SourcePlatform,
		Message: "[NonbiriAPI] safe platform failure", CompletedAt: 2_001,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("post-dispatch platform caller result = %v", err)
	}
	if err := decision.Trace.RecordUpstream(upstream); err != nil {
		t.Fatalf("RecordUpstream: %v", err)
	}
	if err := decision.Trace.CompleteLiveCaptured("en"); err != nil {
		t.Fatalf("CompleteLiveCaptured: %v", err)
	}

	hub.mu.Lock()
	record := hub.activeByUser[8].traces[decision.Trace.TraceID()]
	trace := cloneTrace(record.trace)
	hub.mu.Unlock()
	encoded, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("Marshal trace: %v", err)
	}
	noForbiddenDebugWire(t, encoded,
		"authorization", "set-cookie", "upstream_headers", "upstream_body", "raw_bytes", "sk-upstream-secret")
	if trace.UpstreamResult == nil || trace.CallerResult == nil || trace.CallerResult.HTTPStatus != 422 ||
		trace.CallerResult.ErrorCode == nil || *trace.CallerResult.ErrorCode != httperr.CodeDebugLiveResultCaptured {
		t.Fatalf("terminal trace = %+v", trace)
	}

	charity, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
		UserID: 8, RouteKind: RouteCharityChat, Model: "charity/model", Charity: true,
		MediaType: "application/json", Body: []byte(`{}`), IdentityCertain: true,
	})
	if err != nil || charity.Mode != ModeLive || charity.ClaimPurpose != claim.PurposeCharity || charity.Trace == nil {
		t.Fatalf("charity live decision = (%+v,%v)", charity, err)
	}
	if err := charity.Trace.MarkDispatched(); err != nil {
		t.Fatalf("charity MarkDispatched: %v", err)
	}
	hostileStatus := http.StatusTeapot
	hostileCode, hostileDiag := "hostile_upstream_code", "hostile real upstream diagnostic"
	hostile := DebugUpstreamResult{
		ResultKind: ResultResponse, StatusCode: &hostileStatus,
		UpstreamCode: &hostileCode, Diag: &hostileDiag,
		Usage: ZeroLogUsage(), CompletedAt: 2_002,
	}
	if err := charity.Trace.RecordUpstream(hostile); !errors.Is(err, ErrInvalid) {
		t.Fatalf("charity hostile upstream result = %v", err)
	}
	hub.mu.Lock()
	rejected := cloneTrace(hub.activeByUser[8].traces[charity.Trace.TraceID()].trace)
	hub.mu.Unlock()
	if rejected.UpstreamResult != nil {
		t.Fatalf("charity retained hostile upstream result: %+v", rejected.UpstreamResult)
	}

	safeStatus := http.StatusBadGateway
	safe := DebugUpstreamResult{
		ResultKind: ResultSynthetic, StatusCode: &safeStatus,
		Usage: ZeroLogUsage(), CompletedAt: 2_002,
	}
	if err := charity.Trace.RecordUpstream(safe); err != nil {
		t.Fatalf("charity safe RecordUpstream: %v", err)
	}
	if err := charity.Trace.CompleteLiveCaptured("en"); err != nil {
		t.Fatalf("charity CompleteLiveCaptured: %v", err)
	}
	hub.mu.Lock()
	projected := cloneTrace(hub.activeByUser[8].traces[charity.Trace.TraceID()].trace)
	hub.mu.Unlock()
	if projected.UpstreamResult == nil || projected.UpstreamResult.ResultKind != ResultSynthetic ||
		projected.UpstreamResult.StatusCode == nil || *projected.UpstreamResult.StatusCode != http.StatusBadGateway ||
		projected.UpstreamResult.UpstreamCode != nil || projected.UpstreamResult.Diag != nil {
		t.Fatalf("charity safe projection = %+v", projected.UpstreamResult)
	}
}

func TestCallerCancellationTerminatesLiveTraceWithoutUpstreamProjection(t *testing.T) {
	clock := newDebugTestClock(2_500)
	hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
	metadata := mustStartDebug(t, hub, 81, "binding-81")
	if _, err := hub.ChangeMode(81, metadata.Revision, ModeLive, true); err != nil {
		t.Fatalf("ChangeMode: %v", err)
	}
	decision, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
		UserID: 81, RouteKind: RouteOpenAIChat, Model: "caller-cancelled",
		MediaType: "application/json", Body: []byte(`{"prompt":"temporary"}`),
		IdentityCertain: true, Language: "en",
	})
	if err != nil || decision.Trace == nil || decision.Mode != ModeLive {
		t.Fatalf("live decision = (%+v,%v)", decision, err)
	}
	traceContext := decision.Trace.Context()
	if err := decision.Trace.MarkDispatched(); err != nil {
		t.Fatalf("MarkDispatched: %v", err)
	}
	if err := decision.Trace.CompleteCancelled("en"); err != nil {
		t.Fatalf("CompleteCancelled: %v", err)
	}
	select {
	case <-traceContext.Done():
	case <-time.After(time.Second):
		t.Fatal("caller cancellation did not cancel trace context")
	}
	if cause := context.Cause(traceContext); !errors.Is(cause, ErrTraceTerminal) {
		t.Fatalf("trace context cause = %v", cause)
	}

	hub.mu.Lock()
	current := hub.activeByUser[81]
	record := current.traces[decision.Trace.TraceID()]
	trace := cloneTrace(record.trace)
	inflight := current.inflight
	hub.mu.Unlock()
	if inflight != 0 || trace.State != TraceTerminal || trace.UpstreamResult != nil || trace.CallerResult == nil {
		t.Fatalf("cancelled trace = inflight:%d trace:%+v", inflight, trace)
	}
	if trace.CallerResult.HTTPStatus != 499 || trace.CallerResult.ErrorCode != nil ||
		trace.CallerResult.Source != SourcePlatform ||
		strings.Count(trace.CallerResult.Message, "[NonbiriAPI] ") != 1 {
		t.Fatalf("cancelled caller result = %+v", trace.CallerResult)
	}
	if err := decision.Trace.CompleteCancelled("en"); !errors.Is(err, ErrTraceTerminal) {
		t.Fatalf("second cancellation = %v", err)
	}
}

func TestUncertainIdentityForcesDryAndDefiniteLossTerminates(t *testing.T) {
	clock := newDebugTestClock(3_000)
	verifier := &debugTestVerifier{state: IdentityUncertain}
	hub, _ := newDebugTestHub(t, clock, verifier)
	mustStartDebug(t, hub, 9, "binding-9")
	if _, err := hub.ChangeMode(9, "1", ModeLive, true); err != nil {
		t.Fatalf("ChangeMode: %v", err)
	}
	decision, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
		UserID: 9, RouteKind: RouteOpenAIChat, Model: "m", MediaType: "application/json",
		Body: []byte(`{}`), IdentityCertain: true,
	})
	if err != nil || decision.Mode != ModeDry || decision.ClaimPurpose != "" || !decision.DryIntercepted() {
		t.Fatalf("uncertain decision = (%+v,%v)", decision, err)
	}

	verifier.set(IdentityBanned, nil)
	decision, err = hub.DecideAfterAdmission(context.Background(), CaptureInput{
		UserID: 9, RouteKind: RouteOpenAIChat, Model: "m", MediaType: "application/json",
		Body: []byte(`{}`), IdentityCertain: true,
	})
	if err != nil || decision.Active {
		t.Fatalf("banned decision = (%+v,%v)", decision, err)
	}
	metadata, err := hub.Metadata(9)
	if err != nil || metadata.Active {
		t.Fatalf("metadata after ban = (%+v,%v)", metadata, err)
	}
}

func TestDebugBodyAndEventDTORejectNonCanonicalData(t *testing.T) {
	empty := ""
	if !(DebugBody{MediaType: "application/json", ByteCount: 0, Text: &empty}).valid() {
		t.Fatal("empty text body should be valid and non-null")
	}
	cases := []DebugBody{
		{MediaType: "application/json\nsecret", ByteCount: 1, Text: &empty},
		{MediaType: "application/json", ByteCount: 0},
		{MediaType: "application/json", ByteCount: 0, Text: &empty, Base64: &empty},
		{MediaType: "application/octet-stream", ByteCount: 1, Base64: stringPointer("***")},
		{MediaType: "application/json", ByteCount: 1, Text: &empty, Truncated: false},
	}
	for index, body := range cases {
		if body.valid() {
			t.Fatalf("invalid body %d accepted: %+v", index, body)
		}
	}
	if (DebugRequest{RouteKind: RouteKind("model_discovery"), Model: "m",
		Body: DebugBody{MediaType: "application/json", ByteCount: 0, Text: &empty}}).valid() {
		t.Fatal("model discovery is outside Debug chat capture")
	}
	canonicalID := testOpaqueID("dbs_", 1)
	nonCanonicalID := canonicalID[:len(canonicalID)-1] + "R"
	if validOID(nonCanonicalID, "dbs_") {
		t.Fatal("non-canonical opaque ID was accepted")
	}

	data := []byte(`{"reason":"cursor_invalid","first_available_event_id":null,"extra":true}`)
	if validateEventData(EventGap, data) == nil {
		t.Fatal("unknown event data field was accepted")
	}
	if validateEventData(EventKind("future"), json.RawMessage(`{}`)) == nil {
		t.Fatal("unknown event kind was accepted")
	}
}

func TestOwnerBodyTruncationIsExplicitAndSSEBounded(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   []byte
		binary bool
	}{
		{"text", []byte(strings.Repeat("界", 200_000)), false},
		{"binary", bytes.Repeat([]byte{0xff, 0x00, 0xfe}, 150_000), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			clock := newDebugTestClock(4_000)
			hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
			mustStartDebug(t, hub, 44, "binding-44")
			decision, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
				UserID: 44, RouteKind: RouteOpenAIChat, Model: "m", MediaType: "application/octet-stream",
				Body: test.body, IdentityCertain: true,
			})
			if err != nil || decision.Trace == nil || !decision.DryIntercepted() {
				t.Fatalf("capture = (%+v,%v)", decision, err)
			}
			hub.mu.Lock()
			current := hub.activeByUser[44]
			record := current.traces[decision.Trace.TraceID()]
			trace := cloneTrace(record.trace)
			sessionBytes, eventCount := current.bytes(), len(current.events)
			hub.mu.Unlock()
			if !trace.Truncated || !trace.Request.Body.Truncated || trace.Request.Body.ByteCount != int64(len(test.body)) ||
				record.wireBytes > MaxTraceBytes || sessionBytes > MaxSessionBytes || eventCount > MaxSessionEvents {
				t.Fatalf("bounded trace = trace:%+v wire:%d session:%d events:%d", trace, record.wireBytes, sessionBytes, eventCount)
			}
			if test.binary {
				if trace.Request.Body.Base64 == nil || trace.Request.Body.Text != nil {
					t.Fatalf("binary body projection = %+v", trace.Request.Body)
				}
			} else if trace.Request.Body.Text == nil || trace.Request.Body.Base64 != nil || !utf8.ValidString(*trace.Request.Body.Text) {
				t.Fatalf("text body projection = %+v", trace.Request.Body)
			}
			subscription, err := hub.Subscribe(context.Background(), 44, "binding-44", "")
			if err != nil {
				t.Fatal(err)
			}
			event := mustNextDebug(t, subscription)
			encoded, err := json.Marshal(event)
			if err != nil || len(encoded) > MaxEventBytes {
				t.Fatalf("snapshot bytes = %d, err=%v", len(encoded), err)
			}
			snapshot := decodeDebugData[SnapshotData](t, event)
			if len(snapshot.Traces) != 1 || !snapshot.Traces[0].Truncated {
				t.Fatalf("truncated snapshot = %+v", snapshot)
			}
		})
	}
}

func TestFixedDebugCallerResponsesGolden(t *testing.T) {
	type response struct {
		Status      int         `json:"status"`
		Mode        string      `json:"mode"`
		Body        interface{} `json:"body"`
		ContentType string      `json:"content_type"`
		NoStore     string      `json:"cache_control"`
	}
	capture := func(write func(http.ResponseWriter)) response {
		recorder := httptest.NewRecorder()
		write(recorder)
		var body any
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode fixed response: %v", err)
		}
		return response{Status: recorder.Code, Mode: recorder.Header().Get("X-Nonbiri-Debug-Mode"),
			Body: body, ContentType: recorder.Header().Get("Content-Type"),
			NoStore: recorder.Header().Get("Cache-Control")}
	}
	dry := capture(func(writer http.ResponseWriter) { WriteDryIntercepted(writer, "en") })
	live := capture(func(writer http.ResponseWriter) { WriteLiveCaptured(writer, "en") })
	cancelled := capture(func(writer http.ResponseWriter) { WriteLiveCancelled(writer, "en", false) })
	streamRecorder := httptest.NewRecorder()
	WriteLiveCancelled(streamRecorder, "en", true)
	result := struct {
		Dry       response `json:"dry"`
		Live      response `json:"live"`
		Cancelled response `json:"cancelled"`
		Stream    string   `json:"stream"`
	}{dry, live, cancelled, streamRecorder.Body.String()}
	encoded := marshalDebugGolden(t, result)
	if strings.Count(string(encoded), "[NonbiriAPI] ") != 4 {
		t.Fatalf("platform prefix count = %d\n%s", strings.Count(string(encoded), "[NonbiriAPI] "), encoded)
	}
	if len(streamRecorder.Body.Bytes()) > 4*1024 {
		t.Fatalf("stream terminator = %d bytes", streamRecorder.Body.Len())
	}
	assertDebugGolden(t, "fixed_responses.golden", encoded)
}

package debug

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func TestDebugV2ClosedWireGolden(t *testing.T) {
	text := `{"messages":[null,false,0,""]}`
	binary := base64.StdEncoding.EncodeToString([]byte{0x00, 0xff, 0x7f})
	textBody := DebugBody{
		MediaType: "application/json", ByteCount: int64(len(text)), Text: &text,
	}
	binaryBody := DebugBody{
		MediaType: "application/octet-stream", ByteCount: 3, Base64: &binary,
	}
	usage := LogUsage{
		UncachedInputTokens: "3", CacheWriteInputTokens: "2", CacheReadInputTokens: "1",
		OutputTokens: "4", TotalTokens: "10", UsageUnknown: false, Charge: "1.25",
	}
	unknownUsage := LogUsage{
		UncachedInputTokens: "0", CacheWriteInputTokens: "0", CacheReadInputTokens: "0",
		OutputTokens: "0", TotalTokens: "0", UsageUnknown: true, Charge: "0",
	}
	responseStatus, syntheticStatus := 429, 504
	responseCode, syntheticCode := "rate_limit", "timeout"
	responseDiag, syntheticDiag := "connector-safe rate diagnostic", "connector-safe timeout diagnostic"
	responseResult := DebugUpstreamResult{
		ResultKind: ResultResponse, StatusCode: &responseStatus, UpstreamCode: &responseCode,
		Diag: &responseDiag, Usage: usage, CompletedAt: 1_010,
	}
	syntheticResult := DebugUpstreamResult{
		ResultKind: ResultSynthetic, StatusCode: &syntheticStatus, UpstreamCode: &syntheticCode,
		Diag: &syntheticDiag, Usage: unknownUsage, CompletedAt: 1_011,
	}
	dryCode := httperr.CodeDebugDryRunIntercepted
	liveCode := httperr.CodeDebugLiveResultCaptured
	upstreamErrorCode := httperr.CodeUpstream
	dryCaller := DebugCallerResult{
		HTTPStatus: 422, ErrorCode: &dryCode, Source: SourcePlatform,
		Message: "[NonbiriAPI] Debug dry run intercepted this request.", CompletedAt: 1_002,
	}
	liveCaller := DebugCallerResult{
		HTTPStatus: 422, ErrorCode: &liveCode, Source: SourcePlatform,
		Message: "[NonbiriAPI] Debug live result was captured.", CompletedAt: 1_010,
	}
	upstreamCaller := DebugCallerResult{
		HTTPStatus: 502, ErrorCode: &upstreamErrorCode, Source: SourceUpstream,
		Message: "normalized upstream failure", CompletedAt: 1_011,
	}
	cancelledCaller := DebugCallerResult{
		HTTPStatus: 499, Source: SourcePlatform,
		Message: "[NonbiriAPI] The caller cancelled the request.", CompletedAt: 1_012,
	}
	capturing := DebugTrace{
		TraceID: testOpaqueID("dbt_", 1), Revision: "1", State: TraceCapturing,
		Request:   DebugRequest{RouteKind: RouteOpenAIChat, Model: "provider/model", Stream: true, Body: textBody},
		CreatedAt: 1_000, UpdatedAt: 1_000,
	}
	terminalResponse := DebugTrace{
		TraceID: testOpaqueID("dbt_", 2), Revision: "3", State: TraceTerminal,
		Request:        DebugRequest{RouteKind: RouteCharityChat, Model: "公益/模型", Body: binaryBody},
		UpstreamResult: &responseResult, CallerResult: &liveCaller,
		CreatedAt: 1_001, UpdatedAt: 1_010,
	}
	sessionID := testOpaqueID("dbs_", 1)
	firstEventID := testOpaqueID("dbe_", 90)
	lastEventID := testOpaqueID("dbe_", 99)
	active := DebugSessionMetadata{
		Active: true, ID: sessionID, Generation: "3", Revision: "7", Mode: ModeLive,
		CreatedAt: 1_000, ExpiresAt: 4_600, IdleExpiresAt: 1_600,
		InflightCount: 1, ConnectedSubscribers: 2, LastEventID: &lastEventID,
		Limits: fixedSessionLimits(),
	}

	makePayload := func(value any) json.RawMessage {
		t.Helper()
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal event payload: %v", err)
		}
		return encoded
	}
	makeEvent := func(sequence uint64, kind EventKind, payload any) EventEnvelope {
		t.Helper()
		data := makePayload(payload)
		if err := validateEventData(kind, data); err != nil {
			t.Fatalf("validate %s payload: %v\n%s", kind, err, data)
		}
		return EventEnvelope{
			Version: 2, EventID: testOpaqueID("dbe_", sequence), SessionID: sessionID,
			Generation: "3", Kind: kind, OccurredAt: 1_100 + int64(sequence), Data: data,
		}
	}

	events := []EventEnvelope{
		makeEvent(100, EventSnapshot, SnapshotData{
			Session: active, Traces: []DebugTrace{capturing},
			FirstEventID: &firstEventID, LastEventID: &lastEventID,
		}),
		makeEvent(101, EventTraceUpsert, terminalResponse),
	}
	gapReasons := []GapReason{
		GapCursorInvalid, GapProcessRestart, GapRingExpired, GapRingEvicted, GapSlowConsumer,
	}
	for index, reason := range gapReasons {
		var first *string
		if index != 0 {
			first = &firstEventID
		}
		events = append(events, makeEvent(uint64(110+index), EventGap, GapData{
			Reason: reason, FirstAvailableEventID: first,
		}))
	}
	endReasons := []EndReason{
		EndStopped, EndReplaced, EndIdleExpired, EndAbsoluteExpired,
		EndAuthRevoked, EndAccountBanned, EndAccountDeleted, EndShutdown,
	}
	for index, reason := range endReasons {
		events = append(events, makeEvent(uint64(120+index), EventSessionEnd, SessionEndData{
			Reason: reason, CancelledInflightCount: index % 3,
		}))
	}

	catalog := struct {
		Modes           []Mode                 `json:"modes"`
		RouteKinds      []RouteKind            `json:"route_kinds"`
		TraceStates     []TraceState           `json:"trace_states"`
		ResultKinds     []ResultKind           `json:"result_kinds"`
		ResultSources   []ResultSource         `json:"result_sources"`
		EventKinds      []EventKind            `json:"event_kinds"`
		GapReasons      []GapReason            `json:"gap_reasons"`
		EndReasons      []EndReason            `json:"end_reasons"`
		Metadata        []DebugSessionMetadata `json:"metadata"`
		Bodies          []DebugBody            `json:"bodies"`
		UpstreamResults []DebugUpstreamResult  `json:"upstream_results"`
		CallerResults   []DebugCallerResult    `json:"caller_results"`
		Events          []EventEnvelope        `json:"events"`
	}{
		Modes:         []Mode{ModeDry, ModeLive},
		RouteKinds:    []RouteKind{RouteOpenAIChat, RouteCharityChat},
		TraceStates:   []TraceState{TraceCapturing, TraceTerminal},
		ResultKinds:   []ResultKind{ResultResponse, ResultSynthetic},
		ResultSources: []ResultSource{SourcePlatform, SourceUpstream},
		EventKinds:    []EventKind{EventSnapshot, EventTraceUpsert, EventGap, EventSessionEnd},
		GapReasons:    gapReasons, EndReasons: endReasons,
		Metadata:        []DebugSessionMetadata{{Active: false}, active},
		Bodies:          []DebugBody{textBody, binaryBody},
		UpstreamResults: []DebugUpstreamResult{responseResult, syntheticResult},
		CallerResults:   []DebugCallerResult{dryCaller, liveCaller, upstreamCaller, cancelledCaller},
		Events:          events,
	}
	for _, body := range catalog.Bodies {
		if !body.valid() {
			t.Fatalf("catalog contains invalid body: %+v", body)
		}
	}
	for _, result := range catalog.UpstreamResults {
		if !result.valid() {
			t.Fatalf("catalog contains invalid upstream result: %+v", result)
		}
	}
	for _, caller := range catalog.CallerResults {
		if !caller.valid() {
			t.Fatalf("catalog contains invalid caller result: %+v", caller)
		}
	}
	encoded := marshalDebugGolden(t, catalog)
	noForbiddenDebugWire(t, encoded,
		"authorization", "cookie", "set-cookie", "upstream_headers", "upstream_body",
		"raw_upstream", "raw_bytes", "ciphertext", "sk-upstream-secret",
	)
	assertDebugGolden(t, "debug_v2_wire.golden", encoded)

	for _, event := range events {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(event.Data, &object); err != nil {
			t.Fatalf("decode %s payload for hostile field: %v", event.Kind, err)
		}
		object["future_field"] = json.RawMessage(`true`)
		hostile, err := json.Marshal(object)
		if err != nil {
			t.Fatalf("marshal hostile %s payload: %v", event.Kind, err)
		}
		if validateEventData(event.Kind, hostile) == nil {
			t.Fatalf("%s payload accepted unknown field: %s", event.Kind, hostile)
		}
	}
}

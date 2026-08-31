// Package debug implements the process-local, bounded Debug v2 capture plane.
// It deliberately accepts only the owner's request body and connector-safe
// structured outcomes. Raw upstream headers, bodies, errors, and bytes are not
// representable by any exported capture type in this package.
package debug

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

const (
	MaxGlobalSessions          = 64
	MaxGlobalBytes       int64 = 128 * 1024 * 1024
	MaxSessionBytes            = 4 * 1024 * 1024
	MaxSessionTraces           = 32
	MaxSessionEvents           = 128
	MaxSubscribers             = 2
	MaxEventBytes              = 512 * 1024
	MaxTraceBytes              = 768 * 1024
	SubscriberQueueSize        = 64
	MaxUpstreamCodeBytes       = 64
	MaxDiagnosticBytes         = 4096
	maxUnixSecond        int64 = 253402300799
)

var (
	ErrClosed          = errors.New("debug hub is closed")
	ErrNoActiveSession = errors.New("debug session is not active")
	ErrCapacity        = errors.New("debug capacity exhausted")
	ErrConflict        = errors.New("debug state conflict")
	ErrInvalid         = errors.New("invalid debug input")
	ErrSessionEnded    = errors.New("debug session ended")
	ErrTraceTerminal   = errors.New("debug trace is terminal")
)

type Mode string

const (
	ModeDry  Mode = "dry"
	ModeLive Mode = "live"
)

func (mode Mode) valid() bool { return mode == ModeDry || mode == ModeLive }

type RouteKind string

const (
	RouteOpenAIChat  RouteKind = "openai_chat_completions"
	RouteCharityChat RouteKind = "charity_chat_completions"
)

func (kind RouteKind) valid() bool {
	return kind == RouteOpenAIChat || kind == RouteCharityChat
}

type TraceState string

const (
	TraceCapturing TraceState = "capturing"
	TraceTerminal  TraceState = "terminal"
)

type ResultKind string

const (
	ResultResponse  ResultKind = "response"
	ResultSynthetic ResultKind = "synthetic"
)

func (kind ResultKind) valid() bool { return kind == ResultResponse || kind == ResultSynthetic }

type ResultSource string

const (
	SourcePlatform ResultSource = "platform"
	SourceUpstream ResultSource = "upstream"
)

func (source ResultSource) valid() bool { return source == SourcePlatform || source == SourceUpstream }

// LogUsage is the complete Debug wire projection of usage and charge. Every
// count and charge is a canonical decimal string supplied by the accounting
// boundary; Debug never calculates or persists accounting facts.
type LogUsage struct {
	UncachedInputTokens   string `json:"uncached_input_tokens"`
	CacheWriteInputTokens string `json:"cache_write_input_tokens"`
	CacheReadInputTokens  string `json:"cache_read_input_tokens"`
	OutputTokens          string `json:"output_tokens"`
	TotalTokens           string `json:"total_tokens"`
	UsageUnknown          bool   `json:"usage_unknown"`
	Charge                string `json:"charge"`
}

func ZeroLogUsage() LogUsage {
	return LogUsage{
		UncachedInputTokens: "0", CacheWriteInputTokens: "0", CacheReadInputTokens: "0",
		OutputTokens: "0", TotalTokens: "0", Charge: "0",
	}
}

func (usage LogUsage) valid() bool {
	if !canonicalUnsigned(usage.UncachedInputTokens) || !canonicalUnsigned(usage.CacheWriteInputTokens) ||
		!canonicalUnsigned(usage.CacheReadInputTokens) ||
		!canonicalUnsigned(usage.OutputTokens) || !canonicalUnsigned(usage.TotalTokens) || !canonicalAmount(usage.Charge) {
		return false
	}
	total := new(big.Int)
	for _, value := range []string{usage.UncachedInputTokens, usage.CacheWriteInputTokens, usage.CacheReadInputTokens, usage.OutputTokens} {
		part, ok := new(big.Int).SetString(value, 10)
		if !ok {
			return false
		}
		total.Add(total, part)
	}
	return total.String() == usage.TotalTokens
}

// DebugBody is the only body-shaped value in the package. It represents the
// authenticated owner's request bytes; exactly one of Text and Base64 is set.
type DebugBody struct {
	MediaType string  `json:"media_type"`
	ByteCount int64   `json:"byte_count"`
	Text      *string `json:"text"`
	Base64    *string `json:"base64"`
	Truncated bool    `json:"truncated"`
}

func (body DebugBody) valid() bool {
	if !safeMediaType(body.MediaType) || body.ByteCount < 0 || ((body.Text == nil) == (body.Base64 == nil)) {
		return false
	}
	var captured int64
	switch {
	case body.Text != nil:
		if !utf8.ValidString(*body.Text) {
			return false
		}
		captured = int64(len(*body.Text))
	case body.Base64 != nil:
		decoded, err := base64.StdEncoding.DecodeString(*body.Base64)
		if err != nil {
			return false
		}
		captured = int64(len(decoded))
	}
	if captured > body.ByteCount {
		return false
	}
	return body.Truncated == (captured < body.ByteCount)
}

type DebugRequest struct {
	RouteKind RouteKind `json:"route_kind"`
	Model     string    `json:"model"`
	Stream    bool      `json:"stream"`
	Body      DebugBody `json:"body"`
}

func (request DebugRequest) valid() bool {
	return request.RouteKind.valid() && utf8.ValidString(request.Model) &&
		utf8.RuneCountInString(request.Model) <= 512 && request.Body.valid()
}

// DebugUpstreamResult cannot hold raw headers, bodies, messages, errors, or
// bytes. Connector integration must first reduce an outcome to these bounded
// structured fields.
type DebugUpstreamResult struct {
	ResultKind   ResultKind `json:"result_kind"`
	StatusCode   *int       `json:"status_code"`
	UpstreamCode *string    `json:"upstream_code"`
	Diag         *string    `json:"diag"`
	Usage        LogUsage   `json:"usage"`
	CompletedAt  int64      `json:"completed_at"`
}

func (result DebugUpstreamResult) valid() bool {
	if !result.ResultKind.valid() || result.CompletedAt < 0 || !result.Usage.valid() {
		return false
	}
	if result.StatusCode != nil && (*result.StatusCode < 100 || *result.StatusCode > 599) {
		return false
	}
	if result.UpstreamCode != nil && !safeUpstreamCode(*result.UpstreamCode) {
		return false
	}
	return result.Diag == nil || safeDiagnostic(*result.Diag)
}

type DebugCallerResult struct {
	HTTPStatus  int          `json:"http_status"`
	ErrorCode   *string      `json:"error_code"`
	Source      ResultSource `json:"source"`
	Message     string       `json:"message"`
	CompletedAt int64        `json:"completed_at"`
}

func (result DebugCallerResult) valid() bool {
	if result.HTTPStatus < 100 || result.HTTPStatus > 599 || !result.Source.valid() ||
		result.CompletedAt < 0 || !safeCallerMessage(result.Message) {
		return false
	}
	if result.ErrorCode == nil {
		if result.Source == SourcePlatform && !hasOnePlatformPrefix(result.Message) {
			return false
		}
		if result.Source == SourceUpstream && strings.Contains(result.Message, "[NonbiriAPI] ") {
			return false
		}
		return result.HTTPStatus >= 200 && result.HTTPStatus <= 399 || result.HTTPStatus == 499
	}
	if result.HTTPStatus < 400 || !httperr.IsStableCode(*result.ErrorCode) {
		return false
	}
	if *result.ErrorCode == httperr.CodeUpstream {
		return result.Source == SourceUpstream && !strings.Contains(result.Message, "[NonbiriAPI] ")
	}
	return result.Source == SourcePlatform && hasOnePlatformPrefix(result.Message)
}

type DebugTrace struct {
	TraceID        string               `json:"trace_id"`
	Revision       string               `json:"revision"`
	State          TraceState           `json:"state"`
	Request        DebugRequest         `json:"request"`
	UpstreamResult *DebugUpstreamResult `json:"upstream_result"`
	CallerResult   *DebugCallerResult   `json:"caller_result"`
	CreatedAt      int64                `json:"created_at"`
	UpdatedAt      int64                `json:"updated_at"`
	Truncated      bool                 `json:"truncated"`
}

func (trace DebugTrace) valid() bool {
	if !validOID(trace.TraceID, "dbt_") || !canonicalPositive(trace.Revision) ||
		(trace.State != TraceCapturing && trace.State != TraceTerminal) || !trace.Request.valid() ||
		trace.CreatedAt < 0 || trace.UpdatedAt < trace.CreatedAt {
		return false
	}
	if trace.UpstreamResult != nil && !trace.UpstreamResult.valid() {
		return false
	}
	if trace.CallerResult != nil && !trace.CallerResult.valid() {
		return false
	}
	return trace.Truncated == trace.Request.Body.Truncated &&
		((trace.State == TraceCapturing && trace.CallerResult == nil) ||
			(trace.State == TraceTerminal && trace.CallerResult != nil))
}

type SessionLimits struct {
	SessionBytes int `json:"session_bytes"`
	Traces       int `json:"traces"`
	Events       int `json:"events"`
	Subscribers  int `json:"subscribers"`
	EventBytes   int `json:"event_bytes"`
	TraceBytes   int `json:"trace_bytes"`
}

// DebugSessionMetadata is a strict two-branch union. Its custom marshaler
// emits exactly {"active":false} for the inactive branch and all frozen fields
// (including a null last_event_id) for the active branch.
type DebugSessionMetadata struct {
	Active               bool
	ID                   string
	Generation           string
	Revision             string
	Mode                 Mode
	CreatedAt            int64
	ExpiresAt            int64
	IdleExpiresAt        int64
	InflightCount        int
	ConnectedSubscribers int
	LastEventID          *string
	Limits               SessionLimits
}

func (metadata DebugSessionMetadata) MarshalJSON() ([]byte, error) {
	if !metadata.Active {
		return []byte(`{"active":false}`), nil
	}
	type activeWire struct {
		Active               bool          `json:"active"`
		ID                   string        `json:"id"`
		Generation           string        `json:"generation"`
		Revision             string        `json:"revision"`
		Mode                 Mode          `json:"mode"`
		CreatedAt            int64         `json:"created_at"`
		ExpiresAt            int64         `json:"expires_at"`
		IdleExpiresAt        int64         `json:"idle_expires_at"`
		InflightCount        int           `json:"inflight_count"`
		ConnectedSubscribers int           `json:"connected_subscribers"`
		LastEventID          *string       `json:"last_event_id"`
		Limits               SessionLimits `json:"limits"`
	}
	if !validOID(metadata.ID, "dbs_") || !canonicalPositive(metadata.Generation) ||
		!canonicalPositive(metadata.Revision) || !metadata.Mode.valid() || metadata.CreatedAt < 0 ||
		metadata.ExpiresAt < metadata.CreatedAt || metadata.ExpiresAt > maxUnixSecond ||
		metadata.IdleExpiresAt < metadata.CreatedAt || metadata.IdleExpiresAt > metadata.ExpiresAt ||
		metadata.InflightCount < 0 || metadata.ConnectedSubscribers < 0 || metadata.ConnectedSubscribers > MaxSubscribers ||
		metadata.Limits != fixedSessionLimits() || (metadata.LastEventID != nil && !validOID(*metadata.LastEventID, "dbe_")) {
		return nil, ErrInvalid
	}
	return json.Marshal(activeWire{
		Active: true, ID: metadata.ID, Generation: metadata.Generation, Revision: metadata.Revision,
		Mode: metadata.Mode, CreatedAt: metadata.CreatedAt, ExpiresAt: metadata.ExpiresAt,
		IdleExpiresAt: metadata.IdleExpiresAt, InflightCount: metadata.InflightCount,
		ConnectedSubscribers: metadata.ConnectedSubscribers, LastEventID: metadata.LastEventID,
		Limits: metadata.Limits,
	})
}

func (metadata *DebugSessionMetadata) UnmarshalJSON(data []byte) error {
	if metadata == nil {
		return ErrInvalid
	}
	var discriminator struct {
		Active *bool `json:"active"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil || discriminator.Active == nil {
		return ErrInvalid
	}
	if !*discriminator.Active {
		var inactive map[string]json.RawMessage
		if err := json.Unmarshal(data, &inactive); err != nil || len(inactive) != 1 {
			return ErrInvalid
		}
		*metadata = DebugSessionMetadata{Active: false}
		return nil
	}
	type activeWire struct {
		Active               bool          `json:"active"`
		ID                   string        `json:"id"`
		Generation           string        `json:"generation"`
		Revision             string        `json:"revision"`
		Mode                 Mode          `json:"mode"`
		CreatedAt            int64         `json:"created_at"`
		ExpiresAt            int64         `json:"expires_at"`
		IdleExpiresAt        int64         `json:"idle_expires_at"`
		InflightCount        int           `json:"inflight_count"`
		ConnectedSubscribers int           `json:"connected_subscribers"`
		LastEventID          *string       `json:"last_event_id"`
		Limits               SessionLimits `json:"limits"`
	}
	var wire activeWire
	if err := decodeClosedJSON(data, &wire); err != nil {
		return ErrInvalid
	}
	result := DebugSessionMetadata{
		Active: wire.Active, ID: wire.ID, Generation: wire.Generation, Revision: wire.Revision,
		Mode: wire.Mode, CreatedAt: wire.CreatedAt, ExpiresAt: wire.ExpiresAt,
		IdleExpiresAt: wire.IdleExpiresAt, InflightCount: wire.InflightCount,
		ConnectedSubscribers: wire.ConnectedSubscribers, LastEventID: wire.LastEventID, Limits: wire.Limits,
	}
	if _, err := result.MarshalJSON(); err != nil {
		return ErrInvalid
	}
	*metadata = result
	return nil
}

func fixedSessionLimits() SessionLimits {
	return SessionLimits{
		SessionBytes: MaxSessionBytes, Traces: MaxSessionTraces, Events: MaxSessionEvents,
		Subscribers: MaxSubscribers, EventBytes: MaxEventBytes, TraceBytes: MaxTraceBytes,
	}
}

type EventKind string

const (
	EventSnapshot    EventKind = "snapshot"
	EventTraceUpsert EventKind = "trace_upsert"
	EventGap         EventKind = "gap"
	EventSessionEnd  EventKind = "session_end"
)

type GapReason string

const (
	GapCursorInvalid  GapReason = "cursor_invalid"
	GapProcessRestart GapReason = "process_restart"
	GapRingExpired    GapReason = "ring_expired"
	GapRingEvicted    GapReason = "ring_evicted"
	GapSlowConsumer   GapReason = "slow_consumer"
)

type EndReason string

const (
	EndStopped         EndReason = "stopped"
	EndReplaced        EndReason = "replaced"
	EndIdleExpired     EndReason = "idle_expired"
	EndAbsoluteExpired EndReason = "absolute_expired"
	EndAuthRevoked     EndReason = "auth_revoked"
	EndAccountBanned   EndReason = "account_banned"
	EndAccountDeleted  EndReason = "account_deleted"
	EndShutdown        EndReason = "shutdown"
)

type SnapshotData struct {
	Session      DebugSessionMetadata `json:"session"`
	Traces       []DebugTrace         `json:"traces"`
	FirstEventID *string              `json:"first_event_id"`
	LastEventID  *string              `json:"last_event_id"`
}

type GapData struct {
	Reason                GapReason `json:"reason"`
	FirstAvailableEventID *string   `json:"first_available_event_id"`
}

type SessionEndData struct {
	Reason                 EndReason `json:"reason"`
	CancelledInflightCount int       `json:"cancelled_inflight_count"`
}

// EventEnvelope is the dedicated Debug v2 envelope. Data is produced only by
// Hub constructors, which validate the kind-specific closed payload before an
// event can enter a ring or subscriber queue.
type EventEnvelope struct {
	Version    int             `json:"version"`
	EventID    string          `json:"event_id"`
	SessionID  string          `json:"session_id"`
	Generation string          `json:"generation"`
	Kind       EventKind       `json:"kind"`
	OccurredAt int64           `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

func (event EventEnvelope) clone() EventEnvelope {
	copyEvent := event
	copyEvent.Data = append(json.RawMessage(nil), event.Data...)
	return copyEvent
}

func canonicalUnsigned(value string) bool {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func canonicalPositive(value string) bool { return canonicalUnsigned(value) && value != "0" }

func canonicalAmount(value string) bool {
	if value == "" || value[0] == '+' || value[0] == '-' {
		return false
	}
	dot := -1
	for i := range value {
		switch {
		case value[i] == '.' && dot == -1:
			dot = i
		case value[i] < '0' || value[i] > '9':
			return false
		}
	}
	if dot == -1 {
		return canonicalUnsigned(value)
	}
	return dot > 0 && dot < len(value)-1 && len(value)-dot-1 <= 3 &&
		canonicalUnsigned(value[:dot]) && value[len(value)-1] != '0'
}

func safeUpstreamCode(value string) bool {
	if len(value) < 1 || len(value) > MaxUpstreamCodeBytes {
		return false
	}
	for i := range value {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func safeDiagnostic(value string) bool {
	if !utf8.ValidString(value) || len(value) > MaxDiagnosticBytes {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func safePlatformCode(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for i := range value {
		if value[i] != '_' && (value[i] < 'a' || value[i] > 'z') && (value[i] < '0' || value[i] > '9') {
			return false
		}
	}
	return true
}

func validOID(value, prefix string) bool {
	return db.ValidateOpaqueID(value, prefix)
}

func safeMediaType(value string) bool {
	if value == "" || !utf8.ValidString(value) || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func safeCallerMessage(value string) bool {
	if value == "" || !utf8.ValidString(value) || len(value) > 1024 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func hasOnePlatformPrefix(value string) bool {
	return strings.HasPrefix(value, "[NonbiriAPI] ") && strings.Count(value, "[NonbiriAPI] ") == 1
}

func statusPointer(status int) *int {
	if status == 0 {
		return nil
	}
	copyStatus := status
	return &copyStatus
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	copyValue := value
	return &copyValue
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneTrace(trace DebugTrace) DebugTrace {
	copyTrace := trace
	copyTrace.Request.Body.Text = cloneString(trace.Request.Body.Text)
	copyTrace.Request.Body.Base64 = cloneString(trace.Request.Body.Base64)
	if trace.UpstreamResult != nil {
		upstream := *trace.UpstreamResult
		upstream.StatusCode = statusPointer(valueOrZero(trace.UpstreamResult.StatusCode))
		upstream.UpstreamCode = cloneString(trace.UpstreamResult.UpstreamCode)
		upstream.Diag = cloneString(trace.UpstreamResult.Diag)
		copyTrace.UpstreamResult = &upstream
	}
	if trace.CallerResult != nil {
		caller := *trace.CallerResult
		caller.ErrorCode = cloneString(trace.CallerResult.ErrorCode)
		copyTrace.CallerResult = &caller
	}
	return copyTrace
}

func valueOrZero(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func validateEventData(kind EventKind, data json.RawMessage) error {
	if len(data) == 0 || !json.Valid(data) {
		return ErrInvalid
	}
	var target any
	switch kind {
	case EventSnapshot:
		target = &SnapshotData{}
	case EventTraceUpsert:
		target = &DebugTrace{}
	case EventGap:
		target = &GapData{}
	case EventSessionEnd:
		target = &SessionEndData{}
	default:
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	switch value := target.(type) {
	case *SnapshotData:
		if !value.Session.Active || len(value.Traces) > MaxSessionTraces ||
			(value.FirstEventID != nil && !validOID(*value.FirstEventID, "dbe_")) ||
			(value.LastEventID != nil && !validOID(*value.LastEventID, "dbe_")) ||
			((value.FirstEventID == nil) != (value.LastEventID == nil)) {
			return ErrInvalid
		}
		seen := make(map[string]struct{}, len(value.Traces))
		for _, trace := range value.Traces {
			if !trace.valid() {
				return ErrInvalid
			}
			if _, exists := seen[trace.TraceID]; exists {
				return ErrInvalid
			}
			seen[trace.TraceID] = struct{}{}
		}
	case *DebugTrace:
		if !value.valid() {
			return ErrInvalid
		}
	case *GapData:
		if !validGapReason(value.Reason) ||
			(value.FirstAvailableEventID != nil && !validOID(*value.FirstAvailableEventID, "dbe_")) {
			return ErrInvalid
		}
	case *SessionEndData:
		if !validEndReason(value.Reason) || value.CancelledInflightCount < 0 || value.CancelledInflightCount > MaxSessionTraces {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func decodeClosedJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

func validGapReason(reason GapReason) bool {
	return reason == GapCursorInvalid || reason == GapProcessRestart || reason == GapRingExpired ||
		reason == GapRingEvicted || reason == GapSlowConsumer
}

func validEndReason(reason EndReason) bool {
	return reason == EndStopped || reason == EndReplaced || reason == EndIdleExpired ||
		reason == EndAbsoluteExpired || reason == EndAuthRevoked || reason == EndAccountBanned ||
		reason == EndAccountDeleted || reason == EndShutdown
}

func writeJSONError(writer http.ResponseWriter, status int, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal debug response: %w", err)
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_, err = writer.Write(append(encoded, '\n'))
	return err
}

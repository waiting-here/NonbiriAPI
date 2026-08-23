package httperr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

func decodeEnvelope(t *testing.T, body string) Envelope {
	t.Helper()
	var env Envelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("decode envelope: %v\nbody=%s", err, body)
	}
	return env
}

func TestEnvelopeShapeAndStableCode(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, New(CodeInvalidRequest, "model field is required").WithDiag("got empty model"))

	if got := rec.Code; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("cache-control = %q, want no-store", cc)
	}
	env := decodeEnvelope(t, rec.Body.String())
	if env.Error.Code != CodeInvalidRequest {
		t.Fatalf("code = %q, want %q", env.Error.Code, CodeInvalidRequest)
	}
	if env.Error.Source != SourcePlatform {
		t.Fatalf("source = %q, want %q", env.Error.Source, SourcePlatform)
	}
	if env.Error.Message != "model field is required" {
		t.Fatalf("message = %q", env.Error.Message)
	}
	if env.Error.Diag != "got empty model" {
		t.Fatalf("diag = %q", env.Error.Diag)
	}
	// request_id omitted when unset.
	if strings.Contains(rec.Body.String(), `"request_id"`) {
		t.Fatalf("unexpected request_id in envelope: %s", rec.Body.String())
	}
	// source is always present on the wire, even when the caller did not set it.
	if !strings.Contains(rec.Body.String(), `"source":"platform"`) {
		t.Fatalf("envelope missing source: %s", rec.Body.String())
	}
}

func TestStatusMapping(t *testing.T) {
	cases := map[string]int{
		CodeInvalidRequest:        http.StatusBadRequest,
		CodeUnauthorized:          http.StatusUnauthorized,
		CodeForbidden:             http.StatusForbidden,
		CodeNotFound:              http.StatusNotFound,
		CodeConflict:              http.StatusConflict,
		CodeMethodNotAllowed:      http.StatusMethodNotAllowed,
		CodeRateLimited:           http.StatusTooManyRequests,
		CodePayloadTooLarge:       http.StatusRequestEntityTooLarge,
		CodeUnboundModel:          http.StatusServiceUnavailable,
		CodeUpstream:              http.StatusBadGateway,
		CodeServiceUnavailable:    http.StatusServiceUnavailable,
		CodeResourceLimitExceeded: http.StatusUnprocessableEntity,
		CodeElevationRequired:     http.StatusForbidden,
		CodeInsufficientCredits:   http.StatusForbidden,
		CodeFeatureDisabled:       http.StatusForbidden,
		CodeCharitySuspended:      http.StatusForbidden,
		CodeContentTooShort:       http.StatusBadRequest,
		CodeInternal:              http.StatusInternalServerError,
	}
	for code, wantStatus := range cases {
		rec := httptest.NewRecorder()
		WriteError(rec, New(code, "x"))
		if rec.Code != wantStatus {
			t.Errorf("code %q -> status %d, want %d", code, rec.Code, wantStatus)
		}
		env := decodeEnvelope(t, rec.Body.String())
		if env.Error.Code != code {
			t.Errorf("code roundtrip %q -> %q", code, env.Error.Code)
		}
	}
}

// TestSourceAttributionDerivedAtWireSink covers the contract that every
// emitted error carries a stable source, derived at the wire sink from the
// code so a hand-constructed Error can neither omit source nor forge a
// different attribution. CodeUpstream is the only upstream-origin code; every
// other stable code (and the unknown-code fallback) is platform.
func TestSourceAttributionDerivedAtWireSink(t *testing.T) {
	upstream := []string{CodeUpstream}
	platform := []string{
		CodeInternal, CodeInvalidRequest, CodeUnauthorized, CodeForbidden,
		CodeNotFound, CodeConflict, CodeMethodNotAllowed, CodeRateLimited,
		CodePayloadTooLarge, CodeElevationRequired, CodeUnboundModel,
		CodeServiceUnavailable, CodeResourceLimitExceeded,
		CodeInsufficientCredits, CodeFeatureDisabled, CodeCharitySuspended,
		CodeContentTooShort,
	}
	for _, code := range upstream {
		rec := httptest.NewRecorder()
		WriteError(rec, New(code, "x"))
		env := decodeEnvelope(t, rec.Body.String())
		if env.Error.Source != SourceUpstream {
			t.Errorf("code %q -> source %q, want %q", code, env.Error.Source, SourceUpstream)
		}
	}
	for _, code := range platform {
		rec := httptest.NewRecorder()
		WriteError(rec, New(code, "x"))
		env := decodeEnvelope(t, rec.Body.String())
		if env.Error.Source != SourcePlatform {
			t.Errorf("code %q -> source %q, want %q", code, env.Error.Source, SourcePlatform)
		}
	}
	// An empty code (a hand-constructed Error{}) falls back to internal/platform.
	rec := httptest.NewRecorder()
	WriteError(rec, Error{})
	env := decodeEnvelope(t, rec.Body.String())
	if env.Error.Code != CodeInternal || env.Error.Source != SourcePlatform {
		t.Fatalf("empty-code fallback = code %q source %q", env.Error.Code, env.Error.Source)
	}
}

// TestSourceExplicitOverrideValidatedAtSink covers that an explicit source
// is honored only when it matches the value the wire sink derives from the
// code; any value that disagrees with the code (or is invalid) is dropped in
// favor of the code-derived default. An upstream code is always upstream and a
// platform code is always platform — an explicit source can confirm but never
// override the code-derived attribution. This is the “校验 source” half of the
// wire-sink rule.
func TestSourceExplicitOverrideValidatedAtSink(t *testing.T) {
	cases := []struct {
		code   string
		source string
		want   string
	}{
		{CodeUpstream, "", SourceUpstream},          // empty -> code-derived upstream
		{CodeUpstream, "upstream", SourceUpstream},  // explicit matches derived -> honored
		{CodeUpstream, "platform", SourceUpstream},  // explicit disagrees -> dropped, stays upstream
		{CodeUpstream, "attacker", SourceUpstream},  // invalid -> code-derived upstream
		{CodeUpstream, "UPSTREAM", SourceUpstream},  // wrong casing -> code-derived upstream
		{CodeForbidden, "", SourcePlatform},         // empty -> code-derived platform
		{CodeForbidden, "platform", SourcePlatform}, // explicit matches derived -> honored
		{CodeForbidden, "upstream", SourcePlatform}, // explicit disagrees -> dropped, stays platform
		{CodeForbidden, "attacker", SourcePlatform}, // invalid -> code-derived platform
		{"", "upstream", SourcePlatform},            // empty code coerces to internal; upstream dropped -> platform
		{"", "platform", SourcePlatform},            // empty code coerces to internal; explicit matches -> platform
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		WriteError(rec, Error{Code: tc.code, Source: tc.source, Message: "x"})
		env := decodeEnvelope(t, rec.Body.String())
		if env.Error.Source != tc.want {
			t.Errorf("code=%q source=%q -> %q, want %q", tc.code, tc.source, env.Error.Source, tc.want)
		}
	}
}

// TestWithSourceHelperRoundtrips covers the WithSource builder: it can confirm
// but never override the code-derived attribution. A platform code with an
// explicit upstream source is dropped to platform; a platform code with an
// explicit platform source (matching) is honored; an upstream code with an
// explicit platform source is dropped to upstream.
func TestWithSourceHelperRoundtrips(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, New(CodeForbidden, "no").WithSource(SourceUpstream))
	env := decodeEnvelope(t, rec.Body.String())
	if env.Error.Source != SourcePlatform {
		t.Fatalf("platform code + WithSource(upstream) = %q, want %q", env.Error.Source, SourcePlatform)
	}
	rec = httptest.NewRecorder()
	WriteError(rec, New(CodeForbidden, "no").WithSource(SourcePlatform))
	env = decodeEnvelope(t, rec.Body.String())
	if env.Error.Source != SourcePlatform {
		t.Fatalf("platform code + WithSource(platform) = %q, want %q", env.Error.Source, SourcePlatform)
	}
	rec = httptest.NewRecorder()
	WriteError(rec, New(CodeUpstream, "no").WithSource(SourcePlatform))
	env = decodeEnvelope(t, rec.Body.String())
	if env.Error.Source != SourceUpstream {
		t.Fatalf("upstream code + WithSource(platform) = %q, want %q", env.Error.Source, SourceUpstream)
	}
}

// TestSSEErrorFrameShapeAndSource covers the in-stream error frame: it must
// carry the same {error:{code,source,message}} shape as the JSON envelope,
// derive source from the code at the sink, sanitize the message, and be a
// valid SSE data frame terminated by a blank line.
func TestSSEErrorFrameShapeAndSource(t *testing.T) {
	frame := SSEErrorFrame(New(CodeUpstream, "upstream stream failed"))
	s := string(frame)
	if !strings.HasPrefix(s, "data: ") || !strings.HasSuffix(s, "\n\n") {
		t.Fatalf("frame not a complete SSE data frame: %q", s)
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(s, "data: "), "\n\n")
	var body sseErrorPayload
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		t.Fatalf("frame payload not JSON: %v; %q", err, payload)
	}
	if body.Error.Code != CodeUpstream || body.Error.Source != SourceUpstream || body.Error.Message != "upstream stream failed" {
		t.Fatalf("frame body = %+v", body.Error)
	}
	// The frame carries exactly code/source/message — no diag/request_id/etc.
	for _, forbidden := range []string{"diag", "request_id", "limit", "resource", "type"} {
		if strings.Contains(payload, "\""+forbidden+"\"") {
			t.Fatalf("frame leaked %q field: %s", forbidden, payload)
		}
	}
	// A platform code derives platform source even in an SSE frame.
	platformFrame := SSEErrorFrame(New(CodeServiceUnavailable, "service unavailable"))
	pp := strings.TrimSuffix(strings.TrimPrefix(string(platformFrame), "data: "), "\n\n")
	var pb sseErrorPayload
	if err := json.Unmarshal([]byte(pp), &pb); err != nil {
		t.Fatalf("platform frame not JSON: %v", err)
	}
	if pb.Error.Code != CodeServiceUnavailable || pb.Error.Source != SourcePlatform {
		t.Fatalf("platform frame body = %+v", pb.Error)
	}
	// An empty code coerces to internal/platform and the message is sanitized.
	dirty := SSEErrorFrame(Error{Message: "a\x00b"})
	dp := strings.TrimSuffix(strings.TrimPrefix(string(dirty), "data: "), "\n\n")
	var db sseErrorPayload
	if err := json.Unmarshal([]byte(dp), &db); err != nil {
		t.Fatalf("empty-code frame not JSON: %v", err)
	}
	if db.Error.Code != CodeInternal || db.Error.Source != SourcePlatform || db.Error.Message != "ab" {
		t.Fatalf("empty-code frame body = %+v", db.Error)
	}
	// An invalid explicit source is dropped at the sink.
	forged := SSEErrorFrame(Error{Code: CodeForbidden, Source: "attacker", Message: "x"})
	fp := strings.TrimSuffix(strings.TrimPrefix(string(forged), "data: "), "\n\n")
	var fb sseErrorPayload
	if err := json.Unmarshal([]byte(fp), &fb); err != nil {
		t.Fatalf("forged frame not JSON: %v", err)
	}
	if fb.Error.Source != SourcePlatform {
		t.Fatalf("forged source = %q, want %q", fb.Error.Source, SourcePlatform)
	}
	// An explicit source that disagrees with the code is dropped in the SSE
	// frame too, exactly as in WriteError: a platform code stays platform even
	// when an explicit upstream source is set, and an upstream code stays
	// upstream even when an explicit platform source is set.
	for _, tc := range []struct {
		code, source, want string
	}{
		{CodeForbidden, SourceUpstream, SourcePlatform},
		{CodeUpstream, SourcePlatform, SourceUpstream},
	} {
		frame := SSEErrorFrame(Error{Code: tc.code, Source: tc.source, Message: "x"})
		p := strings.TrimSuffix(strings.TrimPrefix(string(frame), "data: "), "\n\n")
		var body sseErrorPayload
		if err := json.Unmarshal([]byte(p), &body); err != nil {
			t.Fatalf("disagreeing-source frame not JSON: %v", err)
		}
		if body.Error.Source != tc.want {
			t.Fatalf("code=%q source=%q -> frame source %q, want %q", tc.code, tc.source, body.Error.Source, tc.want)
		}
	}
}

func TestMessageAndDiagAreBounded(t *testing.T) {
	long := strings.Repeat("あ", 5000) // 3-byte runes: message uses a rune bound, diag a byte bound
	rec := httptest.NewRecorder()
	WriteError(rec, New(CodeInternal, long).WithDiag(long))
	env := decodeEnvelope(t, rec.Body.String())
	// message keeps its human-safe rune limit.
	if got := []rune(env.Error.Message); len(got) > msgBound {
		t.Fatalf("message rune count = %d, want <= %d", len(got), msgBound)
	}
	// diag is bounded to diagnostic.MaxBytes BYTES (not runes), always valid
	// UTF-8, and marked when truncated.
	if got := len(env.Error.Diag); got > diagnostic.MaxBytes {
		t.Fatalf("diag byte length = %d, want <= %d", got, diagnostic.MaxBytes)
	}
	if !utf8.ValidString(env.Error.Diag) {
		t.Fatalf("diag is not valid UTF-8: %q", env.Error.Diag)
	}
	if !strings.HasSuffix(env.Error.Diag, diagnostic.TruncationMarker) {
		t.Fatalf("truncated diag missing marker: %q", env.Error.Diag[max(0, len(env.Error.Diag)-len(diagnostic.TruncationMarker)):])
	}
}

func TestControlCharsStripped(t *testing.T) {
	dirty := "a\x00b\x01c\rd\ne\tf"
	rec := httptest.NewRecorder()
	WriteError(rec, New(CodeInvalidRequest, dirty).WithDiag(dirty))
	env := decodeEnvelope(t, rec.Body.String())
	// message: all control chars (incl CR/LF/TAB) stripped -> "abcdef".
	if strings.ContainsAny(env.Error.Message, "\x00\x01\r\n\t\x07\x1f\x7f") {
		t.Fatalf("message retained control chars: %q", env.Error.Message)
	}
	if env.Error.Message != "abcdef" {
		t.Fatalf("message = %q, want %q", env.Error.Message, "abcdef")
	}
	// diag: CR/LF/TAB -> space; other C0 and DEL stripped; no line forgery.
	if strings.ContainsAny(env.Error.Diag, "\x00\x01\r\n\t") {
		t.Fatalf("diag retained stripped/line-forgery chars: %q", env.Error.Diag)
	}
	if env.Error.Diag != "abc d e f" {
		t.Fatalf("diag = %q, want %q", env.Error.Diag, "abc d e f")
	}
}

func TestDiagByteBoundNotRuneBound(t *testing.T) {
	// 2000 CJK runes = 6000 bytes: under a 4096-rune bound but over a 4096-byte
	// bound. A byte boundary must cut this; a rune boundary would let it through.
	in := strings.Repeat("あ", 2000)
	rec := httptest.NewRecorder()
	WriteError(rec, New(CodeUpstream, "fail").WithDiag(in))
	env := decodeEnvelope(t, rec.Body.String())
	if got := len(env.Error.Diag); got > diagnostic.MaxBytes {
		t.Fatalf("diag byte length = %d, want <= %d", got, diagnostic.MaxBytes)
	}
	if rc := utf8.RuneCountInString(env.Error.Diag); rc >= 2000 {
		t.Fatalf("diag rune count = %d, want < 2000 (byte-truncated, not rune-bound)", rc)
	}
	if !utf8.ValidString(env.Error.Diag) {
		t.Fatalf("diag not valid UTF-8")
	}
}

func TestDiagNoLineForgery(t *testing.T) {
	in := "ok\n[ERROR] forged admin action\r\n"
	rec := httptest.NewRecorder()
	WriteError(rec, New(CodeUpstream, "fail").WithDiag(in))
	env := decodeEnvelope(t, rec.Body.String())
	if strings.ContainsAny(env.Error.Diag, "\r\n") {
		t.Fatalf("diag allowed line forgery: %q", env.Error.Diag)
	}
	if !utf8.ValidString(env.Error.Diag) {
		t.Fatalf("diag not valid UTF-8")
	}
}

func TestDiagTruncationMarkerAndByteBound(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, New(CodeUpstream, "fail").WithDiag(strings.Repeat("x", diagnostic.MaxBytes+100)))
	env := decodeEnvelope(t, rec.Body.String())
	if len(env.Error.Diag) > diagnostic.MaxBytes {
		t.Fatalf("diag byte length = %d, want <= %d", len(env.Error.Diag), diagnostic.MaxBytes)
	}
	if !strings.HasSuffix(env.Error.Diag, diagnostic.TruncationMarker) {
		t.Fatalf("truncated diag missing marker")
	}
	if !utf8.ValidString(env.Error.Diag) {
		t.Fatalf("diag not valid UTF-8")
	}
}

func TestDiagOmittedWhenEmpty(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, New(CodeInvalidRequest, "bad").WithDiag(""))
	body := rec.Body.String()
	if strings.Contains(body, `"diag"`) {
		t.Fatalf("empty diag should be omitted by omitempty: %s", body)
	}
	env := decodeEnvelope(t, body)
	if env.Error.Diag != "" {
		t.Fatalf("diag = %q, want empty", env.Error.Diag)
	}
}

func TestDiagInvalidUTF8RepairedInEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, New(CodeUpstream, "fail").WithDiag(string([]byte{'b', 'a', 'd', 0xff, 0xfe, 'e', 'n', 'd'})))
	env := decodeEnvelope(t, rec.Body.String())
	if !utf8.ValidString(env.Error.Diag) {
		t.Fatalf("diag not valid UTF-8: %q", env.Error.Diag)
	}
	// The invalid run collapses to one replacement rune; surrounding text kept.
	if !strings.Contains(env.Error.Diag, "bad") || !strings.Contains(env.Error.Diag, "end") {
		t.Fatalf("diag lost surrounding text: %q", env.Error.Diag)
	}
}

func TestWriteErrorReappliesWireSanitizers(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, Error{
		Code:      CodeUpstream,
		Message:   "raw\r\nmessage",
		Diag:      "raw\r\n\t\x00diag" + strings.Repeat("x", diagnostic.MaxBytes),
		RequestID: "request\r\nid" + strings.Repeat("z", requestIDBound),
	})
	env := decodeEnvelope(t, rec.Body.String())
	if env.Error.Message != "rawmessage" {
		t.Fatalf("message = %q, want sanitized direct Error message", env.Error.Message)
	}
	if strings.ContainsAny(env.Error.Diag, "\r\n\t\x00") || len(env.Error.Diag) > diagnostic.MaxBytes || !utf8.ValidString(env.Error.Diag) {
		t.Fatalf("diag was not sanitized at wire sink: len=%d value=%q", len(env.Error.Diag), env.Error.Diag)
	}
	if strings.ContainsAny(env.Error.RequestID, "\r\n") || len([]rune(env.Error.RequestID)) > requestIDBound {
		t.Fatalf("request id was not bounded/sanitized: %q", env.Error.RequestID)
	}

	withID := New(CodeInternal, "failed").WithRequestID("id\r\n")
	if strings.ContainsAny(withID.RequestID, "\r\n") {
		t.Fatalf("WithRequestID retained controls: %q", withID.RequestID)
	}
}

func TestEmptyCodeFallsBackToInternal(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, Error{})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for empty code", rec.Code)
	}
	env := decodeEnvelope(t, rec.Body.String())
	if env.Error.Code != CodeInternal {
		t.Fatalf("code = %q, want %q", env.Error.Code, CodeInternal)
	}
}

func TestResourceLimitExceededEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, New(CodeResourceLimitExceeded, "resource limit reached").
		WithResourceLimit("endpoint_key", 20))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	env := decodeEnvelope(t, rec.Body.String())
	if env.Error.Code != CodeResourceLimitExceeded {
		t.Fatalf("code = %q, want %q", env.Error.Code, CodeResourceLimitExceeded)
	}
	if env.Error.Limit != 20 {
		t.Fatalf("limit = %d, want 20", env.Error.Limit)
	}
	if env.Error.Resource != "endpoint_key" {
		t.Fatalf("resource = %q, want endpoint_key", env.Error.Resource)
	}

	// limit/resource are omitted when unset (a non-resource-limit error).
	rec2 := httptest.NewRecorder()
	WriteError(rec2, New(CodeInvalidRequest, "bad"))
	body := rec2.Body.String()
	if strings.Contains(body, `"limit"`) || strings.Contains(body, `"resource"`) {
		t.Fatalf("non-resource-limit error leaked limit/resource: %s", body)
	}

	// resource is sanitized at the wire sink (defense-in-depth: a caller that
	// constructs Error directly cannot push control characters to the wire).
	rec3 := httptest.NewRecorder()
	WriteError(rec3, Error{Code: CodeResourceLimitExceeded, Resource: "bad\x00resource\n"})
	if env := decodeEnvelope(t, rec3.Body.String()); env.Error.Resource != "badresource" {
		t.Fatalf("resource not sanitized: %q", env.Error.Resource)
	}
}

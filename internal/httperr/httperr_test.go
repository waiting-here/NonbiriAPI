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
	if env.Error.Message != "[NonbiriAPI] model field is required" {
		t.Fatalf("message = %q", env.Error.Message)
	}
	if env.Error.Diag != "" {
		t.Fatalf("platform diag = %q, want empty", env.Error.Diag)
	}
	// Platform errors expose only the exact common fields.
	for _, forbidden := range []string{`"upstream_code"`, `"diag"`, `"request_id"`, `"limit"`, `"resource"`} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Fatalf("platform envelope leaked %s: %s", forbidden, rec.Body.String())
		}
	}
	// source is always present on the wire, even when the caller did not set it.
	if !strings.Contains(rec.Body.String(), `"source":"platform"`) {
		t.Fatalf("envelope missing source: %s", rec.Body.String())
	}
}

func TestStatusMapping(t *testing.T) {
	cases := map[string]int{
		CodeInvalidRequest:          http.StatusBadRequest,
		CodeContentTooShort:         http.StatusBadRequest,
		CodeUnauthorized:            http.StatusUnauthorized,
		CodeForbidden:               http.StatusForbidden,
		CodeElevationRequired:       http.StatusForbidden,
		CodeFeatureDisabled:         http.StatusForbidden,
		CodeInsufficientCredits:     http.StatusForbidden,
		CodeCharitySuspended:        http.StatusForbidden,
		CodeCheckinCapReached:       http.StatusForbidden,
		CodeNotFound:                http.StatusNotFound,
		CodeConflict:                http.StatusConflict,
		CodeAlreadyCheckedIn:        http.StatusConflict,
		CodeDebugLiveCancelled:      http.StatusConflict,
		CodeMethodNotAllowed:        http.StatusMethodNotAllowed,
		CodeRateLimited:             http.StatusTooManyRequests,
		CodePayloadTooLarge:         http.StatusRequestEntityTooLarge,
		CodeUnboundModel:            http.StatusServiceUnavailable,
		CodeMaintenance:             http.StatusServiceUnavailable,
		CodeUpstream:                http.StatusBadGateway,
		CodeServiceUnavailable:      http.StatusServiceUnavailable,
		CodeResourceLimitExceeded:   http.StatusUnprocessableEntity,
		CodeDebugDryRunIntercepted:  http.StatusUnprocessableEntity,
		CodeDebugLiveResultCaptured: http.StatusUnprocessableEntity,
		CodeResourceLocked:          http.StatusLocked,
		CodeInternal:                http.StatusInternalServerError,
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

func TestUpstreamErrorStatusOverride(t *testing.T) {
	cases := []struct {
		name       string
		code       string
		override   int
		wantStatus int
		wantCode   string
	}{
		{name: "lower 4xx", code: CodeUpstream, override: http.StatusBadRequest, wantStatus: http.StatusBadRequest, wantCode: CodeUpstream},
		{name: "upper 4xx", code: CodeUpstream, override: 499, wantStatus: 499, wantCode: CodeUpstream},
		{name: "bad gateway", code: CodeUpstream, override: http.StatusBadGateway, wantStatus: http.StatusBadGateway, wantCode: CodeUpstream},
		{name: "gateway timeout", code: CodeUpstream, override: http.StatusGatewayTimeout, wantStatus: http.StatusGatewayTimeout, wantCode: CodeUpstream},
		// A platform error cannot borrow an upstream status. It keeps its
		// code-derived status, even when the requested value is otherwise legal.
		{name: "platform code", code: CodeForbidden, override: http.StatusTeapot, wantStatus: http.StatusForbidden, wantCode: CodeForbidden},
		// 5xx statuses other than 502/504 are not legal overrides, so the
		// upstream code falls back to its ordinary 502 status.
		{name: "upstream 500", code: CodeUpstream, override: http.StatusInternalServerError, wantStatus: http.StatusBadGateway, wantCode: CodeUpstream},
		{name: "upstream 501", code: CodeUpstream, override: http.StatusNotImplemented, wantStatus: http.StatusBadGateway, wantCode: CodeUpstream},
		{name: "upstream success", code: CodeUpstream, override: http.StatusOK, wantStatus: http.StatusBadGateway, wantCode: CodeUpstream},
		{name: "upstream zero", code: CodeUpstream, override: 0, wantStatus: http.StatusBadGateway, wantCode: CodeUpstream},
		{name: "unknown code", code: "future_code", override: http.StatusTeapot, wantStatus: http.StatusInternalServerError, wantCode: CodeInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteUpstreamError(rec, Error{
				Code:    tc.code,
				Message: "upstream [NonbiriAPI] status detail",
				Diag:    "safe diagnostic",
			}, tc.override)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			env := decodeEnvelope(t, rec.Body.String())
			if env.Error.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", env.Error.Code, tc.wantCode)
			}
			wantSource := SourcePlatform
			if tc.wantCode == CodeUpstream {
				wantSource = SourceUpstream
			}
			if env.Error.Source != wantSource {
				t.Fatalf("source = %q, want %q", env.Error.Source, wantSource)
			}
			if tc.wantCode == CodeUpstream {
				if strings.Contains(env.Error.Message, platformPrefix) {
					t.Fatalf("upstream wire message retained platform marker: %q", env.Error.Message)
				}
			} else if strings.Count(env.Error.Message, platformPrefix) != 1 {
				t.Fatalf("platform wire message marker count = %d, value=%q", strings.Count(env.Error.Message, platformPrefix), env.Error.Message)
			}
		})
	}

	// The ordinary sink has no override path: CodeUpstream remains 502.
	rec := httptest.NewRecorder()
	WriteError(rec, New(CodeUpstream, "upstream timeout"))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("WriteError upstream status = %d, want 502", rec.Code)
	}
}

func TestStableCodeClosedSet(t *testing.T) {
	codes := []string{
		CodeInvalidRequest, CodeContentTooShort, CodeUnauthorized, CodeForbidden,
		CodeElevationRequired, CodeFeatureDisabled, CodeInsufficientCredits,
		CodeCharitySuspended, CodeCheckinCapReached, CodeNotFound,
		CodeMethodNotAllowed, CodeConflict, CodeAlreadyCheckedIn,
		CodePayloadTooLarge, CodeResourceLimitExceeded, CodeResourceLocked,
		CodeRateLimited, CodeUpstream, CodeMaintenance, CodeServiceUnavailable,
		CodeUnboundModel, CodeInternal, CodeDebugDryRunIntercepted,
		CodeDebugLiveResultCaptured, CodeDebugLiveCancelled,
	}
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		if _, duplicate := seen[code]; duplicate {
			t.Fatalf("stable code listed twice: %q", code)
		}
		seen[code] = struct{}{}
		if !IsStableCode(code) {
			t.Errorf("frozen stable code rejected: %q", code)
		}
	}
	for _, alias := range []string{"", "unavailable", "too_large", "service-unavailable", "INTERNAL", "future_code"} {
		if IsStableCode(alias) {
			t.Errorf("non-canonical error code accepted: %q", alias)
		}
	}
}

func TestMessagePrefixAndByteBound(t *testing.T) {
	cases := []struct {
		name   string
		error  Error
		prefix bool
	}{
		{
			name:   "platform",
			error:  New(CodeConflict, "resource revision changed"),
			prefix: true,
		},
		{
			name:   "platform already prefixed",
			error:  Error{Code: CodeConflict, Message: platformPrefix + platformPrefix + "resource revision changed"},
			prefix: true,
		},
		{
			name:   "platform marker in message",
			error:  Error{Code: CodeConflict, Message: "before " + platformPrefix + "middle " + platformPrefix + "after"},
			prefix: true,
		},
		{
			name:   "platform leading markers",
			error:  Error{Code: CodeConflict, Message: strings.Repeat(platformPrefix, 3) + "resource revision changed"},
			prefix: true,
		},
		{
			name:   "upstream",
			error:  Error{Code: CodeUpstream, Message: "before " + platformPrefix + "upstream request " + platformPrefix + "failed"},
			prefix: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteError(rec, tc.error)
			env := decodeEnvelope(t, rec.Body.String())
			if !utf8.ValidString(env.Error.Message) || len(env.Error.Message) > msgBound {
				t.Fatalf("message is not a bounded valid UTF-8 string: bytes=%d value=%q", len(env.Error.Message), env.Error.Message)
			}
			if tc.prefix {
				if !strings.HasPrefix(env.Error.Message, platformPrefix) || strings.Count(env.Error.Message, platformPrefix) != 1 {
					t.Fatalf("platform message prefix count = %d, value=%q", strings.Count(env.Error.Message, platformPrefix), env.Error.Message)
				}
				if tc.name == "platform marker in message" && env.Error.Message != platformPrefix+"before middle after" {
					t.Fatalf("internal markers were not removed: %q", env.Error.Message)
				}
				if tc.name == "platform leading markers" && env.Error.Message != platformPrefix+"resource revision changed" {
					t.Fatalf("leading markers were not collapsed: %q", env.Error.Message)
				}
			} else if strings.Contains(env.Error.Message, platformPrefix) {
				t.Fatalf("upstream message retained platform prefix: %q", env.Error.Message)
			}
		})
	}

	// The prefix consumes part of the byte budget, and truncation must not cut
	// a UTF-8 sequence or move the prefix outside the bound.
	rec := httptest.NewRecorder()
	WriteError(rec, New(CodeInternal, strings.Repeat("界", 1000)))
	env := decodeEnvelope(t, rec.Body.String())
	if len(env.Error.Message) != msgBound || !utf8.ValidString(env.Error.Message) {
		t.Fatalf("platform message byte bound = %d, valid=%v; want %d", len(env.Error.Message), utf8.ValidString(env.Error.Message), msgBound)
	}
	if !strings.HasPrefix(env.Error.Message, platformPrefix) || strings.Count(env.Error.Message, platformPrefix) != 1 {
		t.Fatalf("long platform prefix count = %d, value=%q", strings.Count(env.Error.Message, platformPrefix), env.Error.Message[:min(len(env.Error.Message), 40)])
	}

	rec = httptest.NewRecorder()
	WriteError(rec, Error{Code: CodeUpstream, Message: strings.Repeat("界", 1000)})
	env = decodeEnvelope(t, rec.Body.String())
	if len(env.Error.Message) > msgBound || !utf8.ValidString(env.Error.Message) || strings.Contains(env.Error.Message, platformPrefix) {
		t.Fatalf("long upstream message was not bounded/sanitized: bytes=%d value-prefix=%q", len(env.Error.Message), env.Error.Message[:min(len(env.Error.Message), 20)])
	}

	// Invalid bytes are repaired before marker removal and byte truncation; the
	// final message remains valid UTF-8 and has exactly one platform marker.
	invalid := Error{
		Code:    CodeInternal,
		Message: "界" + platformPrefix + string([]byte{0xff, 0xfe}) + strings.Repeat("界", 500),
	}
	rec = httptest.NewRecorder()
	WriteError(rec, invalid)
	env = decodeEnvelope(t, rec.Body.String())
	if !utf8.ValidString(env.Error.Message) || len(env.Error.Message) > msgBound {
		t.Fatalf("invalid UTF-8 message was not repaired/bounded: bytes=%d value=%q", len(env.Error.Message), env.Error.Message[:min(len(env.Error.Message), 40)])
	}
	if strings.Count(env.Error.Message, platformPrefix) != 1 || strings.Contains(env.Error.Message, platformPrefix+platformPrefix) {
		t.Fatalf("invalid UTF-8 message marker handling = %q", env.Error.Message[:min(len(env.Error.Message), 40)])
	}
}

func TestUpstreamCodeAndSSEWireConsistency(t *testing.T) {
	input := Error{
		Code:         CodeConflict,
		Source:       SourcePlatform,
		Message:      platformPrefix + platformPrefix + "resource revision changed",
		UpstreamCode: "rate_limit_exceeded",
		Diag:         "connector-safe diagnostic",
	}
	rec := httptest.NewRecorder()
	WriteError(rec, input)
	env := decodeEnvelope(t, rec.Body.String())
	if env.Error.UpstreamCode != "" || env.Error.Diag != "" {
		t.Fatalf("platform error exposed upstream context: %+v", env.Error)
	}
	if strings.Count(env.Error.Message, platformPrefix) != 1 {
		t.Fatalf("JSON platform prefix count = %d, message=%q", strings.Count(env.Error.Message, platformPrefix), env.Error.Message)
	}

	frame := string(SSEErrorFrame(input))
	if len(frame) > 4096 {
		t.Fatalf("SSE error frame bytes = %d, want <= 4096", len(frame))
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(frame, "data: "), "\n\n")
	var body sseErrorPayload
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		t.Fatalf("SSE payload not JSON: %v", err)
	}
	if body.Error.Code != env.Error.Code || body.Error.Source != env.Error.Source || body.Error.Message != env.Error.Message || body.Error.UpstreamCode != env.Error.UpstreamCode {
		t.Fatalf("JSON/SSE error mismatch: json=%+v sse=%+v", env.Error, body.Error)
	}
	if strings.Contains(payload, "diag") {
		t.Fatalf("SSE frame leaked diag: %s", payload)
	}

	upstreamInput := Error{
		Code:         CodeUpstream,
		Source:       SourceUpstream,
		Message:      "upstream request failed",
		UpstreamCode: input.UpstreamCode,
		Diag:         input.Diag,
	}
	upstreamRec := httptest.NewRecorder()
	WriteUpstreamError(upstreamRec, upstreamInput, http.StatusBadGateway)
	upstreamEnv := decodeEnvelope(t, upstreamRec.Body.String())
	if upstreamEnv.Error.UpstreamCode != input.UpstreamCode || upstreamEnv.Error.Diag != input.Diag {
		t.Fatalf("real upstream context was not retained: %+v", upstreamEnv.Error)
	}

	defaultUpstreamFrame := string(SSEErrorFrame(upstreamInput))
	defaultPayload := strings.TrimSuffix(strings.TrimPrefix(defaultUpstreamFrame, "data: "), "\n\n")
	var defaultBody sseErrorPayload
	if err := json.Unmarshal([]byte(defaultPayload), &defaultBody); err != nil {
		t.Fatalf("default upstream SSE payload not JSON: %v", err)
	}
	if defaultBody.Error.UpstreamCode != "" {
		t.Fatalf("default SSE exposed upstream_code: %s", defaultPayload)
	}
	explicitFrame := string(SSEUpstreamErrorFrame(upstreamInput))
	explicitPayload := strings.TrimSuffix(strings.TrimPrefix(explicitFrame, "data: "), "\n\n")
	var explicitBody sseErrorPayload
	if err := json.Unmarshal([]byte(explicitPayload), &explicitBody); err != nil {
		t.Fatalf("explicit upstream SSE payload not JSON: %v", err)
	}
	if explicitBody.Error.UpstreamCode != input.UpstreamCode {
		t.Fatalf("explicit upstream SSE did not retain upstream_code: %q", explicitBody.Error.UpstreamCode)
	}

	// JSON's HTML escaping can expand ampersands; the SSE sink must still
	// enforce its independent 4 KiB terminal-frame budget.
	largeFrame := SSEErrorFrame(New(CodeInternal, strings.Repeat("&", msgBound)))
	if len(largeFrame) > sseErrorFrameBound {
		t.Fatalf("expanded SSE error frame bytes = %d, want <= %d", len(largeFrame), sseErrorFrameBound)
	}
	largePayload := strings.TrimSuffix(strings.TrimPrefix(string(largeFrame), "data: "), "\n\n")
	var largeBody sseErrorPayload
	if err := json.Unmarshal([]byte(largePayload), &largeBody); err != nil {
		t.Fatalf("expanded SSE payload not JSON: %v", err)
	}
	if !strings.HasPrefix(largeBody.Error.Message, platformPrefix) || strings.Count(largeBody.Error.Message, platformPrefix) != 1 {
		t.Fatalf("expanded platform prefix count = %d, message=%q", strings.Count(largeBody.Error.Message, platformPrefix), largeBody.Error.Message[:min(len(largeBody.Error.Message), 40)])
	}

	for _, hostile := range []string{"bad\x00code", "bad\ncode", strings.Repeat("x", upstreamCodeBound+1)} {
		rec := httptest.NewRecorder()
		WriteError(rec, New(CodeUpstream, "failed").WithUpstreamCode(hostile))
		env := decodeEnvelope(t, rec.Body.String())
		if env.Error.UpstreamCode != "" || strings.Contains(rec.Body.String(), `"upstream_code"`) {
			t.Fatalf("hostile upstream_code was not rejected: %q", env.Error.UpstreamCode)
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
		CodeContentTooShort, CodeAlreadyCheckedIn, CodeCheckinCapReached,
		CodeResourceLocked, CodeDebugDryRunIntercepted,
		CodeDebugLiveResultCaptured, CodeDebugLiveCancelled,
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
	// The frame carries only the safe error projection — no diag/request_id/etc.
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
	if db.Error.Code != CodeInternal || db.Error.Source != SourcePlatform || db.Error.Message != "[NonbiriAPI] ab" {
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
	WriteUpstreamError(rec, New(CodeUpstream, long).WithDiag(long), http.StatusBadGateway)
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
	dirty := "a\x00b\x01c\rd\ne\t\u0085\u009ff"
	rec := httptest.NewRecorder()
	WriteUpstreamError(rec, New(CodeUpstream, dirty).WithDiag(dirty), http.StatusBadGateway)
	env := decodeEnvelope(t, rec.Body.String())
	// message: all control chars (incl C0/C1, CR/LF/TAB) stripped -> "abcdef".
	if strings.ContainsAny(env.Error.Message, "\x00\x01\r\n\t\x07\x1f\x7f\u0085\u009f") {
		t.Fatalf("message retained control chars: %q", env.Error.Message)
	}
	if env.Error.Message != "abcdef" {
		t.Fatalf("message = %q, want %q", env.Error.Message, "abcdef")
	}
	// diag: CR/LF/TAB -> space; other C0/C1 and DEL stripped; no line forgery.
	if strings.ContainsAny(env.Error.Diag, "\x00\x01\r\n\t\u0085\u009f") {
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
	WriteUpstreamError(rec, New(CodeUpstream, "fail").WithDiag(in), http.StatusBadGateway)
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
	WriteUpstreamError(rec, New(CodeUpstream, "fail").WithDiag(in), http.StatusBadGateway)
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
	WriteUpstreamError(rec, New(CodeUpstream, "fail").WithDiag(strings.Repeat("x", diagnostic.MaxBytes+100)), http.StatusBadGateway)
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
	WriteUpstreamError(rec, New(CodeUpstream, "fail").WithDiag(string([]byte{'b', 'a', 'd', 0xff, 0xfe, 'e', 'n', 'd'})), http.StatusBadGateway)
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
		Code:         CodeUpstream,
		Message:      "raw\r\nmessage",
		UpstreamCode: "upstream\r\ncode",
		Diag:         "raw\r\n\t\x00diag" + strings.Repeat("x", diagnostic.MaxBytes),
	})
	env := decodeEnvelope(t, rec.Body.String())
	if env.Error.Message != "rawmessage" {
		t.Fatalf("message = %q, want sanitized direct Error message", env.Error.Message)
	}
	if env.Error.Diag != "" || env.Error.UpstreamCode != "" {
		t.Fatalf("default upstream sink exposed connector context: %+v", env.Error)
	}

	explicitRec := httptest.NewRecorder()
	WriteUpstreamError(explicitRec, Error{
		Code:         CodeUpstream,
		Message:      "raw\r\nmessage",
		UpstreamCode: "safe-code",
		Diag:         "raw\r\n\t\x00diag" + strings.Repeat("x", diagnostic.MaxBytes),
	}, http.StatusBadGateway)
	explicitEnv := decodeEnvelope(t, explicitRec.Body.String())
	if strings.ContainsAny(explicitEnv.Error.Diag, "\r\n\t\x00") || len(explicitEnv.Error.Diag) > diagnostic.MaxBytes || !utf8.ValidString(explicitEnv.Error.Diag) {
		t.Fatalf("explicit upstream diag was not sanitized: len=%d value=%q", len(explicitEnv.Error.Diag), explicitEnv.Error.Diag)
	}
	if explicitEnv.Error.UpstreamCode != "safe-code" {
		t.Fatalf("explicit upstream_code = %q, want safe-code", explicitEnv.Error.UpstreamCode)
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

func TestExactEnvelopeShape(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, Error{
		Code:         CodeResourceLimitExceeded,
		Message:      "resource limit reached",
		UpstreamCode: "should-not-leak",
		Diag:         "should-not-leak",
	})

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	env := decodeEnvelope(t, rec.Body.String())
	if env.Error.Code != CodeResourceLimitExceeded {
		t.Fatalf("code = %q, want %q", env.Error.Code, CodeResourceLimitExceeded)
	}
	if env.Error.UpstreamCode != "" || env.Error.Diag != "" {
		t.Fatalf("platform context leaked: %+v", env.Error)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw envelope: %v", err)
	}
	rawError, ok := raw["error"]
	if !ok {
		t.Fatalf("missing error object: %s", rec.Body.String())
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rawError, &fields); err != nil {
		t.Fatalf("decode raw error: %v", err)
	}
	for _, field := range []string{"code", "source", "message"} {
		if _, ok := fields[field]; !ok {
			t.Fatalf("missing required field %q: %s", field, rec.Body.String())
		}
	}
	for _, field := range []string{"upstream_code", "diag", "request_id", "limit", "resource"} {
		if _, ok := fields[field]; ok {
			t.Fatalf("unexpected field %q in platform envelope: %s", field, rec.Body.String())
		}
	}
}

func TestNonUpstreamErrorsDropConnectorFields(t *testing.T) {
	for _, code := range []string{
		CodeRateLimited,
		CodeCharitySuspended,
		CodeFeatureDisabled,
		CodeDebugDryRunIntercepted,
		CodeDebugLiveResultCaptured,
		CodeDebugLiveCancelled,
		CodeResourceLimitExceeded,
	} {
		t.Run(code, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteError(rec, Error{
				Code:         code,
				Message:      "failure",
				UpstreamCode: "connector-code",
				Diag:         "connector diagnostic",
			})
			env := decodeEnvelope(t, rec.Body.String())
			if env.Error.Source != SourcePlatform {
				t.Fatalf("source = %q, want %q", env.Error.Source, SourcePlatform)
			}
			if env.Error.UpstreamCode != "" || env.Error.Diag != "" {
				t.Fatalf("non-upstream error exposed connector fields: %+v", env.Error)
			}
			for _, field := range []string{"upstream_code", "diag"} {
				if strings.Contains(rec.Body.String(), `"`+field+`"`) {
					t.Fatalf("non-upstream error emitted %q: %s", field, rec.Body.String())
				}
			}
		})
	}
}

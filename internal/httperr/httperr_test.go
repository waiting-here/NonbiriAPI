package httperr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"nonbiriapi/internal/diagnostic"
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
}

func TestStatusMapping(t *testing.T) {
	cases := map[string]int{
		CodeInvalidRequest:     http.StatusBadRequest,
		CodeUnauthorized:       http.StatusUnauthorized,
		CodeForbidden:          http.StatusForbidden,
		CodeNotFound:           http.StatusNotFound,
		CodeConflict:           http.StatusConflict,
		CodeMethodNotAllowed:   http.StatusMethodNotAllowed,
		CodeRateLimited:        http.StatusTooManyRequests,
		CodePayloadTooLarge:    http.StatusRequestEntityTooLarge,
		CodeUnboundModel:       http.StatusServiceUnavailable,
		CodeUpstream:           http.StatusBadGateway,
		CodeServiceUnavailable: http.StatusServiceUnavailable,
		CodeInternal:           http.StatusInternalServerError,
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

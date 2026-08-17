package httperr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
		CodeInvalidRequest:    http.StatusBadRequest,
		CodeUnauthorized:      http.StatusUnauthorized,
		CodeForbidden:         http.StatusForbidden,
		CodeNotFound:          http.StatusNotFound,
		CodeConflict:          http.StatusConflict,
		CodeMethodNotAllowed:  http.StatusMethodNotAllowed,
		CodeRateLimited:       http.StatusTooManyRequests,
		CodePayloadTooLarge:   http.StatusRequestEntityTooLarge,
		CodeUnboundModel:      http.StatusServiceUnavailable,
		CodeUpstream:          http.StatusBadGateway,
		CodeServiceUnavailable: http.StatusServiceUnavailable,
		CodeInternal:         http.StatusInternalServerError,
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
	long := strings.Repeat("あ", 5000) // multibyte runes; ensures rune-based bound
	rec := httptest.NewRecorder()
	WriteError(rec, New(CodeInternal, long).WithDiag(long))
	env := decodeEnvelope(t, rec.Body.String())
	if got := []rune(env.Error.Message); len(got) > msgBound {
		t.Fatalf("message rune count = %d, want <= %d", len(got), msgBound)
	}
	if got := []rune(env.Error.Diag); len(got) > diagBound {
		t.Fatalf("diag rune count = %d, want <= %d", len(got), diagBound)
	}
}

func TestControlCharsStripped(t *testing.T) {
	dirty := "a\x00b\x01c\rd\ne\tf"
	rec := httptest.NewRecorder()
	WriteError(rec, New(CodeInvalidRequest, dirty).WithDiag(dirty))
	env := decodeEnvelope(t, rec.Body.String())
	// message: all control chars (incl CR/LF/TAB) stripped
	if strings.ContainsAny(env.Error.Message, "\x00\x01\r\n\t") {
		t.Fatalf("message retained control chars: %q", env.Error.Message)
	}
	// diag: CR -> space; TAB and LF kept; NUL/0x01 stripped
	if strings.ContainsAny(env.Error.Diag, "\x00\x01") {
		t.Fatalf("diag retained stripped control chars: %q", env.Error.Diag)
	}
	if !strings.Contains(env.Error.Diag, "c d") { // "c\rd" -> "c d"
		t.Fatalf("diag did not normalize CR to space: %q", env.Error.Diag)
	}
	if !strings.Contains(env.Error.Diag, "\n") || !strings.Contains(env.Error.Diag, "\t") {
		t.Fatalf("diag dropped permitted LF/TAB: %q", env.Error.Diag)
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
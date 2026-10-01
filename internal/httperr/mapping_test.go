package httperr

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMappedErrorPreservesOrderStatusAndSafeSink(t *testing.T) {
	invalid := errors.New("private invalid detail")
	forbidden := errors.New("private forbidden detail")
	for _, test := range []struct {
		err         error
		wantCode    string
		wantStatus  int
		wantMessage string
	}{
		{fmt.Errorf("private wrapper: %w", invalid), "invalid_request", 400, "[NonbiriAPI] invalid request"},
		{forbidden, "forbidden", 403, "[NonbiriAPI] access denied"},
		{errors.Join(invalid, forbidden), "invalid_request", 400, "[NonbiriAPI] invalid request"},
		{errors.New("private unrecognized"), "internal", 500, "[NonbiriAPI] internal error"},
	} {
		w := httptest.NewRecorder()
		WriteMapped(w, test.err, Mapping{Err: invalid, Code: CodeInvalidRequest, Message: "invalid request"}, Mapping{Err: forbidden, Code: CodeForbidden, Message: "access denied"})
		var got Envelope
		if json.Unmarshal(w.Body.Bytes(), &got) != nil || w.Code != test.wantStatus || got.Error.Code != test.wantCode || got.Error.Message != test.wantMessage || got.Error.Source != "platform" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("mapped error=%d %s", w.Code, w.Body)
		}
	}
	w := httptest.NewRecorder()
	WriteMapped(w, invalid, Mapping{Err: invalid, Code: "invalid-code", Message: "bad\nmessage"})
	if w.Code != http.StatusInternalServerError {
		t.Fatal("mapping bypassed stable-code sink")
	}
}

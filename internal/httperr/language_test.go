package httperr

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPreferredErrorLanguage(t *testing.T) {
	for _, tc := range []struct{ header, want string }{
		{"", "en"}, {"zh-CN", "zh"}, {"ZH-TW,en;q=0.5", "zh"},
		{"fr, zh;q=0.6, en;q=0.9", "en"}, {"zh;q=0,en;q=0.1", "en"},
		{"zh;q=invalid,en", "en"}, {"en;q=0.5,zh;q=0.5", "en"},
		{"fr,zh-Hans;q=0.8", "zh"}, {"zh;q=NaN", "en"},
	} {
		if got := preferredLanguage(tc.header); got != tc.want {
			t.Errorf("%q: %q, want %q", tc.header, got, tc.want)
		}
	}
}

type errorRecorder struct {
	http.ResponseWriter
	seen bool
	err  error
}

func (w *errorRecorder) Unwrap() http.ResponseWriter           { return w.ResponseWriter }
func (w *errorRecorder) BeforeHTTPError(_ int, _ string) error { w.seen = true; return w.err }

func TestLanguageKeepsErrorRecordingAndUpstreamMeaning(t *testing.T) {
	rec := httptest.NewRecorder()
	audit := &errorRecorder{ResponseWriter: rec, err: errors.New("record unavailable")}
	WriteError(WithLanguage(audit, "zh-CN"), New(CodeForbidden, "not allowed"))
	value := decodeEnvelope(t, rec.Body.String()).Error
	if !audit.seen || rec.Code != 503 || value.Code != CodeServiceUnavailable ||
		value.Message != "[NonbiriAPI] 服务暂时不可用，请稍后重试。" || rec.Header().Get("Content-Language") != "zh" {
		t.Fatalf("recording/localization: %+v / %d / %v", value, rec.Code, audit.seen)
	}
	rec = httptest.NewRecorder()
	WriteUpstreamError(WithLanguage(rec, "zh-CN"), New(CodeUpstream, "provider refused 192.0.2.1").WithDiag("peer 198.51.100.8"), 429)
	value = decodeEnvelope(t, rec.Body.String()).Error
	if rec.Code != 429 || value.Source != SourceUpstream || value.Message != "provider refused [redacted]" || value.Diag != "peer [redacted]" {
		t.Fatalf("upstream meaning/privacy changed: %+v / %d", value, rec.Code)
	}
}

func TestLanguageRemainsRequestScopedThroughWrappersAndStreams(t *testing.T) {
	for _, lang := range []string{"zh", "en", "zh"} {
		rec := httptest.NewRecorder()
		writer := &errorRecorder{ResponseWriter: WithLanguage(rec, lang)}
		if got := Language(writer); got != lang {
			t.Fatalf("language=%q", got)
		}
		frame := SSEErrorFrame(Localize(New(CodeConflict, "request conflicts with current state"), Language(writer)))
		if strings.Contains(string(frame), "当前状态") != (lang == "zh") {
			t.Fatalf("%s", frame)
		}
		if err := http.NewResponseController(writer).Flush(); err != nil || !rec.Flushed {
			t.Fatalf("flush=%v / %v", err, rec.Flushed)
		}
	}
}

func TestDisplayJSONRedactsOnlyTheRequestedProjection(t *testing.T) {
	value := map[string]string{"endpoint": "http://192.0.2.42:8080", "message": "ping 198.51.100.3"}
	display := httptest.NewRecorder()
	WriteDisplayJSON(display, 200, value)
	if strings.Contains(display.Body.String(), "192.0.2.42") || strings.Contains(display.Body.String(), "198.51.100.3") {
		t.Fatalf("display exposed address: %s", display.Body)
	}
	ordinary := httptest.NewRecorder()
	WriteJSON(WithLanguage(ordinary, "zh"), 200, value)
	if !strings.Contains(ordinary.Body.String(), "http://192.0.2.42:8080") || value["message"] != "ping 198.51.100.3" {
		t.Fatalf("configuration/model output or source was changed: %s", ordinary.Body)
	}
}

func TestSanitizedDiagnosticsCannotReconstructAnAddress(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteUpstreamError(rec, Error{Code: CodeUpstream, Message: "peer 192.0.\x002.5", Diag: "peer 192.0.\x002.5"}, 502)
	value := decodeEnvelope(t, rec.Body.String()).Error
	if value.Message != "peer [redacted]" || value.Diag != "peer [redacted]" {
		t.Fatalf("%+v", value)
	}
}

func TestSpecificBusinessErrorsRetainTheirActionInChinese(t *testing.T) {
	for _, tc := range []struct{ code, message, want string }{
		{CodeNotFound, "Open the activity before continuing.", "请先进入活动，再继续操作。"},
		{CodeConflict, "Refresh the model and confirm its current price before submitting.", "请刷新模型并确认当前价格，再提交。"},
		{CodeAlreadyCheckedIn, "the other daily check-in has already been claimed", "今天已经领取过另一项每日签到奖励。"},
	} {
		e := Localize(New(tc.code, tc.message), "zh")
		if e.Code != tc.code || e.Message != tc.want {
			t.Fatalf("%+v", e)
		}
	}
}

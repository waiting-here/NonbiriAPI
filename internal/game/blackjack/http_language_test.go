package blackjack

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func TestDurableConflictUsesRequestLanguageOnInitialAndReplay(t *testing.T) {
	original := []byte(`{"error":{"code":"conflict","message":"state conflict"}}`)
	for _, replayed := range []bool{false, true} {
		for _, language := range []string{"zh-CN", "en"} {
			result := MutationResult{Status: http.StatusConflict, Body: bytes.Clone(original), Replayed: replayed}
			w := httptest.NewRecorder()
			writeMutation(httperr.WithLanguage(w, language), result, nil)
			if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"conflict"`) {
				t.Fatalf("conflict changed: %d %s", w.Code, w.Body)
			}
			if language == "zh-CN" && (!strings.Contains(w.Body.String(), "当前状态") || w.Header().Get("Content-Language") != "zh") {
				t.Fatalf("untranslated replay=%v: %s", replayed, w.Body)
			}
			if language == "en" && !strings.Contains(w.Body.String(), "state conflict") {
				t.Fatalf("English response changed: %s", w.Body)
			}
			if !bytes.Equal(result.Body, original) {
				t.Fatal("durable receipt was rewritten")
			}
		}
	}
}

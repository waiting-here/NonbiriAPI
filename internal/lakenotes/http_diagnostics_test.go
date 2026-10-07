package lakenotes

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/applog"
)

func TestUnexpectedDatabaseErrorHasSafeHTTPDiagnostic(t *testing.T) {
	f := newFixture(t)
	f.enable(t)
	view := f.profile(t)
	const private = "private request credential and saved identifier"
	if _, err := f.database.Exec("CREATE TRIGGER reject_fishing_update BEFORE UPDATE ON lake_notes_profiles BEGIN SELECT RAISE(ABORT,'" + private + "'); END"); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(applog.New(&log, slog.LevelInfo))
	t.Cleanup(func() { slog.SetDefault(previous) })
	mux := http.NewServeMux()
	if err := RegisterRoutes(userRoutes{mux, f}, userRoutes{mux, f}, adminRoutes{mux, f}, f.service); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", baseRoute+"/casts", strings.NewReader(`{"expected_profile_revision":"`+view.Revision+`"}`))
	request.Header.Set("Idempotency-Key", testKey(980))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("unexpected public status", response.Code)
	}
	if strings.Contains(response.Body.String(), private) || strings.Contains(log.String(), private) {
		t.Fatal("raw database error escaped")
	}
	var entry map[string]any
	if err := json.Unmarshal(log.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["operation"] != "start_cast" || entry["category"] != "sqlite" || entry["sqlite_base_code"] != float64(19) || entry["sqlite_extended_code"] != float64(1811) {
		t.Fatal("incorrect safe diagnostic", entry)
	}
	for key := range entry {
		switch key {
		case "time", "level", "msg", "operation", "category", "sqlite_base_code", "sqlite_extended_code":
		default:
			t.Fatal("unexpected diagnostic field", key)
		}
	}
}

package lakenotes

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

type diagnosticGate func() error

func (gate diagnosticGate) AuthorizeUserActivity(context.Context, *sql.Tx, int64) error {
	return gate()
}

func TestLakeRequestCancellationDiagnostics(t *testing.T) {
	f := newFixture(t)
	mux := http.NewServeMux()
	if err := RegisterRoutes(userRoutes{mux, f}, userRoutes{mux, f}, adminRoutes{mux, f}, f.service); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(applog.New(&log, slog.LevelInfo))
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, tc := range []struct {
		name                string
		cancelRequest       bool
		cause, operationErr error
		category            string
	}{
		{"disconnected", true, nil, fmt.Errorf("operation: %w", context.Canceled), ""},
		{"disconnected with clean rollback", true, nil, errors.Join(context.Canceled, nil), ""},
		{"disconnected with storage failure", true, nil, errors.Join(context.Canceled, ErrInvariant), "stored_state"},
		{"internal cancellation", false, nil, context.Canceled, "cancelled"},
		{"server cancellation", true, errors.New("server shutdown"), context.Canceled, "cancelled"},
		{"deadline", true, context.DeadlineExceeded, context.DeadlineExceeded, "deadline"},
		{"storage failed during disconnect", true, nil, ErrInvariant, "stored_state"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log.Reset()
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			f.service.gate = diagnosticGate(func() error {
				if tc.cancelRequest {
					cancel(tc.cause)
				}
				return tc.operationErr
			})
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequestWithContext(ctx, "GET", baseRoute+"/profile", nil))
			if tc.category == "" {
				if len(response.Header()) != 0 || response.Body.Len() != 0 || log.Len() != 0 {
					t.Fatal("disconnected request wrote a response or logged a service error", response.Header(), log.String())
				}
				return
			}
			var entry struct{ Category string }
			if response.Code != http.StatusServiceUnavailable || json.Unmarshal(log.Bytes(), &entry) != nil || entry.Category != tc.category {
				t.Fatal("missing failure response or diagnostic", response.Code, log.String())
			}
		})
	}
}

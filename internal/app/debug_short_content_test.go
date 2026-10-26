package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/antiabuse"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func TestCharityShortContentDryWarningsDoNotPunishButLiveAndOrdinaryDo(t *testing.T) {
	for _, live := range []bool{false, true} {
		t.Run(fmt.Sprintf("live=%v", live), func(t *testing.T) {
			f := newEmbeddingHTTPFixture(t)
			for key, value := range map[string]string{
				antiabuse.KeyCharityMinChars:                  "20",
				antiabuse.KeyCharityViolationDeductMilli:      "7",
				antiabuse.KeyCharityViolationBanSeconds:       "0",
				antiabuse.KeyCharityViolationBanThreshold:     "2",
				antiabuse.KeyCharityViolationWindowSeconds:    "3600",
				antiabuse.KeyCharityViolationWindowBanSeconds: "60",
				antiabuse.KeyCharitySuspendThreshold:          "0",
			} {
				f.exec(t, `UPDATE site_config SET value=? WHERE key=?`, value, key)
			}
			const token = "short_content_debug_session_01234567890123456789"
			digest := sha256.Sum256([]byte(token))
			binding := fmt.Sprintf("%x", digest)
			now := time.Now().Unix()
			f.exec(t, `INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,?)`, binding, f.userID, now, now+3600, now+3600, now, "short-content-session")
			metadata, _, err := f.app.debug.Start(f.userID, binding)
			if err != nil {
				t.Fatal(err)
			}
			const body = `{"model":"[公益]provider/per_request","messages":[{"role":"user","content":"hi"}]}`
			assertState := func(wantEvents, wantDeductions, wantBan int) {
				t.Helper()
				var events, deductions, banned, reservations int
				var balance string
				if err := f.store.DB().QueryRow(`SELECT
(SELECT COUNT(*) FROM abuse_window_events WHERE user_id=u.id AND violation_kind='short_content'),
(SELECT COUNT(*) FROM abuse_cases WHERE user_id=u.id AND kind='deduction'),
u.is_banned,(SELECT COUNT(*) FROM logical_requests WHERE user_id=u.id AND accounting_state!='none'),
hex((SELECT balance_mag FROM credit_accounts WHERE user_id=u.id AND kind='user' AND asset_type='general'))
FROM users u WHERE u.id=?`, f.userID).Scan(&events, &deductions, &banned, &reservations, &balance); err != nil {
					t.Fatal(err)
				}
				wantBalance := fmt.Sprintf("%032X", 1000000-7*wantDeductions)
				if events != wantEvents || deductions != wantDeductions || banned != wantBan || reservations != 0 || balance != wantBalance {
					t.Fatalf("events=%d deductions=%d ban=%d reservations=%d balance=%s want=%s", events, deductions, banned, reservations, balance, wantBalance)
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				if len(f.upstreamRequests) != 0 {
					t.Fatal("short request reached upstream")
				}
			}
			for i := 0; i < 3; i++ {
				status, raw := f.post(t, "/v1/chat/completions", body)
				var response struct {
					Error struct{ Code, Message string }
				}
				if err := json.Unmarshal(raw, &response); err != nil {
					t.Fatal(err)
				}
				if status != http.StatusUnprocessableEntity || response.Error.Code != httperr.CodeDebugDryRunIntercepted || !strings.Contains(response.Error.Message, "2 < 20") || !strings.Contains(response.Error.Message, "no points were deducted") {
					t.Fatalf("dry request=%d %s", status, raw)
				}
			}
			assertState(0, 0, 0)
			sub, err := f.app.debug.Subscribe(context.Background(), f.userID, binding, "")
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Close()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			event, err := sub.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var snapshot debug.SnapshotData
			if err := json.Unmarshal(event.Data, &snapshot); err != nil || len(snapshot.Traces) != 3 {
				t.Fatal("missing dry traces", snapshot, err)
			}
			for _, trace := range snapshot.Traces {
				if trace.CallerResult == nil || !strings.Contains(trace.CallerResult.Message, "2 < 20") || !strings.Contains(trace.CallerResult.Message, "no points were deducted") {
					t.Fatal("missing warning in debug trace", trace)
				}
			}
			if live {
				if _, err := f.app.debug.ChangeMode(f.userID, metadata.Revision, debug.ModeLive, true); err != nil {
					t.Fatal(err)
				}
			} else if err := f.app.debug.Stop(f.userID, metadata.Revision, false); err != nil {
				t.Fatal(err)
			}
			status, raw := f.post(t, "/v1/chat/completions", body)
			if status != http.StatusBadRequest || !strings.Contains(string(raw), httperr.CodeContentTooShort) {
				t.Fatalf("first enforced request=%d %s", status, raw)
			}
			assertState(1, 1, 0)
			status, raw = f.post(t, "/v1/chat/completions", body)
			if status != http.StatusBadRequest || !strings.Contains(string(raw), httperr.CodeContentTooShort) {
				t.Fatalf("ban-triggering request lost its recorded rejection: %d %s", status, raw)
			}
			assertState(2, 2, 1)
		})
	}
}

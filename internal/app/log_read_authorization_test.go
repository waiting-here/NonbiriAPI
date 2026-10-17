package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestQueuedLogReadRechecksExactSessionAndMFAPolicy(t *testing.T) {
	for _, scenario := range []string{"admin_session", "user_session", "steward_session", "steward_mfa"} {
		t.Run(scenario, func(t *testing.T) {
			f := newGameWireFixture(t)
			host, path, cookies := auditUserHost, "/api/logs?page=1", f.cookies
			if scenario == "admin_session" {
				host, path, cookies = auditAdminHost, "/admin/api/logs?page=1", f.adminCookies
			} else if strings.HasPrefix(scenario, "steward_") {
				path = "/api/steward/logs?page=1"
				if _, err := f.store.DB().Exec(`UPDATE users SET level=6 WHERE id=?`, f.userID); err != nil {
					t.Fatal(err)
				}
			}
			call := func(cookies []*http.Cookie) *httptest.ResponseRecorder {
				return testApplicationRequest(t, f.app.handler, "GET", host, path, "", cookies, nil)
			}
			if response := call(cookies); response.Code != http.StatusOK {
				t.Fatalf("initial read: %d %s", response.Code, response.Body)
			}
			digest := sha256.Sum256([]byte(cookies[0].Value))
			tokenHash := fmt.Sprintf("%x", digest)
			otherCookie := &http.Cookie{Name: cookies[0].Name, Value: "independent_log_reader_session_0123456789"}
			otherDigest := sha256.Sum256([]byte(otherCookie.Value))
			if _, err := f.store.DB().Exec(`INSERT INTO sessions(token_hash,user_id,oauth_state,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen)
SELECT ?,user_id,oauth_state,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen FROM sessions WHERE token_hash=?`, fmt.Sprintf("%x", otherDigest), tokenHash); err != nil {
				t.Fatal(err)
			}
			reader := f.store.ReadDB()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			connections := make([]*sql.Conn, 0, reader.Stats().MaxOpenConnections)
			for range reader.Stats().MaxOpenConnections {
				connection, err := reader.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				connections = append(connections, connection)
				defer connection.Close()
			}
			waits := reader.Stats().WaitCount
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() { result <- call(cookies) }()
			for reader.Stats().WaitCount == waits {
				select {
				case response := <-result:
					t.Fatalf("read never waited: %d %s", response.Code, response.Body)
				case <-ctx.Done():
					t.Fatal("read did not reach the bounded reader pool")
				case <-time.After(time.Millisecond):
				}
			}
			if scenario == "steward_mfa" {
				if _, err := f.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key='mfa_required_level_6'`); err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.store.DB().Exec(`DELETE FROM sessions WHERE token_hash=?`, tokenHash); err != nil {
				t.Fatal(err)
			}
			for _, connection := range connections {
				if err := connection.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case response := <-result:
				if response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), `"data"`) {
					t.Fatalf("queued read exposed rows after revocation: %d %s", response.Code, response.Body)
				}
				if scenario == "steward_mfa" && !strings.Contains(response.Body.String(), `"code":"mfa_required"`) {
					t.Fatalf("MFA policy change lost its error: %s", response.Body)
				}
			case <-ctx.Done():
				t.Fatal("queued read did not finish after the connection was released")
			}
			if scenario != "steward_mfa" {
				if response := call([]*http.Cookie{otherCookie}); response.Code != http.StatusOK {
					t.Fatalf("unrelated session should still authorize: %d %s", response.Code, response.Body)
				}
			}
		})
	}
}

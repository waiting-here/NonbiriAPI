package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/textprivacy"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func TestStewardLogRoleMatrixUsesCurrentPermissionAndPayloadProjection(t *testing.T) {
	f := newEmbeddingHTTPFixture(t)
	if status, _ := f.call(t, "provider/self", `"sample"`, "float", "error"); status != 429 {
		t.Fatal(status)
	}
	var requestID string
	if err := f.store.DB().QueryRow(`SELECT logical_request_id FROM request_logs ORDER BY id DESC LIMIT 1`).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	const text = "peer 192.0.2.7 https://198.51.100.3/v1 IPv6 2001:db8::7 invalid 999.1.2.3"
	f.exec(t, `UPDATE request_attempts SET diag=?,upstream_code='192.0.2.8',canonical_base_url='https://192.0.2.99/v1' WHERE request_log_id=(SELECT id FROM request_logs WHERE logical_request_id=?)`, text, requestID)
	payloads := [][]byte{[]byte(text), append([]byte{0xff, 0, 0xfe}, []byte(text)...)}
	for i, payload := range payloads {
		ctx := f.app.audits.observations.ErrorScope(context.Background(), observability.DiagnosticRef{RequestID: requestID, AttemptSeq: 1, EventSeq: i + 1})
		upstreamerror.CaptureEvent(ctx, 503, "application/octet-stream", payload)
	}
	const token = "cross-user-log-session-01234567890123456789"
	digest, now := sha256.Sum256([]byte(token)), time.Now().Unix()
	f.exec(t, `INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,?)`, fmt.Sprintf("%x", digest), f.donorID, now, now+3600, now+3600, now, "log-viewer")
	cookies := []*http.Cookie{{Name: auth.UserSessionCookieName, Value: token}}
	root := "/api/steward/logs/" + requestID
	for _, level := range []int{6, 5, 4, 3, 2, 1, 5} {
		f.exec(t, "UPDATE users SET level=? WHERE id=?", level, f.donorID)
		for _, path := range []string{"/api/steward/logs", root, "/api/steward/logs/export.json", "/api/steward/logs/export.csv", root + "/source", root + "/attempts/1/errors", root + "/attempts/1/errors/2", root + "/attempts/1/errors/3", "/api/steward/logs/pressure", "/api/steward/logs/diagnostic-capacity", "/api/steward/diagnostics"} {
			got := testApplicationRequest(t, f.app.handler, "GET", auditUserHost, path, "", cookies, nil)
			want := http.StatusOK
			if level < 5 {
				want = http.StatusForbidden
			}
			if got.Code != want {
				t.Fatalf("L%d %s: %d %s", level, path, got.Code, got.Body)
			}
			if want != http.StatusOK {
				continue
			}
			if path == root {
				var detail logapi.StewardLogDetail
				if err := json.Unmarshal(got.Body.Bytes(), &detail); err != nil {
					t.Fatal(err)
				}
				attempt := detail.Attempts.Data[0]
				wantDiag, wantCode := text, "192.0.2.8"
				if level == 5 {
					wantDiag, wantCode = textprivacy.IPv4(text), "[redacted]"
				}
				if attempt.Diag == nil || *attempt.Diag != wantDiag || attempt.UpstreamCode == nil || *attempt.UpstreamCode != wantCode || attempt.EndpointBaseURL != "https://192.0.2.99/v1" {
					t.Fatal("incorrect projection", attempt)
				}
			}
			if strings.HasSuffix(path, "/errors/2") || strings.HasSuffix(path, "/errors/3") {
				var body observability.ErrorBody
				if err := json.Unmarshal(got.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				index := 0
				if strings.HasSuffix(path, "/3") {
					index = 1
				}
				decoded := body.Body
				if body.Encoding == "base64" {
					b, err := base64.StdEncoding.DecodeString(decoded)
					if err != nil {
						t.Fatal(err)
					}
					decoded = string(b)
				}
				want := string(payloads[index])
				if level == 5 {
					want = textprivacy.IPv4(want)
				}
				if decoded != want || body.IPv4Redacted != (level == 5) || body.BytesSaved != int64(len(payloads[index])) {
					t.Fatalf("L%d body projection: %+v", level, body)
				}
			}
			if strings.HasSuffix(path, "/source") && (!strings.Contains(got.Body.String(), `"effective_ip"`) || strings.Contains(got.Body.String(), "[redacted]")) {
				t.Fatal("source facts changed", got.Body)
			}
		}
		for _, path := range []string{"/api/steward/abuse-audit/users", "/api/steward/abuse-audit/access-events", "/api/steward/abuse-audit/access-summary"} {
			got := testApplicationRequest(t, f.app.handler, "GET", auditUserHost, path, "", cookies, nil)
			if level < 6 && got.Code != http.StatusForbidden {
				t.Fatalf("L%d widened abuse audit: %s %d", level, path, got.Code)
			}
		}
	}
	f.exec(t, "UPDATE users SET is_banned=1 WHERE id=?", f.donorID)
	if got := testApplicationRequest(t, f.app.handler, "GET", auditUserHost, root, "", cookies, nil); got.Code != http.StatusForbidden {
		t.Fatal("banned viewer retained logs", got.Code)
	}
	f.exec(t, "UPDATE users SET is_banned=0 WHERE id=?", f.donorID)
	f.exec(t, "UPDATE site_config SET value='1' WHERE key='mfa_required_level_5'")
	if got := testApplicationRequest(t, f.app.handler, "GET", auditUserHost, root, "", cookies, nil); got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), "mfa_required") {
		t.Fatal("MFA bypass", got.Code, got.Body)
	}
	for _, level := range []int{5, 6} {
		f.exec(t, "UPDATE users SET level=? WHERE id=?", level, f.userID)
		got := testApplicationRequest(t, f.app.handler, "GET", auditUserHost, root, "", nil, map[string]string{"Authorization": "Bearer " + f.caller})
		if got.Code != http.StatusForbidden && got.Code != http.StatusUnauthorized {
			t.Fatal("CallerKey gained browser logs", got.Code, got.Body)
		}
	}
}

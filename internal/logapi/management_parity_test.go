package logapi

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func requireManagementLogEqual(t *testing.T, admin, steward any) {
	t.Helper()
	a, err := json.Marshal(admin)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(steward)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("management projections differ:\n%s\n%s", a, b)
	}
}

func TestManagementLogIdentityChargeAndAttemptSnapshotsAgree(t *testing.T) {
	f := newLogFixture(t)
	ctx := context.Background()
	f.mustExec(`INSERT INTO users(id,discord_id,username,guild_nick) VALUES(?,?,'caller','current nickname')`, logUserOne, "12345678901234567890")
	f.mustExec(`UPDATE credit_operations SET source_id=? WHERE id='op-self'`, f.charityID)
	f.mustExec(`UPDATE request_attempts SET endpoint_key_id_snapshot=999 WHERE request_log_id=2 AND attempt_seq=2`)
	assertDetail := func() {
		t.Helper()
		admin, err := f.repo.GetAdmin(ctx, f.charityID, AttemptFilter{})
		if err != nil {
			t.Fatal(err)
		}
		steward, err := f.repo.GetSteward(ctx, 999, f.charityID, AttemptFilter{}, allowLogStewardRead{})
		if err != nil {
			t.Fatal(err)
		}
		requireManagementLogEqual(t, admin, steward)
		if admin.Request.Usage.Charge != "1.234" || admin.Request.UserID == nil || *admin.Request.UserID != "101" || admin.Request.CallerIdentity == nil {
			t.Fatalf("management request lost authoritative facts: %+v", admin.Request)
		}
		if len(admin.Attempts.Data) != 2 || *admin.Attempts.Data[0].EndpointKeyID != "302" || *admin.Attempts.Data[1].EndpointKeyID != "999" {
			t.Fatalf("retry key snapshots changed: %+v", admin.Attempts)
		}
		noLogSentinel(t, admin, "RAW-REQUEST-BODY", "RAW-AUTH", "RAW-CIPHERTEXT", "RAW-UPSTREAM", "RAW-DISCORD")
		caller, err := f.repo.GetUser(ctx, logUserOne, f.charityID, AttemptFilter{})
		if err != nil {
			t.Fatal(err)
		}
		requireNoJSONKeys(t, caller, "user_id", "caller_identity", "endpoint_key_id", "attempts", "endpoint_base_url")
	}
	assertDetail()
	// Removing a live source key cannot erase the historical routing snapshot.
	f.mustExec(`DELETE FROM endpoint_keys WHERE id=302`)
	assertDetail()
	f.mustExec(`UPDATE request_attempts SET endpoint_key_id_snapshot=NULL WHERE request_log_id=2 AND attempt_seq=2`)
	detail, err := f.repo.GetSteward(ctx, 999, f.charityID, AttemptFilter{}, allowLogStewardRead{})
	if err != nil || detail.Attempts.Data[1].EndpointKeyID != nil {
		t.Fatalf("missing snapshot became an invented key: %+v %v", detail, err)
	}
	userID := logUserOne
	for _, filter := range []ListFilter{{}, {UserID: &userID}} {
		admin, err := f.repo.ListAdmin(ctx, filter)
		if err != nil {
			t.Fatal(err)
		}
		steward, err := f.repo.ListSteward(ctx, 999, filter, allowLogStewardRead{})
		if err != nil {
			t.Fatal(err)
		}
		requireManagementLogEqual(t, admin, steward)
	}
}

func TestStewardExportParityFinalAuthorizationAndCSVIdentitySafety(t *testing.T) {
	f := newLogFixture(t)
	ctx := context.Background()
	f.mustExec(`INSERT INTO users(id,discord_id,username) VALUES(?,?,'=1+1')`, logUserOne, "@identity")
	admin, err := f.repo.ExportAdmin(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	steward, err := f.repo.ExportSteward(ctx, 999, ListFilter{}, allowLogStewardRead{})
	if err != nil {
		t.Fatal(err)
	}
	requireManagementLogEqual(t, admin, steward)
	body, err := MarshalAdminCSV(steward)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if rows[2][16] != "'=1+1" || rows[2][17] != "'@identity" {
		t.Fatalf("identity formula escaped incorrectly: %v", rows[2])
	}
	for _, format := range []string{"csv", "json"} {
		registrar := &logUserTestRegistrar{}
		auth := &logStewardTestAuthorizer{results: []error{nil, ErrForbidden}}
		if err := RegisterStewardRoutes(registrar, f.repo, auth); err != nil {
			t.Fatal(err)
		}
		path := "/api/steward/logs/export." + format
		response := callLogUserHandler(t, registrar.handlers["GET "+path], 999, path, "", nil)
		if response.Code != http.StatusForbidden || auth.callCount() != 2 || strings.Contains(response.Body.String(), f.charityID) {
			t.Fatalf("revoked export returned data: %d %s", response.Code, response.Body.String())
		}
		response = callLogUserHandler(t, registrar.handlers["GET "+path], 999, path+"?user_id=101", "", nil)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), f.charityID) || strings.Contains(response.Body.String(), f.otherID) {
			t.Fatalf("filtered export failed: %d %s", response.Code, response.Body.String())
		}
	}
}

func TestManagementLogCursorsRemainBoundToRoleAndActor(t *testing.T) {
	f := newLogFixture(t)
	ctx := context.Background()
	admin, err := f.repo.ListAdmin(ctx, ListFilter{Limit: 1})
	if err != nil || admin.NextCursor == nil {
		t.Fatal(admin, err)
	}
	if _, err := f.repo.ListSteward(ctx, 999, ListFilter{Limit: 1, Cursor: *admin.NextCursor}, allowLogStewardRead{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("admin cursor accepted by steward: %v", err)
	}
	steward, err := f.repo.ListSteward(ctx, 999, ListFilter{Limit: 1}, allowLogStewardRead{})
	if err != nil || steward.NextCursor == nil {
		t.Fatal(steward, err)
	}
	if _, err := f.repo.ListSteward(ctx, 998, ListFilter{Limit: 1, Cursor: *steward.NextCursor}, allowLogStewardRead{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("another steward's cursor accepted: %v", err)
	}
}

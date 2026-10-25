package logapi

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

func TestManagementLogDisplayAndExportsPreserveSavedText(t *testing.T) {
	model := "backend-192.0.2.7"
	nick := "peer 198.51.100.2"
	detail := "person@example.invalid https://example.invalid/path?token=test Authorization: Bearer saved-value <script> & " + strings.Repeat("long ", 20000)
	admin := []AdminLogRow{{ID: "1", CharityModel: &model, CallerIdentity: &CallerIdentity{DiscordNickname: &nick}, RejectionFields: RejectionFields{RejectionDetail: &requestattempt.RejectionDetail{Field: "input", Reason: detail}}}}
	output := httptest.NewRecorder()
	writeManagementLogJSON(output, 200, admin)
	bodies := [][]byte{output.Body.Bytes()}
	for _, encode := range []func() ([]byte, error){
		func() ([]byte, error) { return MarshalAdminJSON(admin) },
		func() ([]byte, error) { return MarshalAdminCSV(admin) },
	} {
		b, err := encode()
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, b)
	}
	for _, b := range bodies {
		if !bytes.Contains(b, []byte(model)) || !bytes.Contains(b, []byte(nick)) || !bytes.Contains(b, []byte(detail[:70])) || bytes.Contains(b, []byte("[redacted]")) {
			t.Fatalf("incomplete management display (%d bytes)", len(b))
		}
	}
	var shown []AdminLogRow
	if err := json.Unmarshal(output.Body.Bytes(), &shown); err != nil || shown[0].RejectionDetail.Reason != detail || bytes.Contains(output.Body.Bytes(), []byte("<script>")) {
		t.Fatal("management JSON changed stored text or failed HTML encoding", err)
	}
	records, err := csv.NewReader(bytes.NewReader(bodies[2])).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, value := range records[1] {
		if value == detail {
			found = true
		}
	}
	if !found {
		t.Fatal("CSV omitted long saved detail")
	}
	if model != "backend-192.0.2.7" || *admin[0].CallerIdentity.DiscordNickname != nick {
		t.Fatal("source facts changed")
	}
}

func TestOrdinaryLogDisplaysAndExportsKeepRedaction(t *testing.T) {
	model := "backend-192.0.2.7"
	user := []UserLogRow{UserSelfLogRow{ID: "1", Model: model}}
	output := httptest.NewRecorder()
	writeLogJSON(output, 200, user)
	bodies := [][]byte{output.Body.Bytes()}
	for _, encode := range []func() ([]byte, error){
		func() ([]byte, error) { return MarshalUserJSON(user) },
		func() ([]byte, error) { return MarshalUserCSV(user) },
	} {
		b, err := encode()
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, b)
	}
	for _, b := range bodies {
		if bytes.Contains(b, []byte("192.0.2.7")) || !bytes.Contains(b, []byte("[redacted]")) {
			t.Fatalf("ordinary display: %s", b)
		}
	}
}

func TestPrivilegedLogHTTPPreservesSavedFields(t *testing.T) {
	f := newLogFixture(t)
	nick := "peer 198.51.100.2"
	diag := "peer 192.0.2.7 person@example.invalid https://upstream.invalid/x?token=saved Authorization: Bearer saved <script> " + strings.Repeat("x", 3900)
	if _, err := f.db.Exec(`INSERT INTO users(id,guild_nick) VALUES(?,?)`, logUserOne, nick); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE request_attempts SET diag=?,canonical_base_url='https://192.0.2.7/v1' WHERE request_log_id=(SELECT id FROM request_logs WHERE logical_request_id=?)`, diag, f.selfID); err != nil {
		t.Fatal(err)
	}
	admin := &logAdminTestRegistrar{}
	steward := &logUserTestRegistrar{}
	if err := RegisterAdminRoutes(admin, f.repo); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStewardRoutes(steward, f.repo, allowLogStewardRead{}); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"admin", "steward"} {
		for _, suffix := range []string{"", "/" + f.selfID, "/export.json", "/export.csv"} {
			var response *httptest.ResponseRecorder
			if role == "admin" {
				pattern := "/admin/api/logs" + suffix
				if suffix == "/"+f.selfID {
					pattern = "/admin/api/logs/{id}"
				}
				response = callLogAdminHandler(t, admin.handlers["GET "+pattern], "/admin/api/logs"+suffix, "", map[string]string{"id": f.selfID})
			} else {
				pattern := "/api/steward/logs" + suffix
				if suffix == "/"+f.selfID {
					pattern = "/api/steward/logs/{id}"
				}
				response = callLogUserHandler(t, steward.handlers["GET "+pattern], logUserTwo, "/api/steward/logs"+suffix, "", map[string]string{"id": f.selfID})
			}
			if response.Code != http.StatusOK || (suffix != "/"+f.selfID && !strings.Contains(response.Body.String(), nick)) {
				t.Fatalf("%s %s: %d %s", role, suffix, response.Code, response.Body)
			}
			if suffix == "/"+f.selfID {
				var shown AdminLogDetail
				if err := json.Unmarshal(response.Body.Bytes(), &shown); err != nil || shown.Attempts.Data[0].Diag == nil || *shown.Attempts.Data[0].Diag != diag {
					t.Fatalf("incomplete %s attempt: %v", role, err)
				}
			}
		}
	}
}

package logapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestOwnerLogExportFinalAccountProjectionAndPhase(t *testing.T) {
	f := newNumberedLogFixture(t)
	f.mustExec(`UPDATE request_logs SET rejection_stage='preflight',rejection_reason='invalid_request',request_method='POST',request_path='/v1/chat/completions' WHERE logical_request_id=?`, f.selfID)
	rows, err := f.repo.ExportUser(context.Background(), logUserOne, ListFilter{Phase: "pre_handler"})
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	body, err := MarshalUserJSON(rows)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), f.selfID) || strings.Contains(string(body), f.otherID) || strings.Contains(string(body), "caller_identity") {
		t.Fatal("export privacy", string(body))
	}
	rows, err = f.repo.ExportUser(context.Background(), logUserOne, ListFilter{Phase: "handler"})
	if err != nil {
		t.Fatal(err)
	}
	body, err = MarshalUserJSON(rows)
	if err != nil {
		t.Fatal(err)
	}
	var exported struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &exported); err != nil {
		t.Fatal(err)
	}
	for _, r := range exported.Data {
		if r["id"] == f.charityID {
			if _, exists := r["attempt_count"]; exists {
				t.Fatal("charity attempt count exposed")
			}
		}
		if r["id"] == f.otherID || r["id"] == f.selfID {
			t.Fatal("filter or ownership lost", r)
		}
	}
	formula := UserSelfLogRow{Model: "=1+1"}
	body, err = MarshalUserCSV([]UserLogRow{formula})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil || parsed[1][2] != "'=1+1" {
		t.Fatal("CSV sink", parsed, err)
	}
	for _, filter := range []ListFilter{{UserID: new(logUserTwo)}, {EndpointBaseURL: new("https://example.test")}, {Cursor: "x"}, {Limit: 1}} {
		if _, err := f.repo.ExportUser(context.Background(), logUserOne, filter); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted invalid export filter", filter, err)
		}
	}
	f.mustExec(`UPDATE users SET is_banned=1 WHERE id=?`, logUserOne)
	if _, err := f.repo.ExportUser(context.Background(), logUserOne, ListFilter{}); !errors.Is(err, ErrForbidden) {
		t.Fatal("stale owner authority", err)
	}
	if _, err := MarshalUserJSON(make([]UserLogRow, maxExportRows+1)); !errors.Is(err, ErrCapacity) {
		t.Fatal("export truncation", err)
	}
}

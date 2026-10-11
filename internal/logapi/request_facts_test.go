package logapi

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRequestFactsProjectionExportsAndHistoricalUnknown(t *testing.T) {
	f := newNumberedLogFixture(t)
	f.mustExec("UPDATE request_logs SET requested_streaming=1,automatic_actions=63 WHERE logical_request_id=?", f.charityID)
	want := []string{"short_content_deduction", "short_content_ban", "short_content_suspension", "rpm_ban", "client_rule_ban", "context_penalty"}
	user, err := f.repo.GetUser(t.Context(), logUserOne, f.charityID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	row := user.(UserCharityLogDetail).Request
	if row.RequestedStreaming == nil || !*row.RequestedStreaming || !reflect.DeepEqual(row.AutomaticActions, want) {
		t.Fatal(row)
	}
	rows, err := f.repo.ExportUser(t.Context(), logUserOne, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := MarshalUserCSV(rows)
	if err != nil || !strings.Contains(string(data), "context_penalty") {
		t.Fatal(string(data), err)
	}
	data, _ = json.Marshal(row)
	if strings.Contains(string(data), "rules_json") || strings.Contains(string(data), "request_body") {
		t.Fatal(string(data))
	}
	history, err := f.repo.GetUser(t.Context(), logUserOne, f.selfID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	old := history.(UserSelfLogDetail).Request
	if old.RequestedStreaming != nil || old.AutomaticActions != nil {
		t.Fatal("historical facts guessed")
	}
	f.mustExec("UPDATE request_logs SET requested_streaming=0,automatic_actions=0 WHERE logical_request_id=?", f.charityID)
	current, err := f.repo.GetUser(t.Context(), logUserOne, f.charityID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	empty := current.(UserCharityLogDetail).Request
	if empty.RequestedStreaming == nil || *empty.RequestedStreaming || empty.AutomaticActions == nil || len(empty.AutomaticActions) != 0 {
		t.Fatal(empty)
	}
}

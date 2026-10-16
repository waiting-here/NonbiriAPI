package reports

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestReportDisplayRedactsSourceAndNotesWithoutChangingEvidence(t *testing.T) {
	e := newReportTestEnvironment(t)
	owner := e.seedActor(t, false, 1)
	endpoint := e.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	e.seedEndpointKey(t, endpoint, "reported-test-secret", reportTestNow)
	input := e.submission("reported-test-secret", "Observed at 203.0.113.8", 1000, 1, nil)
	input.SourceIP = netip.MustParseAddr("198.51.100.42").As16()
	if err := e.repository.AcceptCredentialTheft(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	id := e.caseIDForSecret(t, "reported-test-secret")
	e.runUntilIdle(t, 10)
	admin := e.seedActor(t, true, 1)
	read := func() CaseDetail {
		t.Helper()
		value, err := e.repository.CaseDetail(context.Background(), admin, id, "", 20)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	detail := read()
	w := httptest.NewRecorder()
	writeReportJSON(w, http.StatusOK, detail)
	if w.Code != 200 || strings.Contains(w.Body.String(), "198.51.100.42") || strings.Contains(w.Body.String(), "203.0.113.8") {
		t.Fatalf("unredacted report: %d %s", w.Code, w.Body)
	}
	var shown CaseDetail
	if err := json.Unmarshal(w.Body.Bytes(), &shown); err != nil {
		t.Fatal(err)
	}
	if len(shown.Materials.Data) != 1 || shown.Materials.Data[0].SourceIP != "[redacted]" || shown.Materials.Data[0].NoteText != "Observed at [redacted]" {
		t.Fatalf("unexpected display: %+v", shown.Materials)
	}
	stored := read().Materials.Data[0]
	if stored.SourceIP != "198.51.100.42" || stored.NoteText != input.Note {
		t.Fatalf("stored evidence changed: %+v", stored)
	}
}

package claim

import (
	"context"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
	"testing"
)

func TestNewInvalidRequestFallbackPreservesSpecificDetailAndReplay(t *testing.T) {
	f := newClaimFixture(t)
	user := f.seedUser("diagnostics", false)
	for _, detail := range []*requestattempt.RejectionDetail{nil, requestattempt.NewDetail("messages", "expected an array of message objects")} {
		id, err := db.GenerateOpaqueID("req_")
		if err != nil {
			t.Fatal(err)
		}
		fact := requestattempt.Fact{ID: id, Method: "POST", Path: "/v1/chat/completions", Stage: "preflight", Reason: "invalid_request", Status: 400, Code: "invalid_request", Detail: detail}
		if err := f.service.RecordRejection(context.Background(), user, fact); err != nil {
			t.Fatal(err)
		}
		var encoded string
		if err := f.db.QueryRow("SELECT error_diag FROM request_logs WHERE logical_request_id=?", id).Scan(&encoded); err != nil {
			t.Fatal(err)
		}
		saved := requestattempt.DecodeDetail(encoded)
		if saved == nil || saved.Suggestion == "" {
			t.Fatal(encoded)
		}
		if detail != nil && *saved != *detail {
			t.Fatal("specific check overwritten", saved)
		}
		if detail == nil && saved.Reason != "request validation failed without a recorded field" {
			t.Fatal("invented cause", saved)
		}
		fact.Detail = requestattempt.NewDetail("stream", "expected a boolean")
		if err := f.service.RecordRejection(context.Background(), user, fact); err != nil {
			t.Fatal(err)
		}
		var replay string
		if err := f.db.QueryRow("SELECT error_diag FROM request_logs WHERE logical_request_id=?", id).Scan(&replay); err != nil || replay != encoded {
			t.Fatal("replay changed recorded cause", replay, err)
		}
	}
}

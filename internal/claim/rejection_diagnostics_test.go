package claim

import (
	"context"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
	"testing"
)

func TestUndispatchedResourceDetailPersistsOnceWithoutReplacingUpstream(t *testing.T) {
	f := newClaimFixture(t)
	user := f.seedUser("resource-diagnostics", false)
	key := f.seedKey(user, "resource-diagnostics")
	want := requestattempt.NewDetail("model", "no usable model resource is available for this request")
	for _, dispatched := range []bool{false, true} {
		for _, code := range []string{httperr.CodeUnboundModel, httperr.CodeRateLimited, httperr.CodeInternal} {
			request := f.acceptSelf(user, 1)
			disposition := AccountingRelease
			if dispatched {
				handle, err := f.service.Claim(t.Context(), ClaimInput{RequestID: request.ID, ActorUserID: user, AttemptSeq: 1, Purpose: PurposeSelf, PersonalModelID: key.modelID, Candidate: key.candidate})
				if err != nil {
					t.Fatal(err)
				}
				dispatch, err := f.service.TakeForDispatch(t.Context(), handle)
				if err != nil {
					t.Fatal(err)
				}
				dispatch.Clear()
				if _, err := f.service.CompleteAttempt(t.Context(), handle, AttemptOutcome{Kind: ResultSynthetic, UpstreamStatus: 502, Diagnostic: "saved upstream failure"}); err != nil {
					t.Fatal(err)
				}
				disposition = AccountingCommit
			}
			input := CompleteRequestInput{RequestID: request.ID, Caller: CallerResult{Class: ResultFailed, Status: 503, ErrorCode: code}, Disposition: disposition, RejectionDetail: want}
			var before, after string
			if err := f.db.QueryRow(`SELECT error_diag FROM request_logs WHERE logical_request_id=?`, request.ID).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.CompleteRequest(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			if err := f.db.QueryRow(`SELECT error_diag FROM request_logs WHERE logical_request_id=?`, request.ID).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if !dispatched && code != httperr.CodeInternal {
				if detail := requestattempt.DecodeDetail(after); detail == nil || *detail != *want {
					t.Fatal("missing safe terminal detail", after)
				}
			} else if after != before {
				t.Fatal("replaced a dispatched or unrelated diagnostic", before, after)
			}
			input.RejectionDetail = requestattempt.NewDetail("model", "shared model resources are temporarily at capacity")
			if _, err := f.service.CompleteRequest(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			var replay string
			if err := f.db.QueryRow(`SELECT error_diag FROM request_logs WHERE logical_request_id=?`, request.ID).Scan(&replay); err != nil || replay != after {
				t.Fatal("replay changed the recorded reason", replay, err)
			}
		}
	}
}

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

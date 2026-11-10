package claim

import (
	"errors"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/requestkind"
)

func TestRerankPathRevalidatedAtDispatch(t *testing.T) {
	for _, test := range []struct {
		name                                  string
		charity, rerank, override, wantReject bool
	}{
		{"self_rerank", false, true, false, true}, {"self_unchanged", false, true, false, false}, {"other_operation", false, false, false, false},
		{"charity_snapshot", true, true, false, false}, {"charity_override", true, true, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newClaimFixture(t)
			owner := f.seedUser("path-owner", false)
			caller := owner
			key := f.seedKey(owner, "path-key")
			op := contract.OperationChatCompletions
			if test.rerank {
				op = contract.OperationRerank
			}
			route := requestkind.ForOperation(op, test.charity)
			input := ClaimInput{ActorUserID: caller, PersonalModelID: key.modelID, AttemptSeq: 1, Purpose: PurposeSelf}
			var request Request
			if test.charity {
				caller = f.seedUser("path-caller", false)
				input.ActorUserID = caller
				input.DonationKeyID = f.seedDonationKey(owner, key, "path-donation", 2)
				input.Purpose = PurposeCharity
				input.PersonalModelID = 0
				request = f.acceptCharity(caller, 1, route)
			} else {
				request = f.acceptSelf(caller, 1, route)
			}
			input.RequestID = request.ID
			input.Candidate = key.candidateForRoute(route, test.charity)
			if test.rerank && !test.charity {
				if _, err := f.db.Exec("UPDATE endpoints SET rerank_path='/initial/reranks' WHERE id=?", key.endpointID); err != nil {
					t.Fatal(err)
				}
				input.Candidate.Authorization.RerankPath = "/initial/reranks"
			}
			handle, err := f.service.Claim(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if test.name == "self_unchanged" {
				_, err = f.db.Exec("UPDATE endpoints SET note='updated note' WHERE id=?", key.endpointID)
			} else if test.override {
				_, err = f.db.Exec("UPDATE donation_uses SET rerank_path_override='/managed/reranks',revision=revision+1 WHERE id=?", input.Candidate.Authorization.UseID)
			} else {
				_, err = f.db.Exec("UPDATE endpoints SET rerank_path='/changed/reranks' WHERE id=?", key.endpointID)
			}
			if err != nil {
				t.Fatal(err)
			}
			dispatch, err := f.service.TakeForDispatch(t.Context(), handle)
			if test.wantReject {
				if !errors.Is(err, ErrNotFound) || dispatch != nil || f.codec.openCount() != 0 {
					t.Fatalf("changed path dispatched: %v", err)
				}
				if _, err = f.service.ReleaseUndispatched(t.Context(), handle); err != nil {
					t.Fatal(err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if handle.Target().RerankPath() != input.Candidate.Authorization.RerankPath {
					t.Fatal("unrelated configuration leaked into target")
				}
				dispatch.Clear()
			}
		})
	}
}

package claim

import (
	"context"
	"errors"
	"testing"
)

func connectionClaimInput(t *testing.T) (*claimFixture, ClaimInput) {
	t.Helper()
	f := newClaimFixture(t)
	owner := f.seedUser("connection-owner", false)
	key := f.seedKey(owner, "connection-key")
	request := f.acceptSelf(owner, 1)
	return f, ClaimInput{RequestID: request.ID, ActorUserID: owner, PersonalModelID: key.modelID, AttemptSeq: 1, Purpose: PurposeSelf, Candidate: key.candidate}
}

func TestConnectionClaimRejectsStaleOrForeignAuthorityBeforeWrites(t *testing.T) {
	f, valid := connectionClaimInput(t)
	foreign := f.seedUser("connection-other", false)
	for name, mutate := range map[string]func(*ClaimInput){
		"foreign caller":     func(in *ClaimInput) { in.ActorUserID = foreign },
		"credential version": func(in *ClaimInput) { in.Candidate.Authorization.CredentialVersion++ },
		"grant revision":     func(in *ClaimInput) { in.Candidate.Authorization.GrantRevision++ },
		"target revision":    func(in *ClaimInput) { in.Candidate.Authorization.TargetRevision++ },
		"missing target": func(in *ClaimInput) {
			in.Candidate.Authorization.TargetID = 0
			in.Candidate.Authorization.TargetRevision = 0
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := valid
			mutate(&input)
			if _, err := f.service.Claim(context.Background(), input); !errors.Is(err, ErrNotFound) {
				t.Fatalf("claim accepted stale/foreign authority: %v", err)
			}
		})
	}
	var claims, targets int
	if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM dispatch_claims),(SELECT count(*) FROM dispatch_claim_targets)`).Scan(&claims, &targets); err != nil || claims != 0 || targets != 0 || f.codec.openCount() != 0 {
		t.Fatalf("rejected authority wrote/decrypted: claims=%d targets=%d opens=%d err=%v", claims, targets, f.codec.openCount(), err)
	}
}

func TestConnectionClaimRechecksTargetAtDispatchAndReleasesOnlyItsReference(t *testing.T) {
	f, input := connectionClaimInput(t)
	ctx := context.Background()
	handle, err := f.service.Claim(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE model_binding_targets SET revision=revision+1 WHERE id=?`, input.Candidate.Authorization.TargetID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.TakeForDispatch(ctx, handle); !errors.Is(err, ErrNotFound) || f.codec.openCount() != 0 {
		t.Fatalf("revoked target dispatched: opens=%d err=%v", f.codec.openCount(), err)
	}
	for range 2 {
		if _, err := f.service.ReleaseUndispatched(ctx, handle); err != nil {
			t.Fatal(err)
		}
	}
	var targets, secrets, attempts int
	if err := f.db.QueryRow(`SELECT (SELECT count(*) FROM dispatch_claim_targets),(SELECT count(*) FROM endpoint_key_secrets),(SELECT count(*) FROM request_attempts)`).Scan(&targets, &secrets, &attempts); err != nil || targets != 0 || secrets != 1 || attempts != 0 {
		t.Fatalf("release references: targets=%d secrets=%d attempts=%d err=%v", targets, secrets, attempts, err)
	}
}

func TestConnectionClaimCompletionKeepsSafeFactsAfterAuthorizationIsRemoved(t *testing.T) {
	f, input := connectionClaimInput(t)
	ctx := context.Background()
	handle, err := f.service.Claim(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := f.service.TakeForDispatch(ctx, handle)
	if err != nil {
		t.Fatal(err)
	}
	dispatch.Clear()
	if _, err := f.db.Exec(`UPDATE endpoint_keys SET enabled=0 WHERE id=?`, input.Candidate.EndpointKeyID); err != nil {
		t.Fatal(err)
	}
	outcome := AttemptOutcome{Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true}
	for range 2 {
		if _, err := f.service.CompleteAttempt(ctx, handle, outcome); err != nil {
			t.Fatal(err)
		}
	}
	var state, configured, wire string
	var targetRefs, secretRefs, facts int
	var credentialID, version, grantRevision, targetRevision int64
	if err := f.db.QueryRow(`SELECT c.state,c.secret_ref_id IS NOT NULL,
(SELECT count(*) FROM dispatch_claim_targets WHERE claim_id=c.id),
(SELECT count(*) FROM request_attempt_service_facts WHERE claim_id=c.id),
f.credential_id_snapshot,f.credential_version,f.grant_revision,f.target_revision,f.configured_type,f.wire_type
FROM dispatch_claims c JOIN request_attempt_service_facts f ON f.claim_id=c.id WHERE c.id=?`, handle.ClaimID()).Scan(&state, &secretRefs, &targetRefs, &facts, &credentialID, &version, &grantRevision, &targetRevision, &configured, &wire); err != nil {
		t.Fatal(err)
	}
	a := input.Candidate.Authorization
	if state != "committed" || secretRefs != 0 || targetRefs != 0 || facts != 1 || credentialID != a.CredentialID || version != a.CredentialVersion || grantRevision != a.GrantRevision || targetRevision != a.TargetRevision || configured != string(a.ConfiguredType) || wire != string(input.Candidate.ConnectorType) {
		t.Fatalf("terminal facts lost or live reference retained: state=%s refs=%d/%d facts=%d versions=%d/%d/%d/%d types=%s/%s", state, secretRefs, targetRefs, facts, credentialID, version, grantRevision, targetRevision, configured, wire)
	}
}

package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestPolicyAuditIsAtomicAppendOnlyAndAnonymizesDeletedActor(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "policy-audit.db"))
	defer st.Close()
	ctx := context.Background()
	uid := seedUserRaw(t, st, "policy-audit-user")
	eid := seedEndpointRaw(t, st, uid, true)
	key, err := st.CreateEndpointKeyWithPolicy(ctx, uid, eid, []byte("policy-secret"), "poli", "cret", "", true, true, testNow, "owner")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	model, err := st.CreateModelWithPolicy(ctx, uid, "provider", "model", "ordered", false, true, testNow, "owner")
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	audits, err := st.ListPolicyAudits(ctx, "endpoint_key", key.ID, 10)
	if err != nil || len(audits) != 1 || !audits[0].NewValue || audits[0].ActorUserID == nil || *audits[0].ActorUserID != uid {
		t.Fatalf("key audit = %+v err=%v", audits, err)
	}
	audits, err = st.ListPolicyAudits(ctx, "model", model.ID, 10)
	if err != nil || len(audits) != 1 || !audits[0].NewValue {
		t.Fatalf("model audit = %+v err=%v", audits, err)
	}
	forceOff := false
	if _, err := st.UpdateEndpointKeyWithPolicy(ctx, uid, eid, key.ID, nil, nil, &forceOff, testNow+1, "owner"); err != nil {
		t.Fatalf("disable key policy: %v", err)
	}
	audits, err = st.ListPolicyAudits(ctx, "endpoint_key", key.ID, 10)
	if err != nil || len(audits) != 2 || !audits[1].OldValue || audits[1].NewValue {
		t.Fatalf("key transition audit = %+v err=%v", audits, err)
	}
	if _, err := st.DB().Exec(`UPDATE policy_audits SET new_value=0 WHERE id=?`, audits[0].ID); err == nil {
		t.Fatal("policy audit update was accepted")
	}
	if _, err := st.DB().Exec(`DELETE FROM policy_audits WHERE id=?`, audits[0].ID); err == nil {
		t.Fatal("policy audit delete was accepted")
	}
	if _, err := st.DB().Exec(`UPDATE policy_audits SET actor_user_id=NULL WHERE id=?`, audits[0].ID); err == nil {
		t.Fatal("manual actor anonymization was accepted while actor still exists")
	}
	if err := st.DeleteUserAccount(ctx, uid); err != nil {
		t.Fatalf("delete actor: %v", err)
	}
	var count int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM policy_audits WHERE resource_id IN (?,?)`, key.ID, model.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("audit rows after resource cascade = %d, want 3", count)
	}
	var actors int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM policy_audits WHERE actor_user_id IS NOT NULL`).Scan(&actors); err != nil {
		t.Fatal(err)
	}
	if actors != 0 {
		t.Fatalf("actor references survived deletion: %d", actors)
	}
}

func TestPolicyAuditRejectsInvalidClosedSet(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "policy-audit-invalid.db"))
	defer st.Close()
	tx, err := st.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = appendPolicyAuditTx(context.Background(), tx, 0, "donor", "model", 1, "flatten_tool_calls", 0, 1, testNow)
	_ = tx.Rollback()
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("invalid actor role error = %v", err)
	}
}

func TestExplicitTruePolicyRevalidatesAllBindings(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "policy-revalidate.db"))
	defer st.Close()
	ctx := context.Background()
	uid := seedUserRaw(t, st, "policy-revalidate-user")

	personalEndpoint := seedEndpointRaw(t, st, uid, true)
	personalKey := seedEndpointKeyRaw(t, st, personalEndpoint, true)
	seedFetchedModelRaw(t, st, personalKey, "up/personal")
	personal, err := st.CreateModelWithPolicy(ctx, uid, "provider", "personal", "ordered", false, true, testNow, "owner")
	if err != nil {
		t.Fatalf("create flattened personal model: %v", err)
	}
	if _, err := st.CreateBinding(ctx, uid, personal.ID, personalKey, "up/personal", 0, testNow); err != nil {
		t.Fatalf("bind flattened personal model: %v", err)
	}
	if _, err := st.DB().Exec(`UPDATE endpoints SET connector_type='anthropic-compatible' WHERE id=?`, personalEndpoint); err != nil {
		t.Fatalf("corrupt personal connector fixture: %v", err)
	}
	keepTrue := true
	if _, err := st.UpdateModelWithPolicy(ctx, uid, personal.ID, nil, nil, nil, nil, &keepTrue, testNow+1, "owner"); !errors.Is(err, ErrConflict) {
		t.Fatalf("personal explicit true on mixed bindings = %v, want ErrConflict", err)
	}

	charityEndpoint := newDonationEndpoint(t, st, uid, "https://charity-policy.example.com")
	charityKey := newDonationKey(t, st, uid, charityEndpoint.ID, "sk-charity-policy-revalidate")
	if err := st.ReplaceFetchedModels(ctx, uid, charityEndpoint.ID, charityKey.ID,
		[]FetchedModel{{UpstreamModelID: "up/charity", Provider: "provider"}}, testNow); err != nil {
		t.Fatalf("seed charity fetched model: %v", err)
	}
	donation, err := st.CreateDonation(ctx, existingInput(uid, charityEndpoint.ID, []int64{charityKey.ID}, testNow))
	if err != nil {
		t.Fatalf("create donation: %v", err)
	}
	if _, err := st.ApplyDonationReview(ctx, ReviewDecision{
		DonationID: donation.ID, Role: ReviewRoleAdmin, ReviewerID: uid,
		Action: ReviewActionApprove, Now: testNow + 1,
	}); err != nil {
		t.Fatalf("approve donation: %v", err)
	}
	charitySpec := perRequestModel("policy-revalidate")
	charitySpec.FlattenToolCalls = true
	charity, err := st.CreateCharityModelWithPolicy(ctx, charitySpec, uid, "admin", testNow+2)
	if err != nil {
		t.Fatalf("create flattened charity model: %v", err)
	}
	donationKeyID := donationKeyIDForTest(t, st, donation.ID)
	if _, err := st.CreateCharityBinding(ctx, charity.ID, donationKeyID, "up/charity", 0, testNow+3); err != nil {
		t.Fatalf("bind flattened charity model: %v", err)
	}
	if _, err := st.DB().Exec(`UPDATE endpoints SET connector_type='anthropic-compatible' WHERE id=?`, charityEndpoint.ID); err != nil {
		t.Fatalf("corrupt charity connector fixture: %v", err)
	}
	if _, err := st.UpdateCharityModelWithPolicy(ctx, charity.ID,
		CharityModelUpdate{FlattenToolCalls: &keepTrue}, uid, "admin", testNow+4); !errors.Is(err, ErrConflict) {
		t.Fatalf("charity explicit true on mixed bindings = %v, want ErrConflict", err)
	}
}

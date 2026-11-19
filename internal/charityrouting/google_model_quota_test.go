package charityrouting

import (
	"errors"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

func TestGoogleModelCooldownRoutesOnlyMatchingServiceCredentialAndModel(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	_, key, _ := e.seedCandidateWithConnector(t, owner, "first-google", "model-a", contract.TypeGoogleGemini)
	_, otherKey, _ := e.seedCandidateWithConnector(t, owner, "other-google", "model-a", contract.TypeGoogleGemini)
	model := e.createModel(t, 'A')
	mid, _ := parsePositiveID(model.ID)
	e.seedBinding(t, mid, key, "model-a", 0)
	e.seedBinding(t, mid, key, "model-b", 1)
	e.seedBinding(t, mid, otherKey, "model-a", 2)
	until := routingTestNow + 117
	result, err := e.store.DB().Exec(`INSERT INTO donation_service_health(use_id,endpoint_key_id,native_service_bit,credential_version_id,streak_generation,failure_streak,next_claim_seq,next_fold_seq,backoff_generation,backoff_episode,backoff_stage,updated_at)
SELECT u.id,k.endpoint_key_id,u.native_service_bit,g.credential_version_id,nbi_u128(1),nbi_u128(2),nbi_u128(1),nbi_u128(1),1,0,0,?
FROM donation_uses u JOIN donation_keys k ON k.id=u.donation_key_id JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=r.grant_revision_id WHERE k.id=? AND u.native_service_bit=131072`, routingTestNow, key)
	if err != nil {
		t.Fatal(err)
	}
	health, _ := result.LastInsertId()
	if _, err := e.store.DB().Exec("INSERT INTO donation_service_model_cooldowns VALUES(?,'model-a','google_minute_input_tokens',?)", health, until); err != nil {
		t.Fatal(err)
	}
	ctx, _, err := requestattempt.New(t.Context(), owner, http.MethodPost, "/v1/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := e.service.chatSnapshot(ctx, mid, routingTestNow, []contract.Type{contract.TypeGoogleGemini})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Candidates()) != 2 {
		t.Fatal("wrong number of available routes", len(snap.Candidates()))
	}
	for _, c := range snap.Candidates() {
		if c.DonationKeyID == key && c.UpstreamModelID == "model-a" {
			t.Fatal("quota-limited route selected")
		}
	}
	decision := requestattempt.RouteSnapshot(ctx)
	if decision == nil {
		t.Fatal("missing rejection evidence")
	}
	found := false
	for _, o := range decision.Observations {
		if o.Reason == "google_minute_input_tokens" && o.NativeService == contract.NativeServiceGeminiGenerateContent {
			found = true
		}
	}
	if !found {
		t.Fatal(decision)
	}
	admin, err := e.service.GetBindingsAdmin(ctx, mid)
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range admin.Bindings {
		view := b.Targets[0].Availability
		if view == nil || len(view.ServiceStatuses) != 1 {
			t.Fatal("missing target status", i, view)
		}
		s := view.ServiceStatuses[0]
		if i == 0 {
			if view.State != "rate_limited" || s.ModelQuotaReason != "google_minute_input_tokens" || s.RetryAfterUntil == nil || *s.RetryAfterUntil != until {
				t.Fatal(s, view.State)
			}
		} else if view.State != "available" || s.ModelQuotaReason != "" || s.RetryAfterUntil != nil {
			t.Fatal("sibling cooldown", i, s, view.State)
		}
	}
	// A separate service-wide header can be shorter or longer than the model
	// wait. Both facts remain visible and the binding cannot recover early.
	for _, global := range []int64{until - 20, until + 20} {
		if _, err := e.store.DB().Exec("UPDATE donation_service_health SET retry_after_until=? WHERE id=?", global, health); err != nil {
			t.Fatal(err)
		}
		admin, err := e.service.GetBindingsAdmin(ctx, mid)
		if err != nil {
			t.Fatal(err)
		}
		waiting, sibling := admin.Bindings[0].Targets[0].Availability.ServiceStatuses[0], admin.Bindings[1].Targets[0].Availability.ServiceStatuses[0]
		if waiting.State != "rate_limited" || waiting.ModelQuotaReason != "google_minute_input_tokens" || waiting.RetryAfterUntil == nil || *waiting.RetryAfterUntil != max(global, until) ||
			sibling.ModelQuotaReason != "" || sibling.RetryAfterUntil == nil || *sibling.RetryAfterUntil != global {
			t.Fatal(waiting, sibling)
		}
	}
	if _, err := e.store.DB().Exec("UPDATE donation_service_health SET retry_after_until=NULL WHERE id=?", health); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`DELETE FROM charity_binding_targets WHERE donation_key_id=? OR upstream_model_id='model-b'`, otherKey); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.chatSnapshot(ctx, mid, until-1, []contract.Type{contract.TypeGoogleGemini}); !errors.Is(err, donationquota.ErrLimited) {
		t.Fatal("last candidate still cooling", err)
	}
	if snap, err := e.service.chatSnapshot(ctx, mid, until, []contract.Type{contract.TypeGoogleGemini}); err != nil || len(snap.Candidates()) != 1 {
		t.Fatal("expired scope", err)
	}
}

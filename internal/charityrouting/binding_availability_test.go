package charityrouting

import (
	"context"
	"net/http"
	"testing"
	"time"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestBindingAvailabilityScopesHealthToEachTarget(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	level := int64(6)
	steward := e.seedUser(t, false, &level)
	owner := e.seedUser(t, false, nil)
	model := e.createModel(t, 'a')
	mid, _ := parsePositiveID(model.ID)
	did, key, _ := e.seedCandidate(t, owner, 'h', "chat-upstream")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := e.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE charity_models SET model_types=? WHERE id=?`, modeltype.Set{contract.OperationChatCompletions, contract.OperationEmbeddings}, mid)
	input := BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{Targets: []BindingTargetInput{
		targetForTest(key, "chat-upstream", contract.NativeServiceChatCompletions),
		targetForTest(key, "embedding-upstream", contract.NativeServiceEmbeddings),
	}}}}
	if _, err := e.service.AddBindingsReceiptAdmin(t.Context(), mid, routingMutation(t, 'b', http.MethodPost, routeAdminBindingReceipt, []int64{mid}, input), input); err != nil {
		t.Fatal(err)
	}
	chat, _ := contract.ParseNativeServices([]contract.NativeService{contract.NativeServiceChatCompletions})
	exec(`INSERT INTO donation_service_health(use_id,endpoint_key_id,native_service_bit,credential_version_id,streak_generation,failure_streak,failure_disabled,next_claim_seq,next_fold_seq,backoff_generation,backoff_episode,backoff_stage,updated_at)
SELECT u.id,dk.endpoint_key_id,u.native_service_bit,g.credential_version_id,nbi_u128(1),nbi_u128(10),1,nbi_u128(1),nbi_u128(1),1,0,0,?
FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=r.grant_revision_id WHERE dk.id=? AND u.native_service_bit=?`, routingTestNow, key, chat.Mask())
	check := func(targets []BindingTarget) {
		t.Helper()
		if len(targets) != 2 || targets[0].Availability == nil || targets[1].Availability == nil {
			t.Fatalf("missing per-target state: %+v", targets)
		}
		failed, healthy := targets[0], targets[1]
		if failed.Availability.State == "available" || healthy.Availability.State != "available" {
			t.Fatalf("sibling target masked service pause: %+v / %+v", failed.Availability, healthy.Availability)
		}
		if len(failed.Availability.ServiceStatuses) != 1 || failed.Availability.ServiceStatuses[0].State != "failure_disabled" || failed.Availability.ServiceStatuses[0].FailureStreak != "10" {
			t.Fatalf("current chat failure missing: %+v", failed.Availability)
		}
		if len(healthy.Availability.ServiceStatuses) != 1 || healthy.Availability.ServiceStatuses[0].NativeService != contract.NativeServiceEmbeddings || healthy.Availability.ServiceStatuses[0].State != "available" {
			t.Fatalf("unrelated failure leaked to embeddings: %+v", healthy.Availability)
		}
		if !failed.Capabilities.CanBind {
			t.Fatal("runtime pause changed configuration capability")
		}
	}
	admin, err := e.service.GetBindingsAdmin(t.Context(), mid)
	if err != nil || len(admin.Bindings) != 1 {
		t.Fatalf("admin bindings: %+v %v", admin, err)
	}
	check(admin.Bindings[0].Targets)
	managed, err := e.service.GetBindingsSteward(t.Context(), steward, mid)
	if err != nil || len(managed.Bindings) != 1 {
		t.Fatalf("steward bindings: %+v %v", managed, err)
	}
	check(managed.Bindings[0].Targets)
	// Health inspection must not wait for the database's sole write connection.
	writer, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	_, adminErr := e.service.GetBindingsAdmin(ctx, mid)
	_, stewardErr := e.service.GetBindingsSteward(ctx, steward, mid)
	cancel()
	writer.Rollback()
	if adminErr != nil || stewardErr != nil {
		t.Fatalf("health reads waited for writer: admin=%v steward=%v", adminErr, stewardErr)
	}
	models, _, err := e.service.keyModelPages(t.Context(), roleAdmin, 0, did, key, 0, pagination.Default())
	if err != nil || len(models.Data) != 1 || models.Data[0].AvailableBindingCount != "1" {
		t.Fatalf("group should retain its healthy target: %+v %v", models, err)
	}
	_, bindings, err := e.service.keyModelPages(t.Context(), roleAdmin, 0, did, key, mid, pagination.Default())
	if err != nil || len(bindings.Data) != 1 || len(bindings.Data[0].Targets) != 2 || bindings.Data[0].Targets[0].Availability.ServiceStatuses[0].State != "failure_disabled" {
		t.Fatalf("inverse view lost target diagnosis: %+v %v", bindings, err)
	}
	// A request cooldown ends with the clock; a failure suspension does not.
	exec(`UPDATE donation_service_health SET failure_disabled=0,failure_streak=nbi_u128(0),retry_after_until=?`, routingTestNow+60)
	admin, err = e.service.GetBindingsAdmin(t.Context(), mid)
	if err != nil || admin.Bindings[0].Targets[0].Availability.ServiceStatuses[0].State != "rate_limited" {
		t.Fatalf("cooldown: %+v %v", admin, err)
	}
	e.clock.Store(routingTestNow + 60)
	admin, err = e.service.GetBindingsAdmin(t.Context(), mid)
	if err != nil || admin.Bindings[0].Targets[0].Availability.State != "available" {
		t.Fatalf("expired cooldown: %+v %v", admin, err)
	}
	// An unrelated selected service must not be presented as a model blocker.
	exec(`UPDATE charity_models SET model_types=? WHERE id=?`, modeltype.Set{contract.OperationChatCompletions}, mid)
	combined, _ := contract.ParseNativeServices([]contract.NativeService{contract.NativeServiceChatCompletions, contract.NativeServiceEmbeddings})
	exec(`UPDATE charity_binding_targets SET native_service_mask=? WHERE id=?`, combined.Mask(), admin.Bindings[0].Targets[0].ID)
	admin, err = e.service.GetBindingsAdmin(t.Context(), mid)
	if err != nil || len(admin.Bindings[0].Targets[0].Availability.ServiceStatuses) != 1 {
		t.Fatalf("displayed services irrelevant to model %d: %+v %v", mid, admin, err)
	}
	embeddings, _ := contract.ParseNativeServices([]contract.NativeService{contract.NativeServiceEmbeddings})
	exec(`UPDATE donation_uses SET enabled=(native_service_bit=?),revision=revision+1 WHERE donation_key_id=?`, embeddings.Mask(), key)
	exec(`INSERT INTO donation_service_health(use_id,endpoint_key_id,native_service_bit,credential_version_id,streak_generation,failure_streak,next_claim_seq,next_fold_seq,retry_after_until,backoff_generation,backoff_episode,backoff_stage,updated_at)
SELECT u.id,dk.endpoint_key_id,u.native_service_bit,g.credential_version_id,nbi_u128(1),nbi_u128(0),nbi_u128(1),nbi_u128(1),?,1,0,0,?
FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id JOIN endpoint_key_grant_revisions g ON g.id=r.grant_revision_id WHERE dk.id=? AND u.native_service_bit=?`, routingTestNow+120, routingTestNow, key, embeddings.Mask())
	admin, err = e.service.GetBindingsAdmin(t.Context(), mid)
	if err != nil || admin.Bindings[0].Targets[0].Availability.State != "unavailable" || admin.Bindings[0].Targets[0].Availability.ServiceStatuses[0].State != "disabled" {
		t.Fatalf("unrelated embeddings cooldown mislabeled paused chat: %+v %v", admin, err)
	}
}

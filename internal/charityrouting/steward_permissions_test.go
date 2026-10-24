package charityrouting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func TestConfiguredStewardCreateDeleteAndPresetRespectModelScope(t *testing.T) {
	e, channel, donationID, _, models := presetFixture(t)
	savePresetFixture(t, e, channel, models)
	five := int64(5)
	actor := e.seedUser(t, false, &five)
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["charity.presets"] = true
	policy.Permissions["charity.create"] = true
	policy.Permissions["charity.delete"] = true
	permissions, _ := json.Marshal(policy.Permissions)
	limits, _ := json.Marshal(policy.Limits)
	presetExec(t, e, `INSERT INTO steward_permissions(user_id,revision,permissions,limits,created_at,updated_at) VALUES(?,1,?,?,?,?)`, actor, string(permissions), string(limits), routingTestNow, routingTestNow)
	ctx := authz.WithStewardPermission(context.Background(), "charity.manage")
	input := testModelCreate()
	input.Model = "scoped-created"
	if _, err := e.service.CreateSteward(ctx, actor, routingMutation(t, 'U', http.MethodPost, routeStewardModels, nil, input), input); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("non-mainstream create allowed", err)
	}
	input.IsMainstream = true
	created, err := e.service.CreateSteward(ctx, actor, routingMutation(t, 'V', http.MethodPost, routeStewardModels, nil, input), input)
	if err != nil {
		t.Fatal(err)
	}
	modelID, _ := parsePositiveID(models[0])
	remove := ModelDelete{ExpectedRevision: "1", Confirmation: "delete"}
	if _, err := e.service.DeleteSteward(ctx, actor, modelID, routingMutation(t, 'W', http.MethodDelete, routeStewardModel, []int64{modelID}, remove), remove); !errors.Is(err, ErrNotFound) {
		t.Fatal("non-mainstream delete allowed", err)
	}
	modelID, _ = parsePositiveID(created.Value.ID)
	remove.ExpectedRevision = created.Value.Revision
	mutation := routingMutation(t, 'X', http.MethodDelete, routeStewardModel, []int64{modelID}, remove)
	for attempt := range 2 {
		got, err := e.service.DeleteSteward(ctx, actor, modelID, mutation, remove)
		if err != nil || got.Replayed != (attempt == 1) {
			t.Fatalf("mainstream delete/replay: %+v %v", got, err)
		}
	}
	preset, err := e.service.bindingPreset(context.Background(), roleAdmin, 0, channel)
	if err != nil {
		t.Fatal(err)
	}
	apply := ApplyBindingPreset{ChannelID: channel, ExpectedChannelRevision: preset.ChannelRevision, ExpectedPresetRevision: preset.PresetRevision, ExpectedDonationRevision: "1"}
	for _, mapping := range preset.Mappings {
		if mapping.BindingRevision != nil {
			apply.Models = append(apply.Models, PresetBindingRevision{mapping.CharityModelID, *mapping.BindingRevision})
		}
	}
	ctx = authz.WithStewardPermission(ctx, "charity.presets")
	if _, err := e.service.applyBindingPreset(ctx, roleSteward, actor, donationID, routingMutation(t, 'Y', http.MethodPost, routeStewardApplyBindingPreset, []int64{donationID}, apply), apply); !errors.Is(err, ErrNotFound) {
		t.Fatal("non-mainstream preset allowed", err)
	}
}

func TestModelFieldGrantsPreserveDefaultsAndRemainIndependent(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	five := int64(5)
	actor := e.seedUser(t, false, &five)
	input := testModelCreate()
	input.IsMainstream = true
	ctx := authz.WithStewardPermission(context.Background(), "charity.create")
	if _, err := e.service.CreateSteward(ctx, actor, routingMutation(t, 'n', http.MethodPost, routeStewardModels, nil, input), input); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("default creation allowed", err)
	}
	model := e.createModel(t, 'o')
	mid, _ := parsePositiveID(model.ID)
	presetExec(t, e, `UPDATE charity_models SET is_mainstream=1 WHERE id=?`, mid)
	ctx = authz.WithStewardPermission(context.Background(), "model_fields")
	enabled := false
	patch := ModelPatch{ExpectedRevision: "1", Enabled: &enabled}
	if _, err := e.service.PatchSteward(ctx, actor, mid, routingMutation(t, 'p', http.MethodPatch, routeStewardModel, []int64{mid}, patch), patch); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("default settings allowed", err)
	}
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["charity.manage"] = false
	policy.Permissions["charity.pricing"] = true
	permissions, _ := json.Marshal(policy.Permissions)
	limits, _ := json.Marshal(policy.Limits)
	presetExec(t, e, `INSERT INTO steward_permissions(user_id,revision,permissions,limits,created_at,updated_at) VALUES(?,1,?,?,?,?)`, actor, string(permissions), string(limits), routingTestNow, routingTestNow)
	patch = ModelPatch{ExpectedRevision: "1", Pricing: &input.Pricing}
	if _, err := e.service.PatchSteward(ctx, actor, mid, routingMutation(t, 'q', http.MethodPatch, routeStewardModel, []int64{mid}, patch), patch); err != nil {
		t.Fatal("independent pricing denied", err)
	}
	patch.ExpectedRevision = "2"
	patch.Enabled = &enabled
	if _, err := e.service.PatchSteward(ctx, actor, mid, routingMutation(t, 'r', http.MethodPatch, routeStewardModel, []int64{mid}, patch), patch); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("mixed ungranted field accepted", err)
	}
}

func TestPresetMissingTargetsStillRecheckDonationScope(t *testing.T) {
	e, channel, did, _, models := presetFixture(t)
	savePresetFixture(t, e, channel, models[:1])
	five := int64(5)
	actor := e.seedUser(t, false, &five)
	p := authz.DefaultStewardPermissions(6)
	p.DonationScope = authz.DonationScopeEnhanced
	grants, _ := json.Marshal(p.Permissions)
	limits, _ := json.Marshal(p.Limits)
	presetExec(t, e, `INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,1,?,?,'enhanced',?,?)`, actor, string(grants), string(limits), routingTestNow, routingTestNow)
	presetExec(t, e, `DELETE FROM charity_models WHERE id=?`, models[0])
	presetExec(t, e, `INSERT INTO admin_endpoint_tags(base_url,tag) VALUES('https://preset.routing.test/v1','user_gateway')`)
	input := presetApplyInput(channel, nil)
	mutation := routingMutation(t, 'Z', http.MethodPost, routeStewardApplyBindingPreset, []int64{did}, input)
	ctx := authz.WithStewardPermission(t.Context(), "charity.presets")
	out, err := e.service.applyBindingPreset(ctx, roleSteward, actor, did, mutation, input)
	if err != nil || len(out.Value.Items) != 3 || out.Value.Items[0].Results[0] != "invalid_resource" {
		t.Fatal("missing target receipt", out, err)
	}
	presetExec(t, e, `DELETE FROM admin_endpoint_tags`)
	if _, err := e.service.applyBindingPreset(ctx, roleSteward, actor, did, mutation, input); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing model bypassed tag revocation", err)
	}
	presetExec(t, e, `UPDATE steward_permissions SET donation_scope='none',revision=revision+1 WHERE user_id=?`, actor)
	if _, err := e.service.applyBindingPreset(ctx, roleSteward, actor, did, mutation, input); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("missing model bypassed none", err)
	}
}

func TestBindingScopeDenialClosesTransactionsAndDeletedReceiptRechecksSources(t *testing.T) {
	for _, response := range []string{"", "receipt"} {
		t.Run("response="+response, func(t *testing.T) {
			e := newRoutingTestEnv(t)
			e.seedUser(t, true, nil)
			five := int64(5)
			actor := e.seedUser(t, false, &five)
			owner := e.seedUser(t, false, nil)
			model := e.createModel(t, 'K')
			mid, _ := parsePositiveID(model.ID)
			_, key, _ := e.seedCandidate(t, owner, 's', "source-model")
			e.seedBinding(t, mid, key, "source-model", 0)
			var bid int64
			var revision string
			if err := e.store.DB().QueryRow(`SELECT id,(SELECT binding_revision FROM charity_models WHERE id=?) FROM charity_model_bindings WHERE charity_model_id=?`, mid, mid).Scan(&bid, &revision); err != nil {
				t.Fatal(err)
			}
			p := authz.DefaultStewardPermissions(6)
			grants, _ := json.Marshal(p.Permissions)
			limits, _ := json.Marshal(p.Limits)
			presetExec(t, e, `INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,1,?,?,'none',?,?)`, actor, string(grants), string(limits), routingTestNow, routingTestNow)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = authz.WithStewardPermission(ctx, "charity.manage")
			deletion := BindingDelete{ExpectedBindingRevision: revision, Response: response}
			mutation := routingMutation(t, 'L', http.MethodDelete, routeStewardBinding, []int64{mid, bid}, deletion)
			ordering := BindingOrder{ExpectedBindingRevision: revision, Order: []string{fmt.Sprint(bid)}, Response: response}
			for _, scope := range []string{"none", "enhanced"} {
				presetExec(t, e, `UPDATE steward_permissions SET donation_scope=?,revision=revision+1 WHERE user_id=?`, scope, actor)
				if _, err := e.service.DeleteBindingSteward(ctx, actor, mid, bid, mutation, deletion); err == nil {
					t.Fatal("scope-denied deletion allowed")
				}
				if got := e.store.DB().Stats().InUse; got != 0 {
					t.Fatal("delete denial retained transaction", got)
				}
				if _, err := e.service.OrderBindingsSteward(ctx, actor, mid, routingMutation(t, 'O', http.MethodPut, routeStewardBindingOrder, []int64{mid}, ordering), ordering); err == nil {
					t.Fatal("scope-denied order allowed")
				}
				if got := e.store.DB().Stats().InUse; got != 0 {
					t.Fatal("order denial retained transaction", got)
				}
			}
			presetExec(t, e, `INSERT INTO admin_endpoint_tags(base_url,tag) SELECT canonical_base_url,'invalid_entry' FROM donation_keys WHERE id=?`, key)
			first, err := e.service.DeleteBindingSteward(ctx, actor, mid, bid, mutation, deletion)
			if err != nil || strings.Contains(string(first.Body), "source_keys") || strings.Contains(string(first.Body), "binding_delete_response") {
				t.Fatal("delete/public receipt", first, err)
			}
			replayed, err := e.service.DeleteBindingSteward(ctx, actor, mid, bid, mutation, deletion)
			if err != nil || !replayed.Replayed || string(replayed.Body) != string(first.Body) {
				t.Fatal("delete receipt retry", replayed, err)
			}
			presetExec(t, e, `DELETE FROM admin_endpoint_tags`)
			if _, err := e.service.DeleteBindingSteward(ctx, actor, mid, bid, mutation, deletion); !errors.Is(err, ErrNotFound) {
				t.Fatal("deleted relation bypassed tag revocation", err)
			}
			if got := e.store.DB().Stats().InUse; got != 0 {
				t.Fatal("replay denial retained transaction", got)
			}
			legacy := idempotency.Decision{Kind: idempotency.Replay, HTTPStatus: first.Status, ResponseBody: first.Body}
			tx, scope, err := e.service.beginManagementTx(ctx, roleSteward, actor, mid, false, false)
			if err != nil {
				t.Fatal(err)
			}
			_, err = replayDeletedBinding(ctx, tx, legacy, scope, routingTestNow, response)
			tx.Rollback()
			if !errors.Is(err, authz.ErrPermissionDenied) {
				t.Fatal("legacy receipt established unknown restricted source", err)
			}
			presetExec(t, e, `UPDATE steward_permissions SET donation_scope='full',revision=revision+1 WHERE user_id=?`, actor)
			tx, scope, err = e.service.beginManagementTx(ctx, roleSteward, actor, mid, false, false)
			if err != nil {
				t.Fatal(err)
			}
			old, err := replayDeletedBinding(ctx, tx, legacy, scope, routingTestNow, response)
			tx.Rollback()
			if err != nil || !old.Replayed {
				t.Fatal("legacy full receipt compatibility", old, err)
			}
		})
	}
}

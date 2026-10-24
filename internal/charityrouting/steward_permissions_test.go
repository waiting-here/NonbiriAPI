package charityrouting

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
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

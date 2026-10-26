package charityrouting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
)

func TestAdaptationLimitsApplyToTheEffectiveModelBindingCombination(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	owner := env.seedUser(t, false, nil)
	_, keyID, _ := env.seedCandidate(t, owner, 'k', "upstream")
	model := env.createModel(t, 'a')
	modelID := catalogBind(t, env, model, keyID, 'b')
	var bindingID int64
	if err := env.store.DB().QueryRow(`SELECT id FROM charity_model_bindings WHERE charity_model_id=?`, modelID).Scan(&bindingID); err != nil {
		t.Fatal(err)
	}
	store, err := requestadaptation.New(requestadaptation.Config{DB: env.store.DB(), Codec: env.vault, KeyDeriver: env.vault})
	if err != nil {
		t.Fatal(err)
	}
	env.service.adaptations = store
	ctx := context.Background()
	names := make([]string, 63)
	for i := range names {
		names[i] = fmt.Sprintf("X-Forward-%d", i)
	}
	modelPatch := requestadaptation.Patch{ForwardHeaders: &requestadaptation.ListEdit{Mode: requestadaptation.ModeReplace, Values: names}}
	putModel := func(seed byte) error {
		_, err := env.service.PutAdaptation(ctx, modelID, 0, routingMutation(t, seed, http.MethodPut, routeAdminModelAdaptation, []int64{modelID}, map[string]any{"seed": seed}), modelPatch)
		return err
	}
	if err := putModel('c'); err != nil {
		t.Fatal(err)
	}
	bindingPatch := requestadaptation.Patch{FixedHeaders: &requestadaptation.MapEdit{Mode: requestadaptation.ModeReplace, Values: map[string]requestadaptation.ValueEdit{"X-Fixed-One": {Action: "replace", Value: json.RawMessage(`"value"`)}}}}
	putBinding := func(seed byte) error {
		_, err := env.service.PutAdaptation(ctx, modelID, bindingID, routingMutation(t, seed, http.MethodPut, routeAdminBindingAdaptation, []int64{modelID, bindingID}, map[string]any{"seed": seed}), bindingPatch)
		return err
	}
	if err := putBinding('d'); err != nil {
		t.Fatal(err)
	}
	bindingPatch.ExpectedRevision = 1
	bindingPatch.FixedHeaders.Values["X-Fixed-Two"] = requestadaptation.ValueEdit{Action: "replace", Value: json.RawMessage(`"value"`)}
	if err := putBinding('e'); !errors.Is(err, ErrConflict) {
		t.Fatalf("binding expanded effective budget: %v", err)
	}
	modelPatch.ExpectedRevision = 1
	modelPatch.ForwardHeaders.Values = append(names, "X-Forward-63")
	if err := putModel('f'); !errors.Is(err, ErrConflict) {
		t.Fatalf("model expanded effective budget: %v", err)
	}
}

func TestModelAdaptationAdminWriteStewardReadTraineeDenied(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	levelFive, levelSix := int64(5), int64(6)
	trainee := env.seedUser(t, false, &levelFive)
	steward := env.seedUser(t, false, &levelSix)
	adaptations, err := requestadaptation.New(requestadaptation.Config{DB: env.store.DB(), Codec: env.vault, KeyDeriver: env.vault})
	if err != nil {
		t.Fatal(err)
	}
	env.service.adaptations = adaptations
	model := env.createModel(t, 'a')
	modelID, err := strconv.ParseInt(model.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB().Exec(`UPDATE charity_models SET is_mainstream=1 WHERE id=?`, modelID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := env.service.GetAdaptation(ctx, roleSteward, trainee, modelID, 0); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatalf("level 5 adaptation read: %v", err)
	}
	patch, err := requestadaptation.ParsePatch([]byte(`{"expected_revision":"0","fixed_headers":{"mode":"replace","values":{"X-Research":{"action":"replace","value":"private-charity-header"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	mutation := routingMutation(t, 'r', http.MethodPut, routeAdminModelAdaptation, []int64{modelID}, map[string]any{"adaptation": true})
	result, err := env.service.PutAdaptation(ctx, modelID, 0, mutation, patch)
	if err != nil || result.Value.Revision != "1" {
		t.Fatalf("admin adaptation write: %+v, %v", result.Value, err)
	}
	var auditRole, changed string
	if err := env.store.DB().QueryRow(`SELECT actor_role,changed_partitions FROM request_adaptation_audits WHERE scope='charity_model' AND resource_id=? AND revision=1`, modelID).Scan(&auditRole, &changed); err != nil || auditRole != "admin" || changed != `["fixed_headers"]` {
		t.Fatalf("model audit = (%q,%q), %v", auditRole, changed, err)
	}
	view, err := env.service.GetAdaptation(ctx, roleSteward, steward, modelID, 0)
	if err != nil || !view.FixedHeaders.Values["X-Research"].HasValue {
		t.Fatalf("level 6 redacted read: %+v, %v", view, err)
	}
	encoded, _ := json.Marshal(view)
	if !strings.Contains(string(encoded), "private-charity-header") || string(view.FixedHeaders.Values["X-Research"].Value) != `"private-charity-header"` {
		t.Fatal("level 6 configuration did not reveal unprotected value")
	}
	receipt, _ := json.Marshal(result.Value)
	if strings.Contains(string(receipt), "private-charity-header") {
		t.Fatal("save receipt exposed a configured value")
	}
	replay, err := env.service.PutAdaptation(ctx, modelID, 0, mutation, patch)
	if err != nil {
		t.Fatal(err)
	}
	replayWire, _ := json.Marshal(replay.Value)
	if string(replayWire) != string(receipt) {
		t.Fatal("replayed receipt changed")
	}
	var unsafeReceipts int
	if err := env.store.DB().QueryRow(`SELECT COUNT(*) FROM idempotency_records WHERE instr(CAST(response_body AS TEXT),'private-charity-header')>0`).Scan(&unsafeReceipts); err != nil || unsafeReceipts != 0 {
		t.Fatalf("persisted unsafe receipts = %d, %v", unsafeReceipts, err)
	}
	if _, err := env.service.GetAdaptation(ctx, roleAdmin, 0, modelID, modelID+1000); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-model binding read: %v", err)
	}
}

func TestBindingAdaptationConfigurationInheritanceAndSafeReceipts(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	levelFive, levelSix := int64(5), int64(6)
	trainee := env.seedUser(t, false, &levelFive)
	steward := env.seedUser(t, false, &levelSix)
	owner := env.seedUser(t, false, nil)
	_, keyID, _ := env.seedCandidate(t, owner, 'k', "upstream")
	model := env.createModel(t, 'a')
	modelID := catalogBind(t, env, model, keyID, 'b')
	var bindingID int64
	if err := env.store.DB().QueryRow(`SELECT id FROM charity_model_bindings WHERE charity_model_id=?`, modelID).Scan(&bindingID); err != nil {
		t.Fatal(err)
	}
	adaptations, err := requestadaptation.New(requestadaptation.Config{DB: env.store.DB(), Codec: env.vault, KeyDeriver: env.vault})
	if err != nil {
		t.Fatal(err)
	}
	env.service.adaptations = adaptations
	patch, err := requestadaptation.ParsePatch([]byte(`{"expected_revision":"0","body_defaults":{"mode":"replace","values":{"/null":{"action":"replace","value":null},"/partial":{"action":"replace","value":{"token":"protected-token","mode":"also-hidden"},"sensitive_paths":["/token"]}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.service.PutAdaptation(t.Context(), modelID, 0, routingMutation(t, 'c', http.MethodPut, routeAdminModelAdaptation, []int64{modelID}, map[string]any{"defaults": true}), patch); err != nil {
		t.Fatal(err)
	}
	patch, err = requestadaptation.ParsePatch([]byte(`{"expected_revision":"0","fixed_headers":{"mode":"replace","values":{"X-Plain":{"action":"replace","value":"binding-value"},"X-Protected":{"action":"replace","value":"protected-header","sensitive_paths":[""]}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	mutation := routingMutation(t, 'd', http.MethodPut, routeAdminBindingAdaptation, []int64{modelID, bindingID}, map[string]any{"binding": true})
	result, err := env.service.PutAdaptation(t.Context(), modelID, bindingID, mutation, patch)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := env.service.PutAdaptation(t.Context(), modelID, bindingID, mutation, patch)
	if err != nil {
		t.Fatal(err)
	}
	for _, receipt := range []adaptationView{result.Value, replay.Value} {
		wire, err := json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Effective == nil || strings.Contains(string(wire), `"value":`) || strings.Contains(string(wire), "binding-value") || strings.Contains(string(wire), "protected-") || strings.Contains(string(wire), "also-hidden") {
			t.Fatal("binding receipt or effective receipt exposed a value")
		}
	}
	for _, actor := range []struct {
		role roleKind
		id   int64
	}{{roleAdmin, 0}, {roleSteward, steward}} {
		view, err := env.service.GetAdaptation(t.Context(), actor.role, actor.id, modelID, bindingID)
		if err != nil {
			t.Fatal(err)
		}
		if string(view.FixedHeaders.Values["X-Plain"].Value) != `"binding-value"` || view.FixedHeaders.Values["X-Protected"].Value != nil || view.Effective == nil {
			t.Fatal("binding configuration did not respect markers")
		}
		if view.Effective.Revision != "1/1" || view.Effective.BodyDefaults.Source != requestadaptation.ScopeCharityModel || view.Effective.FixedHeaders.Source != requestadaptation.ScopeBinding {
			t.Fatal("effective configuration lost inheritance metadata")
		}
		if string(view.Effective.BodyDefaults.Values["/null"].Value) != "null" || view.Effective.BodyDefaults.Values["/partial"].Value != nil || string(view.Effective.FixedHeaders.Values["X-Plain"].Value) != `"binding-value"` {
			t.Fatal("effective configuration did not respect values and markers")
		}
		wire, _ := json.Marshal(view)
		if strings.Contains(string(wire), "protected-token") || strings.Contains(string(wire), "protected-header") || strings.Contains(string(wire), "also-hidden") {
			t.Fatal("protected value escaped binding configuration")
		}
	}
	if _, err := env.store.DB().Exec(`UPDATE charity_models SET is_mainstream=1 WHERE id=?`, modelID); err != nil {
		t.Fatal(err)
	}
	for _, binding := range []int64{0, bindingID} {
		if _, err := env.service.GetAdaptation(t.Context(), roleSteward, trainee, modelID, binding); !errors.Is(err, authz.ErrPermissionDenied) {
			t.Fatalf("level 5 configuration read = %v", err)
		}
		if _, err := env.service.GetAdaptation(t.Context(), roleSteward, owner, modelID, binding); !errors.Is(err, ErrForbidden) {
			t.Fatalf("ordinary configuration read = %v", err)
		}
	}
	var unsafeReceipts int
	if err := env.store.DB().QueryRow(`SELECT COUNT(*) FROM idempotency_records WHERE instr(CAST(response_body AS TEXT),'binding-value')>0 OR instr(CAST(response_body AS TEXT),'protected-token')>0 OR instr(CAST(response_body AS TEXT),'protected-header')>0 OR instr(CAST(response_body AS TEXT),'also-hidden')>0`).Scan(&unsafeReceipts); err != nil || unsafeReceipts != 0 {
		t.Fatalf("persisted unsafe binding receipts = %d, %v", unsafeReceipts, err)
	}
}

package charityrouting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if _, err := env.service.GetAdaptation(ctx, roleSteward, trainee, modelID, 0); !errors.Is(err, ErrForbidden) {
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
	if strings.Contains(string(encoded), "private-charity-header") {
		t.Fatalf("sensitive value in steward projection: %s", encoded)
	}
	if _, err := env.service.GetAdaptation(ctx, roleAdmin, 0, modelID, modelID+1000); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-model binding read: %v", err)
	}
}

package charityrouting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"net/http"
	"strings"
	"testing"
)

func (environment *routingTestEnv) seedPresetCandidate(t *testing.T, ownerID, donationID int64, identity, upstream string, connectorType connectorcontract.Type, channel ...string) (int64, int64, int64) {
	return environment.seedCandidateAt(t, ownerID, donationID, identity, "https://preset.routing.test/v1", upstream, connectorType, channel...)
}
func presetExec(t *testing.T, e *routingTestEnv, q string, args ...any) {
	t.Helper()
	if _, err := e.store.DB().Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}
func presetFixture(t *testing.T) (*routingTestEnv, string, int64, []int64, []string) {
	t.Helper()
	return presetSourceFixture(t, true)
}
func presetSourceFixture(t *testing.T, mainstream bool) (*routingTestEnv, string, int64, []int64, []string) {
	t.Helper()
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	channel, err := db.GenerateOpaqueID("mch_")
	if err != nil {
		t.Fatal(err)
	}
	seedRoutingChannel(t, e, channel, "Example", "https://preset.routing.test/v1")
	var donationID int64
	keys := []int64{}
	models := []string{}
	channels := []string{}
	if mainstream {
		channels = append(channels, channel)
	}
	for i := range 3 {
		did, key, physical := e.seedPresetCandidate(t, owner, donationID, fmt.Sprint(i), "up-0", connectorcontract.TypeOpenAICompatible, channels...)
		donationID = did
		keys = append(keys, key)
		catalog := []testseed.DiscoveryModel{{ID: "up-0"}}
		for j := 1; j < 6; j++ {
			if i == 1 && j == 3 {
				continue
			}
			catalog = append(catalog, testseed.DiscoveryModel{ID: fmt.Sprint("up-", j)})
		}
		tx, err := e.store.DB().BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := testseed.SeedDiscoveryCatalogTx(t.Context(), tx, testseed.DiscoveryCatalogInput{EndpointKeyID: physical, ListFormat: "openai", Models: catalog, At: routingTestNow}); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 6 {
		input := testModelCreate()
		input.Model = fmt.Sprint("preset-", i)
		out, err := e.service.CreateAdmin(t.Context(), routingMutation(t, byte('A'+i), http.MethodPost, routeAdminModels, nil, input), input)
		if err != nil {
			t.Fatal(err)
		}
		models = append(models, out.Value.ID)
	}
	return e, channel, donationID, keys, models
}
func savePresetFixture(t *testing.T, e *routingTestEnv, channel string, models []string) BindingPreset {
	t.Helper()
	input := BindingPresetInput{ExpectedChannelRevision: "1", ExpectedPresetRevision: "0", Mappings: []BindingPresetPair{}}
	for i, id := range models {
		input.Mappings = append(input.Mappings, BindingPresetPair{fmt.Sprint("up-", i), id})
	}
	mutation := routingMutation(t, 'S', http.MethodPut, routeAdminBindingPreset, nil, input)
	mutation.PathIDs = []string{channel}
	out, err := e.service.saveBindingPreset(t.Context(), roleAdmin, 0, channel, mutation, input)
	if err != nil {
		t.Fatal(err)
	}
	return out.Value
}
func presetApplyInput(channel string, models []string) ApplyBindingPreset {
	out := ApplyBindingPreset{ChannelID: channel, ExpectedChannelRevision: "1", ExpectedPresetRevision: "1", ExpectedDonationRevision: "1", Models: []PresetBindingRevision{}}
	for _, id := range models {
		out.Models = append(out.Models, PresetBindingRevision{id, "0"})
	}
	return out
}
func TestPresetBindingMatrixSkipsMissingAndReplaysWithoutReordering(t *testing.T) {
	e, channel, did, keys, models := presetFixture(t)
	savePresetFixture(t, e, channel, models)
	input := presetApplyInput(channel, models)
	mutation := routingMutation(t, 'T', http.MethodPost, routeAdminApplyBindingPreset, []int64{did}, input)
	out, err := e.service.applyBindingPreset(t.Context(), roleAdmin, 0, did, mutation, input)
	if err != nil || out.Value.AddedCount != "17" || len(out.Value.Items) != 3 {
		t.Fatalf("matrix %+v %v", out.Value, err)
	}
	for row, item := range out.Value.Items {
		if item.DonationKeyID != fmt.Sprint(keys[row]) || len(item.Results) != 6 {
			t.Fatal(item)
		}
		for col, status := range item.Results {
			want := "added"
			if row == 1 && col == 3 {
				want = "missing_model"
			}
			if status != want {
				t.Fatalf("%d %d: %s", row, col, status)
			}
		}
	}
	replayed, err := e.service.applyBindingPreset(t.Context(), roleAdmin, 0, did, mutation, input)
	if err != nil || !replayed.Replayed || string(replayed.Body) != string(out.Body) {
		t.Fatal("replay", err)
	}
	for i := range input.Models {
		input.Models[i].ExpectedBindingRevision = "1"
	}
	repeated, err := e.service.applyBindingPreset(t.Context(), roleAdmin, 0, did, routingMutation(t, 'U', http.MethodPost, routeAdminApplyBindingPreset, []int64{did}, input), input)
	if err != nil || repeated.Value.AddedCount != "0" {
		t.Fatalf("repeat %+v %v", repeated.Value, err)
	}
	for _, model := range repeated.Value.Models {
		if model.BindingRevision != "1" {
			t.Fatal("repeat advanced revision", model)
		}
	}
	for row, item := range repeated.Value.Items {
		for col, status := range item.Results {
			want := "already_bound"
			if row == 1 && col == 3 {
				want = "missing_model"
			}
			if status != want {
				t.Fatal(status)
			}
		}
	}
	var order string
	if err := e.store.DB().QueryRow(`SELECT group_concat(donation_key_id,',') FROM (SELECT t.donation_key_id FROM charity_model_bindings b JOIN charity_binding_targets t ON t.binding_id=b.id WHERE b.charity_model_id=? ORDER BY b.ord,t.ord)`, models[0]).Scan(&order); err != nil || order != fmt.Sprintf("%d,%d,%d", keys[0], keys[1], keys[2]) {
		t.Fatal(order, err)
	}
	// Source menus include fully bound sources only on explicit request.
	mid, _ := parsePositiveID(models[0])
	candidates, _, _, err := e.service.BindingCandidatesAdmin(t.Context(), mid, CandidateQuery{DonationID: did, DonationKeyID: keys[0], Limit: 50, IncludeBound: true})
	if err != nil || len(candidates) != 6 || candidates[0].CurrentBindingID == nil {
		t.Fatalf("markers %+v %v", candidates, err)
	}
	if _, _, _, err := e.service.bindingSources(t.Context(), roleAdmin, 0, mid, did, 0, 50, "", true); err != nil {
		t.Fatal(err)
	}
}
func TestManualMultiTargetBindingRollbackAuthorizationAndRevision(t *testing.T) {
	e, channel, did, keys, models := presetFixture(t)
	ctx := context.Background()
	input := KeyBindingBatch{ExpectedDonationRevision: "1", Targets: []KeyBindingTarget{{models[0], "0", "up-0", nil}, {models[1], "0", "up-1", nil}, {"999999", "0", "up-2", nil}, {models[2], "0", "missing", nil}}}
	mutation := routingMutation(t, 'V', http.MethodPost, routeAdminKeyBindingBatch, []int64{did, keys[0]}, input)
	presetExec(t, e, `CREATE TRIGGER reject_second_binding BEFORE INSERT ON charity_model_bindings WHEN NEW.charity_model_id=`+models[1]+` BEGIN SELECT RAISE(ABORT,'synthetic failure');END`)
	if _, err := e.service.bindKeyTargets(ctx, roleAdmin, 0, did, keys[0], mutation, input); err == nil {
		t.Fatal("database error accepted")
	}
	var count int
	if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM charity_model_bindings`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial write", count, err)
	}
	presetExec(t, e, `DROP TRIGGER reject_second_binding`)
	out, err := e.service.bindKeyTargets(ctx, roleAdmin, 0, did, keys[0], mutation, input)
	if err != nil || strings.Join(out.Value.Items[0].Results, ",") != "added,added,invalid_resource,missing_model" {
		t.Fatalf("result %+v %v", out.Value, err)
	}
	if _, err := e.service.bindKeyTargets(ctx, roleAdmin, 0, did, keys[0], routingMutation(t, 'W', http.MethodPost, routeAdminKeyBindingBatch, []int64{did, keys[0]}, input), input); !errors.Is(err, ErrConflict) {
		t.Fatal("stale accepted", err)
	}
	five := int64(5)
	trainee := e.seedUser(t, false, &five)
	presetExec(t, e, `UPDATE charity_models SET is_mainstream=1 WHERE id=?`, models[0])
	scoped := KeyBindingBatch{ExpectedDonationRevision: "1", Targets: []KeyBindingTarget{{models[0], "1", "up-2", nil}, {models[1], "1", "up-3", nil}}}
	if _, err := e.service.bindKeyTargets(ctx, roleSteward, trainee, did, keys[0], routingMutation(t, 'X', http.MethodPost, routeStewardKeyBindingBatch, []int64{did, keys[0]}, scoped), scoped); !errors.Is(err, ErrNotFound) {
		t.Fatal("trainee crossed target scope", err)
	}
	options, err := e.service.keyBindingOptions(ctx, roleSteward, trainee, did, keys[0], "", pagination.Default())
	if err != nil || len(options.Data) != 1 || options.Data[0].CharityModelID != models[0] {
		t.Fatalf("options %+v %v", options, err)
	}
	scoped.Targets = scoped.Targets[:1]
	operation := routingMutation(t, 'Y', http.MethodPost, routeStewardKeyBindingBatch, []int64{did, keys[0]}, scoped)
	if _, err := e.service.bindKeyTargets(ctx, roleSteward, trainee, did, keys[0], operation, scoped); err != nil {
		t.Fatal(err)
	}
	presetExec(t, e, `UPDATE charity_models SET is_mainstream=0,revision=revision+1 WHERE id=?`, models[0])
	if _, err := e.service.bindKeyTargets(ctx, roleSteward, trainee, did, keys[0], operation, scoped); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked replay", err)
	}
	if _, err := e.service.bindingPreset(ctx, roleSteward, trainee, channel); !errors.Is(err, ErrForbidden) {
		t.Fatal("trainee preset access", err)
	}
	presetExec(t, e, `UPDATE donations SET status='deleted',terminal_at=?,revision=revision+1 WHERE id=?`, routingTestNow, did)
	if _, err := e.service.bindKeyTargets(ctx, roleAdmin, 0, did, keys[0], mutation, input); !errors.Is(err, ErrConflict) {
		t.Fatal("terminal replay", err)
	}
}
func TestBindingMatrixReceiptFitsExistingLimit(t *testing.T) {
	for _, test := range []struct {
		name          string
		keys, targets int
	}{
		{"manual", 1, maxKeyBindingTargets}, {"preset", 100, maxChannelBindingMappings},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := BindingMatrixReceipt{Items: []BindingMatrixRow{}, Models: []BindingModelRevision{}, AddedCount: fmt.Sprint(test.keys * test.targets)}
			for range test.keys {
				row := BindingMatrixRow{DonationKeyID: "9223372036854775807", Results: []string{}}
				for range test.targets {
					row.Results = append(row.Results, "capacity_exceeded")
				}
				result.Items = append(result.Items, row)
			}
			for range test.targets {
				result.Models = append(result.Models, BindingModelRevision{"9223372036854775807", "9223372036854775807"})
			}
			body, err := json.Marshal(result)
			if err != nil || len(body) > idempotency.MaxResponseBytes {
				t.Fatal(len(body), err)
			}
		})
	}
}

func TestMultiTargetBindingPreservesSelectedNativeServicesAndReplay(t *testing.T) {
	e, _, did, keys, models := presetFixture(t)
	selected := []connectorcontract.NativeService{connectorcontract.NativeServiceChatCompletions}
	input := KeyBindingBatch{ExpectedDonationRevision: "1", Targets: []KeyBindingTarget{{CharityModelID: models[0], ExpectedBindingRevision: "0", UpstreamModelID: "up-0", NativeServices: selected}}}
	mutation := routingMutation(t, 'N', http.MethodPost, routeAdminKeyBindingBatch, []int64{did, keys[0]}, input)
	out, err := e.service.bindKeyTargets(t.Context(), roleAdmin, 0, did, keys[0], mutation, input)
	if err != nil || out.Value.AddedCount != "1" {
		t.Fatalf("subset binding %+v %v", out, err)
	}
	for range 2 {
		replayed, err := e.service.bindKeyTargets(t.Context(), roleAdmin, 0, did, keys[0], mutation, input)
		if err != nil || !replayed.Replayed || string(replayed.Body) != string(out.Body) {
			t.Fatal("unknown-result retry", err)
		}
	}
	set, _ := connectorcontract.ParseNativeServices(selected)
	var mask int64
	var origin string
	if err := e.store.DB().QueryRow(`SELECT native_service_mask,selection_origin FROM charity_binding_targets WHERE donation_key_id=?`, keys[0]).Scan(&mask, &origin); err != nil || mask != set.Mask() || origin != "reviewer" {
		t.Fatalf("subset not retained: mask=%d origin=%s err=%v", mask, origin, err)
	}
	for _, invalid := range [][]connectorcontract.NativeService{{}, {"unknown"}} {
		input.Targets[0].NativeServices = invalid
		if _, err := e.service.bindKeyTargets(t.Context(), roleAdmin, 0, did, keys[0], mutation, input); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal("invalid selection accepted before replay", err)
		}
	}
	input.Targets[0].NativeServices = []connectorcontract.NativeService{connectorcontract.NativeServiceEmbeddings}
	changed := routingMutation(t, 'N', http.MethodPost, routeAdminKeyBindingBatch, []int64{did, keys[0]}, input)
	if _, err := e.service.bindKeyTargets(t.Context(), roleAdmin, 0, did, keys[0], changed, input); !errors.Is(err, ErrConflict) {
		t.Fatal("changed subset reused receipt", err)
	}
}
